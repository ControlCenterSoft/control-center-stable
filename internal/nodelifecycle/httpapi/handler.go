package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"control-center/internal/corecontracts"
	"control-center/internal/nodelifecycle"
)

const maxTransitionPlanRequestBytes int64 = 64 << 10

var (
	errInvalidJSON  = errors.New("invalid JSON request")
	errBodyTooLarge = errors.New("request body too large")
)

type Option func(*server)

// WithClock supplies a deterministic evaluation clock for embedding/tests.
func WithClock(clock func() time.Time) Option {
	return func(s *server) {
		if clock != nil {
			s.now = clock
		}
	}
}

type server struct {
	projection nodelifecycle.Projection
	now        func() time.Time
}

type transitionPlanPayload struct {
	To           *nodelifecycle.State              `json:"to"`
	Type         *nodelifecycle.TransitionType     `json:"type"`
	Reason       *string                           `json:"reason,omitempty"`
	Precondition *corecontracts.ObjectPrecondition `json:"precondition"`
	Evidence     *nodelifecycle.TransitionEvidence `json:"evidence"`
}

type errorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// New exposes a read-only projection and transition planning. It has no route
// that writes lifecycle state or mutates a managed host.
func New(projection nodelifecycle.Projection, options ...Option) http.Handler {
	s := &server{projection: projection, now: time.Now}
	for _, option := range options {
		if option != nil {
			option(s)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/nodes/{nodeID}/lifecycle", s.handleGet)
	mux.HandleFunc("/api/v1/nodes/{nodeID}/lifecycle/transitions/plan", s.handlePlan)
	return mux
}

func (s *server) handleGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method is not allowed")
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_query", "Query parameters are not supported")
		return
	}
	lifecycle, err := s.readProjection(r, r.PathValue("nodeID"))
	if err != nil {
		writeProjectionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, lifecycle)
}

func (s *server) handlePlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method is not allowed")
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_query", "Query parameters are not supported")
		return
	}
	if !hasJSONMediaType(r) {
		writeError(w, http.StatusUnsupportedMediaType, "json_required", "Content-Type application/json is required")
		return
	}
	var payload transitionPlanPayload
	if err := decodeStrictJSON(w, r, &payload); err != nil {
		if errors.Is(err, errBodyTooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Request body is too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request", "Request body must be one strict JSON object")
		return
	}
	request, err := payload.contractRequest()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Required transition fields are missing")
		return
	}
	current, err := s.readProjection(r, r.PathValue("nodeID"))
	if err != nil {
		writeProjectionError(w, err)
		return
	}
	plan, err := nodelifecycle.BuildTransitionPlan(current, request, s.now().UTC())
	if err != nil {
		writePlanError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (p transitionPlanPayload) contractRequest() (nodelifecycle.TransitionRequest, error) {
	if p.To == nil || p.Type == nil || p.Precondition == nil || p.Evidence == nil {
		return nodelifecycle.TransitionRequest{}, errInvalidJSON
	}
	reason := ""
	if p.Reason != nil {
		reason = *p.Reason
	}
	return nodelifecycle.TransitionRequest{
		To:           *p.To,
		Type:         *p.Type,
		Reason:       reason,
		Precondition: *p.Precondition,
		Evidence: nodelifecycle.TransitionEvidence{
			PassedChecks: append([]nodelifecycle.EvidenceCheck(nil), p.Evidence.PassedChecks...),
		},
	}, nil
}

func (s *server) readProjection(r *http.Request, nodeID string) (nodelifecycle.NodeLifecycle, error) {
	if s.projection == nil {
		return nodelifecycle.NodeLifecycle{}, nodelifecycle.ErrProjectionUnavailable
	}
	lifecycle, err := s.projection.Get(r.Context(), nodeID)
	if err != nil {
		return nodelifecycle.NodeLifecycle{}, err
	}
	if lifecycle.ObjectID != nodeID {
		return nodelifecycle.NodeLifecycle{}, fmt.Errorf("%w: projected object does not match requested node", nodelifecycle.ErrProjectionUnavailable)
	}
	if err := nodelifecycle.Validate(lifecycle); err != nil {
		return nodelifecycle.NodeLifecycle{}, fmt.Errorf("%w: invalid projected object", nodelifecycle.ErrProjectionUnavailable)
	}
	return lifecycle, nil
}

func writeProjectionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, nodelifecycle.ErrProjectionNotFound):
		writeError(w, http.StatusNotFound, "node_lifecycle_not_found", "Node lifecycle was not found")
	default:
		writeError(w, http.StatusServiceUnavailable, "lifecycle_projection_unavailable", "Lifecycle projection is unavailable")
	}
}

func writePlanError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, corecontracts.ErrPreconditionFailed):
		writeError(w, http.StatusConflict, "precondition_failed", "Lifecycle projection changed; read it again")
	case errors.Is(err, corecontracts.ErrInvalidTransition):
		writeError(w, http.StatusConflict, "invalid_transition", "Lifecycle transition is not allowed")
	case errors.Is(err, corecontracts.ErrPreconditionRequired), errors.Is(err, corecontracts.ErrInvalidPrecondition):
		writeError(w, http.StatusUnprocessableEntity, "invalid_precondition", "A valid lifecycle precondition is required")
	case errors.Is(err, nodelifecycle.ErrInvalidEvidence):
		writeError(w, http.StatusUnprocessableEntity, "invalid_transition_evidence", "Required transition evidence did not pass")
	case errors.Is(err, nodelifecycle.ErrInvalidLifecycle):
		writeError(w, http.StatusUnprocessableEntity, "invalid_node_lifecycle", "Lifecycle successor is invalid")
	case errors.Is(err, nodelifecycle.ErrInvalidPlan):
		writeError(w, http.StatusServiceUnavailable, "lifecycle_projection_unavailable", "Lifecycle plan cannot be evaluated safely")
	default:
		writeError(w, http.StatusInternalServerError, "lifecycle_plan_failed", "Lifecycle plan could not be evaluated")
	}
}

func hasJSONMediaType(r *http.Request) bool {
	values := r.Header.Values("Content-Type")
	if len(values) != 1 {
		return false
	}
	mediaType, parameters, err := mime.ParseMediaType(values[0])
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return false
	}
	for name, value := range parameters {
		if !strings.EqualFold(name, "charset") || !strings.EqualFold(value, "utf-8") {
			return false
		}
	}
	return true
}

func decodeStrictJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxTransitionPlanRequestBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			return errBodyTooLarge
		}
		return fmt.Errorf("%w: read body", errInvalidJSON)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return fmt.Errorf("%w: empty body", errInvalidJSON)
	}
	if err := validateUniqueTransitionObject(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: decode object", errInvalidJSON)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing value", errInvalidJSON)
	}
	return nil
}

var transitionObjectFields = map[string]map[string]struct{}{
	"": {
		"to": {}, "type": {}, "reason": {}, "precondition": {}, "evidence": {},
	},
	"/precondition": {
		"object_id": {}, "resource_version": {}, "generation": {},
	},
	"/evidence": {
		"passed_checks": {},
	},
}

func validateUniqueTransitionObject(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	first, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%w: malformed root", errInvalidJSON)
	}
	root, ok := first.(json.Delim)
	if !ok || root != '{' {
		return fmt.Errorf("%w: root must be an object", errInvalidJSON)
	}
	if err := walkJSONValue(decoder, root, ""); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing value", errInvalidJSON)
	}
	return nil
}

func walkJSONValue(decoder *json.Decoder, token json.Token, path string) error {
	if token == nil {
		return fmt.Errorf("%w: null is not supported", errInvalidJSON)
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		allowed, knownObject := transitionObjectFields[path]
		if !knownObject {
			return fmt.Errorf("%w: object is not allowed at %s", errInvalidJSON, path)
		}
		seen := make(map[string]struct{}, len(allowed))
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("%w: malformed object key", errInvalidJSON)
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("%w: object key is not a string", errInvalidJSON)
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("%w: duplicate field %q", errInvalidJSON, key)
			}
			if _, supported := allowed[key]; !supported {
				return fmt.Errorf("%w: unsupported field %q", errInvalidJSON, key)
			}
			seen[key] = struct{}{}
			valueToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("%w: malformed value for %q", errInvalidJSON, key)
			}
			if err := walkJSONValue(decoder, valueToken, path+"/"+key); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return fmt.Errorf("%w: unterminated object", errInvalidJSON)
		}
	case '[':
		for decoder.More() {
			valueToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("%w: malformed array", errInvalidJSON)
			}
			if err := walkJSONValue(decoder, valueToken, path+"/[]"); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return fmt.Errorf("%w: unterminated array", errInvalidJSON)
		}
	default:
		return fmt.Errorf("%w: unexpected delimiter", errInvalidJSON)
	}
	return nil
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	response := errorResponse{}
	response.Error.Code = code
	response.Error.Message = message
	writeJSON(w, status, response)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
