package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// jobReconnectETagMiddleware binds a successful durable Job read to the exact
// persisted Job version returned in the response. The ETag is deliberately a
// strong decimal tag so an operator can reload/reconnect and then use the exact
// reviewed version as the precondition for a later bounded mutation such as
// cancellation. Job responses remain non-cacheable because they may contain
// operational evidence.
func jobReconnectETagMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isJobReadRequest(r) {
			next.ServeHTTP(w, r)
			return
		}

		buffer := newBufferedResponse()
		next.ServeHTTP(buffer, r)
		status := buffer.status
		if status == 0 {
			status = http.StatusOK
		}
		if status == http.StatusOK {
			var response struct {
				Version uint64 `json:"version"`
			}
			if err := json.Unmarshal(buffer.body.Bytes(), &response); err == nil && response.Version > 0 {
				buffer.Header().Set("ETag", `"`+strconv.FormatUint(response.Version, 10)+`"`)
				buffer.Header().Set("Cache-Control", "no-store")
			}
		}
		buffer.flushTo(w)
	})
}

func isJobReadRequest(r *http.Request) bool {
	if r == nil || r.Method != http.MethodGet {
		return false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	return len(parts) == 4 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "jobs" && parts[3] != ""
}
