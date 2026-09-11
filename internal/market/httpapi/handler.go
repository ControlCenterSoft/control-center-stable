package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"control-center/internal/market"
)

const (
	manifestsPath   = "/api/v1/market/manifests"
	manifestsV2Path = "/api/v2/market/manifests"
)

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func New() http.Handler {
	return http.HandlerFunc(serveManifests)
}

func serveManifests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method is not allowed")
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "INVALID_QUERY", "query parameters are not supported")
		return
	}
	if r.URL.Path == manifestsPath || r.URL.Path == manifestsPath+"/" {
		items := market.BuiltinManifests()
		writeJSON(w, http.StatusOK, struct {
			Items []market.BuiltinManifest `json:"items"`
			Count int                      `json:"count"`
		}{Items: items, Count: len(items)})
		return
	}
	if r.URL.Path == manifestsV2Path || r.URL.Path == manifestsV2Path+"/" {
		items, err := market.BuiltinManifestsV2()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "MANIFEST_CONTRACT_INVALID", "built-in market manifest contract is invalid")
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Items []market.ManifestV2 `json:"items"`
			Count int                 `json:"count"`
		}{Items: items, Count: len(items)})
		return
	}
	if strings.HasPrefix(r.URL.Path, manifestsV2Path+"/") {
		serveManifestV2(w, r)
		return
	}
	prefix := manifestsPath + "/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		writeError(w, http.StatusNotFound, "ROUTE_NOT_FOUND", "route was not found")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, prefix)
	if id == "" || strings.Contains(id, "/") || len(id) > 128 {
		writeError(w, http.StatusBadRequest, "INVALID_MANIFEST_ID", "manifest id must be a single path segment")
		return
	}
	manifest, ok := market.FindBuiltinManifest(id)
	if !ok {
		writeError(w, http.StatusNotFound, "MANIFEST_NOT_FOUND", "market manifest was not found")
		return
	}
	writeJSON(w, http.StatusOK, manifest)
}

func serveManifestV2(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, manifestsV2Path+"/")
	if strings.Contains(id, "/") || !market.IsValidManifestID(id) {
		writeError(w, http.StatusBadRequest, "INVALID_MANIFEST_ID", "manifest id must be a lowercase identifier")
		return
	}
	manifest, ok, err := market.FindBuiltinManifestV2(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "MANIFEST_CONTRACT_INVALID", "built-in market manifest contract is invalid")
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "MANIFEST_NOT_FOUND", "market manifest was not found")
		return
	}
	writeJSON(w, http.StatusOK, manifest)
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
