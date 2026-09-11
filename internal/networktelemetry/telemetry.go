// Package networktelemetry defines provider-neutral, read-only network
// observations for Capacity Planner inputs. It does not discover topology,
// persist data, or expose any network mutation primitive.
package networktelemetry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/netip"
	"sort"
	"strings"
	"time"
)

const (
	ObservationSchemaV1    = "network.telemetry.observation/v1"
	BatchSchemaV1          = "network.telemetry.batch/v1"
	BoundarySchemaV1       = "network.telemetry.boundary/v1"
	RecommendationInputV1  = "network.telemetry.recommendation-input/v1"
	maxObservations        = 1024
	maxBoundaries          = 256
	maxIdentifierLength    = 128
	maxFreshnessSeconds    = 24 * 60 * 60
	maxSampleWindow        = 24 * time.Hour
	maxSamplesPerWindow    = uint64(1_000_000_000_000)
	maxSamplesPerAggregate = uint64(1_000_000_000_000_000)
	maxEncodedBatchBytes   = 2 * 1024 * 1024
	maximumUnboundedMetric = 1e15
	maximumLatencyMillis   = 600_000
	maximumErrorsPerSecond = 1e12
	minimumSampleWindow    = time.Second
)

var (
	ErrUnsupportedSchema  = errors.New("unsupported network telemetry schema")
	ErrInvalidBatch       = errors.New("invalid network telemetry batch")
	ErrInvalidObservation = errors.New("invalid network telemetry observation")
	ErrStaleObservation   = errors.New("stale network telemetry observation")
	ErrInvalidBoundary    = errors.New("invalid network telemetry boundary")
	ErrInvalidInput       = errors.New("invalid network recommendation input")
)

// TargetKind is a logical aggregation level. IDs are opaque inventory IDs;
// this contract deliberately carries no address or topology relationships.
type TargetKind string

const (
	TargetInterface TargetKind = "interface"
	TargetZone      TargetKind = "zone"
	TargetSite      TargetKind = "site"
)

type Target struct {
	Kind TargetKind `json:"kind"`
	ID   string     `json:"id"`
}

// Metric is a closed set of higher-is-worse pressure measurements. Receive
// and transmit series stay separate so aggregation never invents a direction.
type Metric string

const (
	MetricReceiveThroughput   Metric = "network.throughput.receive"
	MetricTransmitThroughput  Metric = "network.throughput.transmit"
	MetricReceiveUtilization  Metric = "network.utilization.receive"
	MetricTransmitUtilization Metric = "network.utilization.transmit"
	MetricReceiveErrors       Metric = "network.errors.receive"
	MetricTransmitErrors      Metric = "network.errors.transmit"
	MetricLatency             Metric = "network.latency"
	MetricPacketLoss          Metric = "network.packet-loss"
)

type Unit string

const (
	UnitBitsPerSecond   Unit = "bits-per-second"
	UnitPercent         Unit = "percent"
	UnitErrorsPerSecond Unit = "errors-per-second"
	UnitMilliseconds    Unit = "milliseconds"
)

type ConfidenceLevel string

const (
	ConfidenceLow    ConfidenceLevel = "low"
	ConfidenceMedium ConfidenceLevel = "medium"
	ConfidenceHigh   ConfidenceLevel = "high"
)

type Confidence struct {
	Level ConfidenceLevel `json:"level"`
	Score float64         `json:"score"`
}

type SampleWindow struct {
	StartedAt   time.Time `json:"started_at"`
	EndedAt     time.Time `json:"ended_at"`
	SampleCount uint64    `json:"sample_count"`
}

// Observation is a scalar summary of a bounded sample window. It intentionally
// has no provider, endpoint, address, credential, interface relationship, or
// desired-state fields.
type Observation struct {
	SchemaVersion string       `json:"schema_version"`
	ID            string       `json:"id"`
	Target        Target       `json:"target"`
	Metric        Metric       `json:"metric"`
	Value         float64      `json:"value"`
	Unit          Unit         `json:"unit"`
	Window        SampleWindow `json:"window"`
	Confidence    Confidence   `json:"confidence"`
}

type Batch struct {
	SchemaVersion string        `json:"schema_version"`
	MaxAgeSeconds uint32        `json:"max_age_seconds"`
	Observations  []Observation `json:"observations"`
}

// Aggregate is a deterministic per-target/per-metric reduction. LatestValue
// feeds the bottleneck input; WeightedAverage and PeakValue retain bounded
// planning context without changing the source observations.
type Aggregate struct {
	Target              Target     `json:"target"`
	Metric              Metric     `json:"metric"`
	Unit                Unit       `json:"unit"`
	LatestObservationID string     `json:"latest_observation_id"`
	LatestValue         float64    `json:"latest_value"`
	WeightedAverage     float64    `json:"weighted_average"`
	PeakValue           float64    `json:"peak_value"`
	SampleCount         uint64     `json:"sample_count"`
	WindowStartedAt     time.Time  `json:"window_started_at"`
	WindowEndedAt       time.Time  `json:"window_ended_at"`
	FreshnessAgeSeconds uint32     `json:"freshness_age_seconds"`
	Confidence          Confidence `json:"confidence"`
}

// Boundary gives Capacity Planner a safe and a technical upper bound. It is
// advisory input, never an instruction to alter a host or network.
type Boundary struct {
	SchemaVersion  string  `json:"schema_version"`
	ID             string  `json:"id"`
	Target         Target  `json:"target"`
	Metric         Metric  `json:"metric"`
	Unit           Unit    `json:"unit"`
	SafeLimit      float64 `json:"safe_limit"`
	TechnicalLimit float64 `json:"technical_limit"`
}

type BottleneckInput struct {
	BoundaryID              string     `json:"boundary_id"`
	Target                  Target     `json:"target"`
	Metric                  Metric     `json:"metric"`
	Unit                    Unit       `json:"unit"`
	ObservationID           string     `json:"observation_id"`
	ObservedValue           float64    `json:"observed_value"`
	ObservedAt              time.Time  `json:"observed_at"`
	SafeLimit               float64    `json:"safe_limit"`
	TechnicalLimit          float64    `json:"technical_limit"`
	SafeReserve             float64    `json:"safe_reserve"`
	SafeReservePercent      float64    `json:"safe_reserve_percent"`
	TechnicalReserve        float64    `json:"technical_reserve"`
	TechnicalReservePercent float64    `json:"technical_reserve_percent"`
	FreshnessAgeSeconds     uint32     `json:"freshness_age_seconds"`
	Confidence              Confidence `json:"confidence"`
}

// RecommendationInput is calculation-only input for Capacity Planner. The
// explicit safety flags prevent this data from being mistaken for an action.
type RecommendationInput struct {
	SchemaVersion      string          `json:"schema_version"`
	EvaluatedAt        time.Time       `json:"evaluated_at"`
	Aggregates         []Aggregate     `json:"aggregates"`
	Bottleneck         BottleneckInput `json:"bottleneck"`
	AdvisoryOnly       bool            `json:"advisory_only"`
	ProductionMutation bool            `json:"production_mutation"`
}

type metricDefinition struct {
	Unit    Unit
	Maximum float64
}

var metricDefinitions = map[Metric]metricDefinition{
	MetricReceiveThroughput:   {Unit: UnitBitsPerSecond, Maximum: maximumUnboundedMetric},
	MetricTransmitThroughput:  {Unit: UnitBitsPerSecond, Maximum: maximumUnboundedMetric},
	MetricReceiveUtilization:  {Unit: UnitPercent, Maximum: 100},
	MetricTransmitUtilization: {Unit: UnitPercent, Maximum: 100},
	MetricReceiveErrors:       {Unit: UnitErrorsPerSecond, Maximum: maximumErrorsPerSecond},
	MetricTransmitErrors:      {Unit: UnitErrorsPerSecond, Maximum: maximumErrorsPerSecond},
	MetricLatency:             {Unit: UnitMilliseconds, Maximum: maximumLatencyMillis},
	MetricPacketLoss:          {Unit: UnitPercent, Maximum: 100},
}

// DecodeBatchJSON rejects unknown fields and trailing documents before normal
// validation. Callers that accept untrusted input should use
// DecodeAndNormalizeBatchJSON so structural and semantic checks are atomic.
func DecodeBatchJSON(reader io.Reader) (Batch, error) {
	if reader == nil {
		return Batch{}, fmt.Errorf("%w: reader is required", ErrInvalidBatch)
	}
	payload, err := io.ReadAll(io.LimitReader(reader, maxEncodedBatchBytes+1))
	if err != nil {
		return Batch{}, fmt.Errorf("%w: read payload: %v", ErrInvalidBatch, err)
	}
	if len(payload) > maxEncodedBatchBytes {
		return Batch{}, fmt.Errorf("%w: encoded payload exceeds %d bytes", ErrInvalidBatch, maxEncodedBatchBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var batch Batch
	if err := decoder.Decode(&batch); err != nil {
		return Batch{}, fmt.Errorf("%w: decode payload: %v", ErrInvalidBatch, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Batch{}, fmt.Errorf("%w: multiple JSON documents are forbidden", ErrInvalidBatch)
		}
		return Batch{}, fmt.Errorf("%w: trailing payload: %v", ErrInvalidBatch, err)
	}
	return batch, nil
}

// DecodeAndNormalizeBatchJSON is the fail-closed ingress for untrusted JSON.
func DecodeAndNormalizeBatchJSON(reader io.Reader, evaluatedAt time.Time) (Batch, error) {
	batch, err := DecodeBatchJSON(reader)
	if err != nil {
		return Batch{}, err
	}
	return NormalizeBatch(batch, evaluatedAt)
}

// NormalizeBatch validates freshness against a caller-supplied trusted clock,
// returns a sorted defensive copy, and rejects overlapping sample windows.
func NormalizeBatch(batch Batch, evaluatedAt time.Time) (Batch, error) {
	if strings.TrimSpace(batch.SchemaVersion) != BatchSchemaV1 {
		return Batch{}, fmt.Errorf("%w: batch %q", ErrUnsupportedSchema, batch.SchemaVersion)
	}
	if evaluatedAt.IsZero() {
		return Batch{}, fmt.Errorf("%w: evaluated_at is required", ErrInvalidBatch)
	}
	evaluatedAt = evaluatedAt.UTC()
	if batch.MaxAgeSeconds == 0 || batch.MaxAgeSeconds > maxFreshnessSeconds {
		return Batch{}, fmt.Errorf("%w: max_age_seconds must be in 1..%d", ErrInvalidBatch, maxFreshnessSeconds)
	}
	if len(batch.Observations) == 0 || len(batch.Observations) > maxObservations {
		return Batch{}, fmt.Errorf("%w: observations must contain 1..%d items", ErrInvalidBatch, maxObservations)
	}

	result := Batch{SchemaVersion: BatchSchemaV1, MaxAgeSeconds: batch.MaxAgeSeconds}
	result.Observations = make([]Observation, 0, len(batch.Observations))
	ids := make(map[string]struct{}, len(batch.Observations))
	for _, candidate := range batch.Observations {
		observation, err := normalizeObservation(candidate, evaluatedAt, time.Duration(batch.MaxAgeSeconds)*time.Second)
		if err != nil {
			return Batch{}, err
		}
		idKey := strings.ToLower(observation.ID)
		if _, duplicate := ids[idKey]; duplicate {
			return Batch{}, fmt.Errorf("%w: duplicate observation id %q", ErrInvalidBatch, observation.ID)
		}
		ids[idKey] = struct{}{}
		result.Observations = append(result.Observations, observation)
	}
	sort.Slice(result.Observations, func(i, j int) bool { return observationLess(result.Observations[i], result.Observations[j]) })
	for i := 1; i < len(result.Observations); i++ {
		previous := result.Observations[i-1]
		current := result.Observations[i]
		if observationSeriesKey(previous) == observationSeriesKey(current) && current.Window.StartedAt.Before(previous.Window.EndedAt) {
			return Batch{}, fmt.Errorf("%w: observations %q and %q have overlapping windows", ErrInvalidBatch, previous.ID, current.ID)
		}
	}
	return result, nil
}

func normalizeObservation(value Observation, evaluatedAt time.Time, maxAge time.Duration) (Observation, error) {
	if strings.TrimSpace(value.SchemaVersion) != ObservationSchemaV1 {
		return Observation{}, fmt.Errorf("%w: observation %q", ErrUnsupportedSchema, value.SchemaVersion)
	}
	value.SchemaVersion = ObservationSchemaV1
	value.ID = strings.TrimSpace(value.ID)
	if err := validateIdentifier("observation.id", value.ID); err != nil {
		return Observation{}, fmt.Errorf("%w: %v", ErrInvalidObservation, err)
	}
	target, err := normalizeTarget(value.Target)
	if err != nil {
		return Observation{}, fmt.Errorf("%w: %v", ErrInvalidObservation, err)
	}
	value.Target = target
	value.Metric = Metric(strings.ToLower(strings.TrimSpace(string(value.Metric))))
	value.Unit = Unit(strings.ToLower(strings.TrimSpace(string(value.Unit))))
	definition, exists := metricDefinitions[value.Metric]
	if !exists || value.Unit != definition.Unit {
		return Observation{}, fmt.Errorf("%w: metric %q does not use unit %q", ErrInvalidObservation, value.Metric, value.Unit)
	}
	if !finite(value.Value) || value.Value < 0 || value.Value > definition.Maximum {
		return Observation{}, fmt.Errorf("%w: metric %q value is outside supported bounds", ErrInvalidObservation, value.Metric)
	}
	if value.Window.StartedAt.IsZero() || value.Window.EndedAt.IsZero() || !value.Window.StartedAt.Before(value.Window.EndedAt) {
		return Observation{}, fmt.Errorf("%w: sample window must have ordered non-zero bounds", ErrInvalidObservation)
	}
	value.Window.StartedAt = value.Window.StartedAt.UTC()
	value.Window.EndedAt = value.Window.EndedAt.UTC()
	duration := value.Window.EndedAt.Sub(value.Window.StartedAt)
	if duration < minimumSampleWindow || duration > maxSampleWindow {
		return Observation{}, fmt.Errorf("%w: sample window duration must be between one second and 24 hours", ErrInvalidObservation)
	}
	if value.Window.SampleCount == 0 || value.Window.SampleCount > maxSamplesPerWindow {
		return Observation{}, fmt.Errorf("%w: sample_count must be in 1..%d", ErrInvalidObservation, maxSamplesPerWindow)
	}
	if value.Window.EndedAt.After(evaluatedAt) {
		return Observation{}, fmt.Errorf("%w: sample window ends in the future", ErrInvalidObservation)
	}
	if evaluatedAt.Sub(value.Window.EndedAt) > maxAge {
		return Observation{}, fmt.Errorf("%w: observation %q exceeds max_age_seconds", ErrStaleObservation, value.ID)
	}
	confidence, err := normalizeConfidence(value.Confidence)
	if err != nil {
		return Observation{}, fmt.Errorf("%w: %v", ErrInvalidObservation, err)
	}
	value.Confidence = confidence
	return value, nil
}

// AggregateBatch reduces each target/metric series without mutating the batch.
func AggregateBatch(batch Batch, evaluatedAt time.Time) ([]Aggregate, error) {
	normalized, err := NormalizeBatch(batch, evaluatedAt)
	if err != nil {
		return nil, err
	}
	return aggregateNormalized(normalized, evaluatedAt.UTC())
}

func aggregateNormalized(batch Batch, evaluatedAt time.Time) ([]Aggregate, error) {
	type accumulator struct {
		aggregate   Aggregate
		weightedSum float64
	}
	bySeries := make(map[string]*accumulator)
	for _, observation := range batch.Observations {
		key := observationSeriesKey(observation)
		item := bySeries[key]
		if item == nil {
			item = &accumulator{aggregate: Aggregate{
				Target: observation.Target, Metric: observation.Metric, Unit: observation.Unit,
				LatestObservationID: observation.ID, LatestValue: observation.Value,
				PeakValue: observation.Value, WindowStartedAt: observation.Window.StartedAt,
				WindowEndedAt: observation.Window.EndedAt, Confidence: observation.Confidence,
			}}
			bySeries[key] = item
		}
		if maxSamplesPerAggregate-item.aggregate.SampleCount < observation.Window.SampleCount {
			return nil, fmt.Errorf("%w: aggregate sample_count exceeds %d", ErrInvalidBatch, maxSamplesPerAggregate)
		}
		item.aggregate.SampleCount += observation.Window.SampleCount
		item.weightedSum += observation.Value * float64(observation.Window.SampleCount)
		if observation.Value > item.aggregate.PeakValue {
			item.aggregate.PeakValue = observation.Value
		}
		if observation.Window.StartedAt.Before(item.aggregate.WindowStartedAt) {
			item.aggregate.WindowStartedAt = observation.Window.StartedAt
		}
		if observation.Window.EndedAt.After(item.aggregate.WindowEndedAt) ||
			(observation.Window.EndedAt.Equal(item.aggregate.WindowEndedAt) && observation.ID < item.aggregate.LatestObservationID) {
			item.aggregate.WindowEndedAt = observation.Window.EndedAt
			item.aggregate.LatestObservationID = observation.ID
			item.aggregate.LatestValue = observation.Value
		}
		if observation.Confidence.Score < item.aggregate.Confidence.Score {
			item.aggregate.Confidence = observation.Confidence
		}
	}

	result := make([]Aggregate, 0, len(bySeries))
	for _, item := range bySeries {
		item.aggregate.WeightedAverage = item.weightedSum / float64(item.aggregate.SampleCount)
		item.aggregate.FreshnessAgeSeconds = ceilingSeconds(evaluatedAt.Sub(item.aggregate.WindowEndedAt))
		result = append(result, item.aggregate)
	}
	sort.Slice(result, func(i, j int) bool { return aggregateLess(result[i], result[j]) })
	return result, nil
}

// BuildRecommendationInput binds fresh aggregates to explicit safe and
// technical boundaries and deterministically selects the smallest safe reserve.
func BuildRecommendationInput(batch Batch, boundaries []Boundary, evaluatedAt time.Time) (RecommendationInput, error) {
	normalized, err := NormalizeBatch(batch, evaluatedAt)
	if err != nil {
		return RecommendationInput{}, err
	}
	aggregates, err := aggregateNormalized(normalized, evaluatedAt.UTC())
	if err != nil {
		return RecommendationInput{}, err
	}
	if len(boundaries) == 0 || len(boundaries) > maxBoundaries {
		return RecommendationInput{}, fmt.Errorf("%w: boundaries must contain 1..%d items", ErrInvalidInput, maxBoundaries)
	}
	aggregateBySeries := make(map[string]Aggregate, len(aggregates))
	for _, aggregate := range aggregates {
		aggregateBySeries[targetMetricKey(aggregate.Target, aggregate.Metric)] = aggregate
	}

	ids := make(map[string]struct{}, len(boundaries))
	series := make(map[string]struct{}, len(boundaries))
	var bottleneck BottleneckInput
	selected := false
	for _, candidate := range boundaries {
		boundary, err := normalizeBoundary(candidate)
		if err != nil {
			return RecommendationInput{}, err
		}
		idKey := strings.ToLower(boundary.ID)
		if _, duplicate := ids[idKey]; duplicate {
			return RecommendationInput{}, fmt.Errorf("%w: duplicate boundary id %q", ErrInvalidInput, boundary.ID)
		}
		ids[idKey] = struct{}{}
		seriesKey := targetMetricKey(boundary.Target, boundary.Metric)
		if _, duplicate := series[seriesKey]; duplicate {
			return RecommendationInput{}, fmt.Errorf("%w: duplicate target/metric boundary", ErrInvalidInput)
		}
		series[seriesKey] = struct{}{}
		aggregate, exists := aggregateBySeries[seriesKey]
		if !exists {
			return RecommendationInput{}, fmt.Errorf("%w: boundary %q has no fresh aggregate", ErrInvalidInput, boundary.ID)
		}
		candidateInput := bottleneckFor(boundary, aggregate)
		if !selected || candidateInput.SafeReservePercent < bottleneck.SafeReservePercent ||
			(candidateInput.SafeReservePercent == bottleneck.SafeReservePercent && candidateInput.BoundaryID < bottleneck.BoundaryID) {
			bottleneck = candidateInput
			selected = true
		}
	}
	if !selected {
		return RecommendationInput{}, fmt.Errorf("%w: no bottleneck candidate", ErrInvalidInput)
	}
	return RecommendationInput{
		SchemaVersion: RecommendationInputV1, EvaluatedAt: evaluatedAt.UTC(), Aggregates: aggregates,
		Bottleneck: bottleneck, AdvisoryOnly: true, ProductionMutation: false,
	}, nil
}

func normalizeBoundary(value Boundary) (Boundary, error) {
	if strings.TrimSpace(value.SchemaVersion) != BoundarySchemaV1 {
		return Boundary{}, fmt.Errorf("%w: boundary %q", ErrUnsupportedSchema, value.SchemaVersion)
	}
	value.SchemaVersion = BoundarySchemaV1
	value.ID = strings.TrimSpace(value.ID)
	if err := validateIdentifier("boundary.id", value.ID); err != nil {
		return Boundary{}, fmt.Errorf("%w: %v", ErrInvalidBoundary, err)
	}
	target, err := normalizeTarget(value.Target)
	if err != nil {
		return Boundary{}, fmt.Errorf("%w: %v", ErrInvalidBoundary, err)
	}
	value.Target = target
	value.Metric = Metric(strings.ToLower(strings.TrimSpace(string(value.Metric))))
	value.Unit = Unit(strings.ToLower(strings.TrimSpace(string(value.Unit))))
	definition, exists := metricDefinitions[value.Metric]
	if !exists || value.Unit != definition.Unit {
		return Boundary{}, fmt.Errorf("%w: metric %q does not use unit %q", ErrInvalidBoundary, value.Metric, value.Unit)
	}
	if !finite(value.SafeLimit) || !finite(value.TechnicalLimit) || value.SafeLimit <= 0 ||
		value.SafeLimit >= value.TechnicalLimit || value.TechnicalLimit > definition.Maximum {
		return Boundary{}, fmt.Errorf("%w: safe_limit must be positive and below a bounded technical_limit", ErrInvalidBoundary)
	}
	return value, nil
}

func bottleneckFor(boundary Boundary, aggregate Aggregate) BottleneckInput {
	value := aggregate.LatestValue
	return BottleneckInput{
		BoundaryID: boundary.ID, Target: boundary.Target, Metric: boundary.Metric, Unit: boundary.Unit,
		ObservationID: aggregate.LatestObservationID, ObservedValue: value, ObservedAt: aggregate.WindowEndedAt,
		SafeLimit: boundary.SafeLimit, TechnicalLimit: boundary.TechnicalLimit,
		SafeReserve:             boundary.SafeLimit - value,
		SafeReservePercent:      (boundary.SafeLimit - value) / boundary.SafeLimit * 100,
		TechnicalReserve:        boundary.TechnicalLimit - value,
		TechnicalReservePercent: (boundary.TechnicalLimit - value) / boundary.TechnicalLimit * 100,
		FreshnessAgeSeconds:     aggregate.FreshnessAgeSeconds, Confidence: aggregate.Confidence,
	}
}

func normalizeTarget(value Target) (Target, error) {
	value.Kind = TargetKind(strings.ToLower(strings.TrimSpace(string(value.Kind))))
	value.ID = strings.TrimSpace(value.ID)
	switch value.Kind {
	case TargetInterface, TargetZone, TargetSite:
	default:
		return Target{}, fmt.Errorf("unsupported target kind %q", value.Kind)
	}
	if err := validateIdentifier("target.id", value.ID); err != nil {
		return Target{}, err
	}
	return value, nil
}

func normalizeConfidence(value Confidence) (Confidence, error) {
	value.Level = ConfidenceLevel(strings.ToLower(strings.TrimSpace(string(value.Level))))
	if !finite(value.Score) || value.Score < 0 || value.Score > 1 {
		return Confidence{}, errors.New("confidence score must be in [0,1]")
	}
	valid := false
	switch value.Level {
	case ConfidenceLow:
		valid = value.Score < .5
	case ConfidenceMedium:
		valid = value.Score >= .5 && value.Score < .8
	case ConfidenceHigh:
		valid = value.Score >= .8
	}
	if !valid {
		return Confidence{}, fmt.Errorf("confidence level %q does not match score", value.Level)
	}
	return value, nil
}

func validateIdentifier(field, value string) error {
	if len(value) == 0 || len(value) > maxIdentifierLength {
		return fmt.Errorf("%s length must be in 1..%d", field, maxIdentifierLength)
	}
	if _, err := netip.ParseAddr(value); err == nil {
		return fmt.Errorf("%s must not contain a network address", field)
	}
	if address, err := net.ParseMAC(value); err == nil && len(address) > 0 {
		return fmt.Errorf("%s must not contain a network address", field)
	}
	for index, character := range value {
		allowed := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9'
		if index > 0 && index < len(value)-1 {
			allowed = allowed || character == '.' || character == '_' || character == ':' || character == '-'
		}
		if !allowed {
			return fmt.Errorf("%s must be a canonical opaque identifier", field)
		}
	}
	return nil
}

func observationLess(left, right Observation) bool {
	leftKey, rightKey := observationSeriesKey(left), observationSeriesKey(right)
	if leftKey != rightKey {
		return leftKey < rightKey
	}
	if !left.Window.StartedAt.Equal(right.Window.StartedAt) {
		return left.Window.StartedAt.Before(right.Window.StartedAt)
	}
	if !left.Window.EndedAt.Equal(right.Window.EndedAt) {
		return left.Window.EndedAt.Before(right.Window.EndedAt)
	}
	return left.ID < right.ID
}

func aggregateLess(left, right Aggregate) bool {
	leftKey, rightKey := targetMetricKey(left.Target, left.Metric), targetMetricKey(right.Target, right.Metric)
	return leftKey < rightKey
}

func observationSeriesKey(value Observation) string {
	return targetMetricKey(value.Target, value.Metric)
}

func targetMetricKey(target Target, metric Metric) string {
	return string(target.Kind) + "\x00" + strings.ToLower(target.ID) + "\x00" + string(metric)
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func ceilingSeconds(duration time.Duration) uint32 {
	if duration <= 0 {
		return 0
	}
	return uint32(math.Ceil(duration.Seconds()))
}
