package httpapi

import (
	"encoding/json"
	"io"
	"net/http"

	"control-center/internal/automation"
)

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func New() http.Handler {
	return http.HandlerFunc(planAutomation)
}

func planAutomation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method is not allowed")
		return
	}
	var request automation.Request
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid automation request")
		return
	}
	plan, err := automation.BuildPlan(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_AUTOMATION_REQUEST", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return err
	}
	return nil
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	body := errorBody{}
	body.Error.Code = code
	body.Error.Message = message
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
