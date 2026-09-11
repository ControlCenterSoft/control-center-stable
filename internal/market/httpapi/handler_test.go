package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"control-center/internal/market"
)

func TestMarketManifestList(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, manifestsPath, nil)
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Items []market.BuiltinManifest `json:"items"`
		Count int                      `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Count != len(response.Items) || response.Count < 7 {
		t.Fatalf("response=%#v", response)
	}
}

func TestMarketManifestGet(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, manifestsPath+"/directory-services", nil)
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var manifest market.BuiltinManifest
	if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.ID != "directory-services" || len(manifest.Providers) != 2 {
		t.Fatalf("manifest=%#v", manifest)
	}
}

func TestMarketManifestV2List(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, manifestsV2Path, nil)
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Items []market.ManifestV2 `json:"items"`
		Count int                 `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Count != len(response.Items) || response.Count < 7 {
		t.Fatalf("response=%#v", response)
	}
	for _, manifest := range response.Items {
		if err := market.ValidateManifestV2(manifest); err != nil {
			t.Fatalf("invalid v2 manifest %q: %v", manifest.ID, err)
		}
		if manifest.Status != market.ManifestStatusTransitional || manifest.Activation.Mode != market.ActivationExplicit {
			t.Fatalf("unsafe migrated manifest: %#v", manifest)
		}
	}
}

func TestMarketManifestV2Get(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, manifestsV2Path+"/directory-services", nil)
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var manifest market.ManifestV2
	if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.ID != "directory-services" || manifest.SchemaVersion != market.ManifestSchemaV2 {
		t.Fatalf("manifest=%#v", manifest)
	}
	if len(manifest.Network.Requirements) != 0 || manifest.Network.Enforcement != market.NetworkPolicyControlled {
		t.Fatalf("network contract=%#v", manifest.Network)
	}
}

func TestMarketManifestNotFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, manifestsPath+"/missing", nil)
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestMarketManifestV2NotFoundAndInvalidID(t *testing.T) {
	for _, test := range []struct {
		path string
		want int
	}{
		{path: manifestsV2Path + "/missing", want: http.StatusNotFound},
		{path: manifestsV2Path + "/bad/id", want: http.StatusBadRequest},
		{path: manifestsV2Path + "/Directory-Services", want: http.StatusBadRequest},
	} {
		req := httptest.NewRequest(http.MethodGet, test.path, nil)
		rec := httptest.NewRecorder()
		New().ServeHTTP(rec, req)
		if rec.Code != test.want {
			t.Fatalf("path=%q status=%d body=%s", test.path, rec.Code, rec.Body.String())
		}
	}
}

func TestMarketManifestV2RejectsQueriesAndMutatingMethods(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, manifestsV2Path+"?activate=true", nil)
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("query status=%d body=%s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, manifestsV2Path, nil)
	rec = httptest.NewRecorder()
	New().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("method status=%d allow=%q body=%s", rec.Code, rec.Header().Get("Allow"), rec.Body.String())
	}
}
