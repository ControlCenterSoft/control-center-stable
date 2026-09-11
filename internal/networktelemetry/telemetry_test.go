package networktelemetry

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

var telemetryNow = time.Date(2026, 9, 9, 9, 0, 0, 0, time.UTC)

func observation(id string, target Target, metric Metric, value float64, started, ended time.Time, samples uint64, confidence Confidence) Observation {
	definition := metricDefinitions[metric]
	return Observation{
		SchemaVersion: ObservationSchemaV1,
		ID:            id,
		Target:        target,
		Metric:        metric,
		Value:         value,
		Unit:          definition.Unit,
		Window:        SampleWindow{StartedAt: started, EndedAt: ended, SampleCount: samples},
		Confidence:    confidence,
	}
}

func validBatch() Batch {
	return Batch{
		SchemaVersion: BatchSchemaV1,
		MaxAgeSeconds: 600,
		Observations: []Observation{
			observation(
				"rx-new", Target{Kind: TargetInterface, ID: "nic-001"}, MetricReceiveUtilization, 70,
				telemetryNow.Add(-2*time.Minute), telemetryNow.Add(-time.Minute), 30,
				Confidence{Level: ConfidenceHigh, Score: .9},
			),
			observation(
				"rx-old", Target{Kind: TargetInterface, ID: "nic-001"}, MetricReceiveUtilization, 50,
				telemetryNow.Add(-4*time.Minute), telemetryNow.Add(-3*time.Minute), 10,
				Confidence{Level: ConfidenceMedium, Score: .7},
			),
			observation(
				"site-loss", Target{Kind: TargetSite, ID: "site-a"}, MetricPacketLoss, 1.5,
				telemetryNow.Add(-90*time.Second), telemetryNow.Add(-30*time.Second), 60,
				Confidence{Level: ConfidenceHigh, Score: .95},
			),
		},
	}
}

func boundary(id string, target Target, metric Metric, safe, technical float64) Boundary {
	return Boundary{
		SchemaVersion: BoundarySchemaV1, ID: id, Target: target, Metric: metric,
		Unit: metricDefinitions[metric].Unit, SafeLimit: safe, TechnicalLimit: technical,
	}
}

func TestNormalizeBatchCanonicalizesSortsAndDoesNotMutateInput(t *testing.T) {
	batch := validBatch()
	batch.SchemaVersion = " network.telemetry.batch/v1 "
	batch.Observations[0].SchemaVersion = " network.telemetry.observation/v1 "
	batch.Observations[0].Target.Kind = " INTERFACE "
	batch.Observations[0].Metric = " NETWORK.UTILIZATION.RECEIVE "
	batch.Observations[0].Unit = " PERCENT "
	batch.Observations[0].Confidence.Level = " HIGH "
	batch.Observations[0].Window.StartedAt = batch.Observations[0].Window.StartedAt.In(time.FixedZone("offset", 2*60*60))
	before, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}

	got, err := NormalizeBatch(batch, telemetryNow)
	if err != nil {
		t.Fatalf("NormalizeBatch() error = %v", err)
	}
	after, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatal("NormalizeBatch mutated caller input")
	}
	if got.SchemaVersion != BatchSchemaV1 || got.Observations[0].ID != "rx-old" || got.Observations[1].ID != "rx-new" || got.Observations[2].ID != "site-loss" {
		t.Fatalf("non-deterministic normalized batch: %#v", got)
	}
	if got.Observations[1].Target.Kind != TargetInterface || got.Observations[1].Metric != MetricReceiveUtilization || got.Observations[1].Unit != UnitPercent {
		t.Fatalf("observation was not canonicalized: %#v", got.Observations[1])
	}
	if got.Observations[1].Window.StartedAt.Location() != time.UTC {
		t.Fatalf("sample window is not UTC: %v", got.Observations[1].Window.StartedAt.Location())
	}
	got.Observations[0].ID = "changed"
	if batch.Observations[1].ID == "changed" {
		t.Fatal("normalized observations alias caller slice")
	}
}

func TestEveryMetricAcceptsEveryLogicalScopeWithCanonicalUnit(t *testing.T) {
	metrics := []Metric{
		MetricReceiveThroughput, MetricTransmitThroughput,
		MetricReceiveUtilization, MetricTransmitUtilization,
		MetricReceiveErrors, MetricTransmitErrors,
		MetricLatency, MetricPacketLoss,
	}
	targets := []Target{
		{Kind: TargetInterface, ID: "nic-001"},
		{Kind: TargetZone, ID: "zone-lan"},
		{Kind: TargetSite, ID: "site-a"},
	}
	for _, metric := range metrics {
		for _, target := range targets {
			name := string(metric) + "/" + string(target.Kind)
			t.Run(name, func(t *testing.T) {
				value := observation("sample", target, metric, 1, telemetryNow.Add(-time.Minute), telemetryNow, 1, Confidence{Level: ConfidenceLow, Score: .2})
				got, err := NormalizeBatch(Batch{SchemaVersion: BatchSchemaV1, MaxAgeSeconds: 60, Observations: []Observation{value}}, telemetryNow)
				if err != nil {
					t.Fatal(err)
				}
				if got.Observations[0].Unit != metricDefinitions[metric].Unit {
					t.Fatalf("unit = %q", got.Observations[0].Unit)
				}
			})
		}
	}
}

func TestNormalizeBatchRejectsInvalidEnvelope(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Batch)
		at     time.Time
		want   error
	}{
		{name: "missing schema", mutate: func(value *Batch) { value.SchemaVersion = "" }, at: telemetryNow, want: ErrUnsupportedSchema},
		{name: "future schema", mutate: func(value *Batch) { value.SchemaVersion = "network.telemetry.batch/v2" }, at: telemetryNow, want: ErrUnsupportedSchema},
		{name: "zero clock", mutate: func(*Batch) {}, at: time.Time{}, want: ErrInvalidBatch},
		{name: "zero freshness", mutate: func(value *Batch) { value.MaxAgeSeconds = 0 }, at: telemetryNow, want: ErrInvalidBatch},
		{name: "excessive freshness", mutate: func(value *Batch) { value.MaxAgeSeconds = maxFreshnessSeconds + 1 }, at: telemetryNow, want: ErrInvalidBatch},
		{name: "empty observations", mutate: func(value *Batch) { value.Observations = nil }, at: telemetryNow, want: ErrInvalidBatch},
		{name: "too many observations", mutate: func(value *Batch) { value.Observations = make([]Observation, maxObservations+1) }, at: telemetryNow, want: ErrInvalidBatch},
		{name: "duplicate id case insensitive", mutate: func(value *Batch) { value.Observations[1].ID = "RX-NEW" }, at: telemetryNow, want: ErrInvalidBatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			batch := validBatch()
			test.mutate(&batch)
			_, err := NormalizeBatch(batch, test.at)
			if !errors.Is(err, test.want) {
				t.Fatalf("NormalizeBatch() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestNormalizeBatchRejectsUnsafeObservationFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Observation)
		want   error
	}{
		{name: "missing schema", mutate: func(value *Observation) { value.SchemaVersion = "" }, want: ErrUnsupportedSchema},
		{name: "bad id", mutate: func(value *Observation) { value.ID = "bad/id" }, want: ErrInvalidObservation},
		{name: "bad target kind", mutate: func(value *Observation) { value.Target.Kind = "subnet" }, want: ErrInvalidObservation},
		{name: "bad target id", mutate: func(value *Observation) { value.Target.ID = "nic/001" }, want: ErrInvalidObservation},
		{name: "ipv4 target id", mutate: func(value *Observation) { value.Target.ID = "192.0.2.10" }, want: ErrInvalidObservation},
		{name: "ipv6 target id", mutate: func(value *Observation) { value.Target.ID = "2001:db8::10" }, want: ErrInvalidObservation},
		{name: "mac target id", mutate: func(value *Observation) { value.Target.ID = "02:00:00:00:00:01" }, want: ErrInvalidObservation},
		{name: "unknown metric", mutate: func(value *Observation) { value.Metric = "network.jitter" }, want: ErrInvalidObservation},
		{name: "wrong unit", mutate: func(value *Observation) { value.Unit = UnitMilliseconds }, want: ErrInvalidObservation},
		{name: "negative", mutate: func(value *Observation) { value.Value = -1 }, want: ErrInvalidObservation},
		{name: "nan", mutate: func(value *Observation) { value.Value = math.NaN() }, want: ErrInvalidObservation},
		{name: "infinite", mutate: func(value *Observation) { value.Value = math.Inf(1) }, want: ErrInvalidObservation},
		{name: "over maximum", mutate: func(value *Observation) { value.Value = 101 }, want: ErrInvalidObservation},
		{name: "zero samples", mutate: func(value *Observation) { value.Window.SampleCount = 0 }, want: ErrInvalidObservation},
		{name: "too many samples", mutate: func(value *Observation) { value.Window.SampleCount = maxSamplesPerWindow + 1 }, want: ErrInvalidObservation},
		{name: "zero window", mutate: func(value *Observation) { value.Window.StartedAt = time.Time{} }, want: ErrInvalidObservation},
		{name: "reversed window", mutate: func(value *Observation) { value.Window.StartedAt = value.Window.EndedAt }, want: ErrInvalidObservation},
		{name: "short window", mutate: func(value *Observation) { value.Window.StartedAt = value.Window.EndedAt.Add(-time.Millisecond) }, want: ErrInvalidObservation},
		{name: "long window", mutate: func(value *Observation) {
			value.Window.StartedAt = value.Window.EndedAt.Add(-maxSampleWindow - time.Second)
		}, want: ErrInvalidObservation},
		{name: "future window", mutate: func(value *Observation) { value.Window.EndedAt = telemetryNow.Add(time.Nanosecond) }, want: ErrInvalidObservation},
		{name: "stale", mutate: func(value *Observation) {
			value.Window.EndedAt = telemetryNow.Add(-601 * time.Second)
			value.Window.StartedAt = value.Window.EndedAt.Add(-time.Minute)
		}, want: ErrStaleObservation},
		{name: "unknown confidence", mutate: func(value *Observation) { value.Confidence.Level = "certain" }, want: ErrInvalidObservation},
		{name: "low score mismatch", mutate: func(value *Observation) { value.Confidence = Confidence{Level: ConfidenceLow, Score: .5} }, want: ErrInvalidObservation},
		{name: "medium score mismatch", mutate: func(value *Observation) { value.Confidence = Confidence{Level: ConfidenceMedium, Score: .8} }, want: ErrInvalidObservation},
		{name: "high score mismatch", mutate: func(value *Observation) { value.Confidence = Confidence{Level: ConfidenceHigh, Score: .79} }, want: ErrInvalidObservation},
		{name: "confidence nan", mutate: func(value *Observation) { value.Confidence.Score = math.NaN() }, want: ErrInvalidObservation},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			batch := validBatch()
			batch.Observations = batch.Observations[:1]
			test.mutate(&batch.Observations[0])
			_, err := NormalizeBatch(batch, telemetryNow)
			if !errors.Is(err, test.want) {
				t.Fatalf("NormalizeBatch() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestMetricSpecificUpperBoundsFailClosed(t *testing.T) {
	tests := []struct {
		metric Metric
		value  float64
	}{
		{MetricReceiveThroughput, maximumUnboundedMetric + 1},
		{MetricTransmitThroughput, maximumUnboundedMetric + 1},
		{MetricReceiveUtilization, 100.0001},
		{MetricTransmitUtilization, 100.0001},
		{MetricReceiveErrors, maximumErrorsPerSecond + 1},
		{MetricTransmitErrors, maximumErrorsPerSecond + 1},
		{MetricLatency, maximumLatencyMillis + 1},
		{MetricPacketLoss, 100.0001},
	}
	for _, test := range tests {
		t.Run(string(test.metric), func(t *testing.T) {
			item := observation("sample", Target{Kind: TargetZone, ID: "zone-a"}, test.metric, test.value, telemetryNow.Add(-time.Minute), telemetryNow, 1, Confidence{Level: ConfidenceLow, Score: 0})
			_, err := NormalizeBatch(Batch{SchemaVersion: BatchSchemaV1, MaxAgeSeconds: 60, Observations: []Observation{item}}, telemetryNow)
			if !errors.Is(err, ErrInvalidObservation) {
				t.Fatalf("NormalizeBatch() error = %v", err)
			}
		})
	}
}

func TestFreshnessBoundaryAndOverlappingWindows(t *testing.T) {
	batch := validBatch()
	batch.Observations = batch.Observations[:1]
	batch.Observations[0].Window.EndedAt = telemetryNow.Add(-600 * time.Second)
	batch.Observations[0].Window.StartedAt = batch.Observations[0].Window.EndedAt.Add(-time.Minute)
	if _, err := NormalizeBatch(batch, telemetryNow); err != nil {
		t.Fatalf("exact freshness boundary rejected: %v", err)
	}

	overlap := validBatch()
	overlap.Observations = overlap.Observations[:2]
	overlap.Observations[1].Window.StartedAt = overlap.Observations[0].Window.StartedAt.Add(30 * time.Second)
	overlap.Observations[1].Window.EndedAt = overlap.Observations[0].Window.EndedAt.Add(30 * time.Second)
	_, err := NormalizeBatch(overlap, telemetryNow)
	if !errors.Is(err, ErrInvalidBatch) || !strings.Contains(err.Error(), "overlapping") {
		t.Fatalf("overlap error = %v", err)
	}
}

func TestAggregateBatchIsDeterministicAndConservative(t *testing.T) {
	batch := validBatch()
	before := deepCopyBatch(t, batch)
	got, err := AggregateBatch(batch, telemetryNow.Add(500*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(batch, before) {
		t.Fatal("AggregateBatch mutated caller input")
	}
	if len(got) != 2 {
		t.Fatalf("aggregates = %#v", got)
	}
	rx := got[0]
	if rx.Target.Kind != TargetInterface || rx.Metric != MetricReceiveUtilization || rx.LatestObservationID != "rx-new" || rx.LatestValue != 70 {
		t.Fatalf("unexpected interface aggregate: %#v", rx)
	}
	if rx.WeightedAverage != 65 || rx.PeakValue != 70 || rx.SampleCount != 40 {
		t.Fatalf("unexpected reduction: %#v", rx)
	}
	if rx.Confidence != (Confidence{Level: ConfidenceMedium, Score: .7}) {
		t.Fatalf("confidence is not conservative: %#v", rx.Confidence)
	}
	if rx.FreshnessAgeSeconds != 61 {
		t.Fatalf("freshness age = %d, want ceiling 61", rx.FreshnessAgeSeconds)
	}

	reversed := deepCopyBatch(t, batch)
	for left, right := 0, len(reversed.Observations)-1; left < right; left, right = left+1, right-1 {
		reversed.Observations[left], reversed.Observations[right] = reversed.Observations[right], reversed.Observations[left]
	}
	again, err := AggregateBatch(reversed, telemetryNow.Add(500*time.Millisecond))
	if err != nil || !reflect.DeepEqual(got, again) {
		t.Fatalf("aggregation depends on input order: got=%#v again=%#v err=%v", got, again, err)
	}
}

func TestAggregateLatestTimestampTieUsesLexicalObservationID(t *testing.T) {
	first := observation("z-last", Target{Kind: TargetZone, ID: "zone-a"}, MetricLatency, 20, telemetryNow.Add(-time.Minute), telemetryNow, 1, Confidence{Level: ConfidenceHigh, Score: .9})
	second := observation("a-last", first.Target, first.Metric, 10, first.Window.StartedAt, first.Window.EndedAt, 1, first.Confidence)
	// Equal windows would be duplicates for aggregation safety, so use adjacent
	// windows ending at the same instant only through the normalized private
	// reduction to exercise the stable latest-value tie rule.
	second.Window.StartedAt = first.Window.StartedAt.Add(30 * time.Second)
	normalized := Batch{SchemaVersion: BatchSchemaV1, MaxAgeSeconds: 60, Observations: []Observation{first, second}}
	got, err := aggregateNormalized(normalized, telemetryNow)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].LatestObservationID != "a-last" || got[0].LatestValue != 10 {
		t.Fatalf("latest tie was not lexical: %#v", got[0])
	}
}

func TestBuildRecommendationInputSelectsDeterministicBottleneck(t *testing.T) {
	batch := validBatch()
	boundaries := []Boundary{
		boundary("z-interface", Target{Kind: TargetInterface, ID: "nic-001"}, MetricReceiveUtilization, 80, 100),
		boundary("a-site", Target{Kind: TargetSite, ID: "site-a"}, MetricPacketLoss, 2, 5),
	}
	beforeBatch := deepCopyBatch(t, batch)
	beforeBoundaries := append([]Boundary(nil), boundaries...)
	got, err := BuildRecommendationInput(batch, boundaries, telemetryNow)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(batch, beforeBatch) || !reflect.DeepEqual(boundaries, beforeBoundaries) {
		t.Fatal("BuildRecommendationInput mutated caller input")
	}
	if got.SchemaVersion != RecommendationInputV1 || !got.AdvisoryOnly || got.ProductionMutation {
		t.Fatalf("unsafe recommendation input envelope: %#v", got)
	}
	if got.Bottleneck.BoundaryID != "z-interface" || got.Bottleneck.ObservationID != "rx-new" || got.Bottleneck.SafeReserve != 10 || got.Bottleneck.SafeReservePercent != 12.5 {
		t.Fatalf("unexpected bottleneck: %#v", got.Bottleneck)
	}
	if got.Bottleneck.TechnicalReserve != 30 || got.Bottleneck.TechnicalReservePercent != 30 {
		t.Fatalf("unexpected technical reserve: %#v", got.Bottleneck)
	}

	// Equal pressure resolves by boundary ID, independent of input order.
	tieBatch := validBatch()
	tieBatch.Observations[2].Value = 70
	tied := append([]Boundary(nil), boundaries...)
	tied[1].SafeLimit = 80
	tied[1].TechnicalLimit = 100
	tiedInput, err := BuildRecommendationInput(tieBatch, []Boundary{tied[1], tied[0]}, telemetryNow)
	if err != nil {
		t.Fatal(err)
	}
	if tiedInput.Bottleneck.BoundaryID != "a-site" {
		t.Fatalf("bottleneck tie is not lexical: %#v", tiedInput.Bottleneck)
	}
}

func TestBuildRecommendationInputRejectsBoundaryFailures(t *testing.T) {
	tests := []struct {
		name       string
		boundaries func() []Boundary
		want       error
	}{
		{name: "none", boundaries: func() []Boundary { return nil }, want: ErrInvalidInput},
		{name: "too many", boundaries: func() []Boundary { return make([]Boundary, maxBoundaries+1) }, want: ErrInvalidInput},
		{name: "missing schema", boundaries: func() []Boundary {
			value := boundary("b", Target{Kind: TargetSite, ID: "site-a"}, MetricPacketLoss, 2, 5)
			value.SchemaVersion = ""
			return []Boundary{value}
		}, want: ErrUnsupportedSchema},
		{name: "unknown target", boundaries: func() []Boundary {
			return []Boundary{boundary("b", Target{Kind: "subnet", ID: "subnet-a"}, MetricPacketLoss, 2, 5)}
		}, want: ErrInvalidBoundary},
		{name: "wrong unit", boundaries: func() []Boundary {
			value := boundary("b", Target{Kind: TargetSite, ID: "site-a"}, MetricPacketLoss, 2, 5)
			value.Unit = UnitMilliseconds
			return []Boundary{value}
		}, want: ErrInvalidBoundary},
		{name: "zero safe", boundaries: func() []Boundary {
			return []Boundary{boundary("b", Target{Kind: TargetSite, ID: "site-a"}, MetricPacketLoss, 0, 5)}
		}, want: ErrInvalidBoundary},
		{name: "safe above technical", boundaries: func() []Boundary {
			return []Boundary{boundary("b", Target{Kind: TargetSite, ID: "site-a"}, MetricPacketLoss, 5, 5)}
		}, want: ErrInvalidBoundary},
		{name: "technical above metric maximum", boundaries: func() []Boundary {
			return []Boundary{boundary("b", Target{Kind: TargetSite, ID: "site-a"}, MetricPacketLoss, 90, 101)}
		}, want: ErrInvalidBoundary},
		{name: "missing aggregate", boundaries: func() []Boundary {
			return []Boundary{boundary("b", Target{Kind: TargetZone, ID: "zone-missing"}, MetricPacketLoss, 2, 5)}
		}, want: ErrInvalidInput},
		{name: "duplicate id", boundaries: func() []Boundary {
			first := boundary("b", Target{Kind: TargetSite, ID: "site-a"}, MetricPacketLoss, 2, 5)
			second := boundary("B", Target{Kind: TargetInterface, ID: "nic-001"}, MetricReceiveUtilization, 80, 100)
			return []Boundary{first, second}
		}, want: ErrInvalidInput},
		{name: "duplicate series", boundaries: func() []Boundary {
			first := boundary("a", Target{Kind: TargetSite, ID: "site-a"}, MetricPacketLoss, 2, 5)
			second := boundary("b", first.Target, first.Metric, 3, 6)
			return []Boundary{first, second}
		}, want: ErrInvalidInput},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := BuildRecommendationInput(validBatch(), test.boundaries(), telemetryNow)
			if !errors.Is(err, test.want) {
				t.Fatalf("BuildRecommendationInput() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestDecodeBatchJSONRejectsSensitiveTopologyAndMutationFields(t *testing.T) {
	valid, err := json.Marshal(validBatch())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeAndNormalizeBatchJSON(strings.NewReader(string(valid)), telemetryNow); err != nil {
		t.Fatalf("valid batch rejected: %v", err)
	}

	for _, field := range []string{"address", "credential", "provider", "endpoint", "route", "gateway", "topology", "desired_state", "command"} {
		t.Run(field, func(t *testing.T) {
			payload := strings.Replace(string(valid), `"max_age_seconds":600`, `"max_age_seconds":600,"`+field+`":"forbidden"`, 1)
			_, err := DecodeAndNormalizeBatchJSON(strings.NewReader(payload), telemetryNow)
			if !errors.Is(err, ErrInvalidBatch) || !strings.Contains(err.Error(), "unknown field") {
				t.Fatalf("unsafe field %q error = %v", field, err)
			}
		})
	}
	if _, err := DecodeBatchJSON(strings.NewReader(string(valid) + `{}`)); !errors.Is(err, ErrInvalidBatch) {
		t.Fatalf("trailing document error = %v", err)
	}
	if _, err := DecodeBatchJSON(nil); !errors.Is(err, ErrInvalidBatch) {
		t.Fatalf("nil reader error = %v", err)
	}
	tooLarge := `{"schema_version":"network.telemetry.batch/v1","padding":"` + strings.Repeat("x", maxEncodedBatchBytes) + `"}`
	if _, err := DecodeBatchJSON(strings.NewReader(tooLarge)); !errors.Is(err, ErrInvalidBatch) {
		t.Fatalf("oversize error = %v", err)
	}
	stale := validBatch()
	stale.Observations[0].Window.EndedAt = telemetryNow.Add(-time.Hour)
	stale.Observations[0].Window.StartedAt = stale.Observations[0].Window.EndedAt.Add(-time.Minute)
	staleJSON, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeAndNormalizeBatchJSON(strings.NewReader(string(staleJSON)), telemetryNow); !errors.Is(err, ErrStaleObservation) {
		t.Fatalf("stale untrusted payload error = %v", err)
	}
}

func deepCopyBatch(t *testing.T, value Batch) Batch {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result Batch
	if err := json.Unmarshal(payload, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
