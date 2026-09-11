package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"control-center/internal/agent"
)

const maxHealthRequestBytes = 64 << 10

type heartbeatRequest struct {
	LastSeen            time.Time `json:"last_seen"`
	Now                 time.Time `json:"now"`
	DelayedAfterSeconds int64     `json:"delayed_after_seconds"`
	OfflineAfterSeconds int64     `json:"offline_after_seconds"`
}

type leaseRequest struct {
	NodeID        string    `json:"node_id"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	Now           time.Time `json:"now"`
	TTLSeconds    int64     `json:"ttl_seconds"`
	GraceSeconds  int64     `json:"grace_seconds"`
}

func HeartbeatHandler() http.Handler {
	return healthJSONPost(func(w http.ResponseWriter, decoder *json.Decoder) {
		var input heartbeatRequest
		if err := decoder.Decode(&input); err != nil || ensureAgentEOF(decoder) != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if input.LastSeen.IsZero() || input.Now.IsZero() || input.DelayedAfterSeconds < 0 || input.OfflineAfterSeconds < input.DelayedAfterSeconds {
			http.Error(w, "invalid heartbeat policy", http.StatusUnprocessableEntity)
			return
		}
		state := agent.EvaluateHeartbeat(
			input.LastSeen, input.Now,
			time.Duration(input.DelayedAfterSeconds)*time.Second,
			time.Duration(input.OfflineAfterSeconds)*time.Second,
		)
		writeAgentJSON(w, http.StatusOK, map[string]any{"state": state})
	})
}

func LeaseHandler() http.Handler {
	return healthJSONPost(func(w http.ResponseWriter, decoder *json.Decoder) {
		var input leaseRequest
		if err := decoder.Decode(&input); err != nil || ensureAgentEOF(decoder) != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		state, err := agent.EvaluateNodeLease(
			agent.NodeLease{NodeID: input.NodeID, LastHeartbeat: input.LastHeartbeat},
			agent.LeasePolicy{TTL: time.Duration(input.TTLSeconds) * time.Second, Grace: time.Duration(input.GraceSeconds) * time.Second},
			input.Now,
		)
		if err != nil {
			http.Error(w, "invalid lease", http.StatusUnprocessableEntity)
			return
		}
		writeAgentJSON(w, http.StatusOK, map[string]any{"state": state})
	})
}

func healthJSONPost(handler func(http.ResponseWriter, *json.Decoder)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			http.Error(w, "application/json required", http.StatusUnsupportedMediaType)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxHealthRequestBytes)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		handler(w, decoder)
	})
}

func ensureAgentEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("trailing JSON value")
	}
	return err
}

func writeAgentJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
