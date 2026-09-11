// Package loadtest provides a deterministic, network-free Agent load harness.
package loadtest

import (
	"errors"
	"fmt"
)

var ErrInvalidConfig = errors.New("invalid synthetic agent load config")

type Config struct {
	AgentCount   int `json:"agent_count"`
	Rounds       int `json:"rounds"`
	BatchSize    int `json:"batch_size"`
	RestartEvery int `json:"restart_every"`
	DropEvery    int `json:"drop_every"`
}

type CapacityBaseline struct {
	Agents                     int     `json:"agents"`
	Rounds                     int     `json:"rounds"`
	AcceptedHeartbeatsPerRound float64 `json:"accepted_heartbeats_per_round"`
	PeakBatch                  int     `json:"peak_batch"`
	Confidence                 string  `json:"confidence"`
	Synthetic                  bool    `json:"synthetic"`
}

type Report struct {
	OfferedHeartbeats    uint64           `json:"offered_heartbeats"`
	AcceptedHeartbeats   uint64           `json:"accepted_heartbeats"`
	DroppedHeartbeats    uint64           `json:"dropped_heartbeats"`
	AcceptedResults      uint64           `json:"accepted_results"`
	RejectedStaleResults uint64           `json:"rejected_stale_results"`
	ControllerRestarts   uint64           `json:"controller_restarts"`
	FalseSuccesses       uint64           `json:"false_successes"`
	Baseline             CapacityBaseline `json:"baseline"`
	SideEffectFree       bool             `json:"side_effect_free"`
	ProductionTraffic    bool             `json:"production_traffic"`
}

// Run executes logical ticks only: it opens no sockets, sleeps for no wall time,
// and mutates no external state. After a controller restart, results carrying the
// previous epoch are rejected, then agents rebind for the next round.
func Run(config Config) (Report, error) {
	if config.AgentCount < 1 || config.AgentCount > 100_000 || config.Rounds < 1 || config.Rounds > 10_000 || config.BatchSize < 1 || config.BatchSize > config.AgentCount || config.RestartEvery < 0 || config.DropEvery < 0 {
		return Report{}, fmt.Errorf("%w: values outside supported bounds", ErrInvalidConfig)
	}
	controllerEpoch := uint64(1)
	agentEpoch := make([]uint64, config.AgentCount)
	for index := range agentEpoch {
		agentEpoch[index] = controllerEpoch
	}
	report := Report{SideEffectFree: true, ProductionTraffic: false}
	ordinal := uint64(0)
	for round := 1; round <= config.Rounds; round++ {
		if config.RestartEvery > 0 && round > 1 && (round-1)%config.RestartEvery == 0 {
			controllerEpoch++
			report.ControllerRestarts++
		}
		for start := 0; start < config.AgentCount; start += config.BatchSize {
			end := start + config.BatchSize
			if end > config.AgentCount {
				end = config.AgentCount
			}
			for agentID := start; agentID < end; agentID++ {
				ordinal++
				report.OfferedHeartbeats++
				if config.DropEvery > 0 && ordinal%uint64(config.DropEvery) == 0 {
					report.DroppedHeartbeats++
					continue
				}
				report.AcceptedHeartbeats++
				if agentEpoch[agentID] != controllerEpoch {
					report.RejectedStaleResults++
					agentEpoch[agentID] = controllerEpoch
					continue
				}
				report.AcceptedResults++
			}
		}
	}
	confidence := "medium"
	samples := report.AcceptedHeartbeats
	if samples >= 10_000 {
		confidence = "high"
	}
	if samples >= 100_000 {
		confidence = "certified"
	}
	report.Baseline = CapacityBaseline{Agents: config.AgentCount, Rounds: config.Rounds, AcceptedHeartbeatsPerRound: float64(report.AcceptedHeartbeats) / float64(config.Rounds), PeakBatch: config.BatchSize, Confidence: confidence, Synthetic: true}
	return report, nil
}
