package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"control-center/internal/agent"
)

type heartbeatPayload struct {
	NodeID string    `json:"node_id"`
	SeenAt time.Time `json:"seen_at"`
}

// stateEnrollmentPayload intentionally preserves the bounded 0.3 state API
// shape. The v2 enrollment contract is normalized by EnrollmentHandler; this
// registry endpoint must not accept extended fields that it cannot persist.
type stateEnrollmentPayload struct {
	NodeID       string   `json:"node_id"`
	Hostname     string   `json:"hostname"`
	Capabilities []string `json:"capabilities,omitempty"`
}

func StateHandler(registry *agent.MemoryRegistry) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/agent/enrollments", func(w http.ResponseWriter, r *http.Request) {
		if registry == nil {
			http.Error(w, "agent registry unavailable", http.StatusServiceUnavailable)
			return
		}
		if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			http.Error(w, "application/json required", http.StatusUnsupportedMediaType)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxEnrollmentRequestBytes)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var input stateEnrollmentPayload
		if err := decoder.Decode(&input); err != nil || ensureAgentEOF(decoder) != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		state, err := registry.Enroll(agent.EnrollmentRequest{NodeID: input.NodeID, Hostname: input.Hostname, Capabilities: input.Capabilities})
		if err != nil {
			http.Error(w, "invalid enrollment", http.StatusUnprocessableEntity)
			return
		}
		writeAgentJSON(w, http.StatusOK, state)
	})
	mux.HandleFunc("POST /api/v1/agent/heartbeats", func(w http.ResponseWriter, r *http.Request) {
		if registry == nil {
			http.Error(w, "agent registry unavailable", http.StatusServiceUnavailable)
			return
		}
		if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			http.Error(w, "application/json required", http.StatusUnsupportedMediaType)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxEnrollmentRequestBytes)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var input heartbeatPayload
		if err := decoder.Decode(&input); err != nil || ensureAgentEOF(decoder) != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		state, err := registry.Heartbeat(input.NodeID, input.SeenAt)
		if errors.Is(err, agent.ErrUnknownNode) {
			http.Error(w, "node not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "invalid heartbeat", http.StatusUnprocessableEntity)
			return
		}
		writeAgentJSON(w, http.StatusOK, state)
	})
	mux.HandleFunc("GET /api/v1/agent/nodes", func(w http.ResponseWriter, _ *http.Request) {
		if registry == nil {
			http.Error(w, "agent registry unavailable", http.StatusServiceUnavailable)
			return
		}
		items := registry.List()
		writeAgentJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
	})
	mux.HandleFunc("GET /api/v1/agent/nodes/{nodeID}", func(w http.ResponseWriter, r *http.Request) {
		if registry == nil {
			http.Error(w, "agent registry unavailable", http.StatusServiceUnavailable)
			return
		}
		state, ok := registry.Get(r.PathValue("nodeID"))
		if !ok {
			http.Error(w, "node not found", http.StatusNotFound)
			return
		}
		writeAgentJSON(w, http.StatusOK, state)
	})
	return mux
}
