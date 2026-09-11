package agent

import "time"

// HeartbeatState describes agent liveness.
type HeartbeatState string

const (
	HeartbeatOnline  HeartbeatState = "online"
	HeartbeatDelayed HeartbeatState = "delayed"
	HeartbeatOffline HeartbeatState = "offline"
)

// EvaluateHeartbeat classifies a node heartbeat using configurable thresholds.
func EvaluateHeartbeat(lastSeen, now time.Time, delayedAfter, offlineAfter time.Duration) HeartbeatState {
	if now.Before(lastSeen) {
		return HeartbeatOnline
	}
	age := now.Sub(lastSeen)
	if offlineAfter >= 0 && age >= offlineAfter {
		return HeartbeatOffline
	}
	if delayedAfter >= 0 && age >= delayedAfter {
		return HeartbeatDelayed
	}
	return HeartbeatOnline
}
