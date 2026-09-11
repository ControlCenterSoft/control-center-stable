package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"control-center/internal/agent"
)

func TestEnrollmentHandlerNormalizesCapabilities(t *testing.T) {
	body := `{"node_id":" node-01 ","hostname":" host-01 ","capabilities":["Inventory","inventory"," PXE "]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/enrollment/normalize", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	EnrollmentHandler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	for _, want := range []string{`"node_id":"node-01"`, `"hostname":"host-01"`, `"capabilities":["inventory","pxe"]`} {
		if !strings.Contains(res.Body.String(), want) {
			t.Fatalf("body=%s missing %s", res.Body.String(), want)
		}
	}
}

func TestEnrollmentHandlerRejectsMissingNode(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/enrollment/normalize", strings.NewReader(`{"hostname":"host-01"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	EnrollmentHandler().ServeHTTP(res, req)
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestEnrollmentHandlerRejectsUnknownField(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/enrollment/normalize", strings.NewReader(`{"node_id":"node-01","hostname":"host-01","secret":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	EnrollmentHandler().ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestEnrollmentHandlerNormalizesV2WithoutSideEffects(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/enrollment/normalize", strings.NewReader(validV2EnrollmentBody()))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	res := httptest.NewRecorder()
	EnrollmentHandler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	var result agent.EnrollmentNormalization
	if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !result.Preconditions.Ready {
		t.Fatalf("preconditions=%#v", result.Preconditions)
	}
	if result.Effects.PersistsEnrollment || result.Effects.NetworkMutation {
		t.Fatalf("effects=%#v", result.Effects)
	}
	if len(result.NetworkInterfaces) != 2 || result.NetworkInterfaces[0].ID != "lan0" || result.NetworkInterfaces[1].ID != "wan0" {
		t.Fatalf("network_interfaces=%#v", result.NetworkInterfaces)
	}
}

func TestEnrollmentHandlerRejectsDuplicateKeysAndJSONLikeMediaType(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
	}{
		{
			name: "duplicate nested key", contentType: "application/json",
			body:       `{"node_id":"node-01","hostname":"host-01","identity":{"agent_id":"a","agent_id":"b"}}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "json prefix is not json", contentType: "application/jsonp",
			body:       `{"node_id":"node-01","hostname":"host-01"}`,
			wantStatus: http.StatusUnsupportedMediaType,
		},
		{
			name: "empty v2 field without version", contentType: "application/json",
			body:       `{"node_id":"node-01","hostname":"host-01","roles":[]}`,
			wantStatus: http.StatusBadRequest,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/enrollment/normalize", strings.NewReader(test.body))
			req.Header.Set("Content-Type", test.contentType)
			res := httptest.NewRecorder()
			EnrollmentHandler().ServeHTTP(res, req)
			if res.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", res.Code, test.wantStatus, res.Body.String())
			}
		})
	}
}

func TestEnrollmentHandlerRejectsOversizedBody(t *testing.T) {
	body := `{"node_id":"node-01","hostname":"` + strings.Repeat("x", maxEnrollmentRequestBytes) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/enrollment/normalize", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	EnrollmentHandler().ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestTransitionalStateHandlerDoesNotPartiallyPersistV2(t *testing.T) {
	handler := StateHandler(agent.NewMemoryRegistry())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/enrollments", strings.NewReader(validV2EnrollmentBody()))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}

	res = httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1/agent/nodes", nil))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"count":0`) {
		t.Fatalf("registry state status=%d body=%s", res.Code, res.Body.String())
	}
}

func validV2EnrollmentBody() string {
	fingerprintA := strings.Repeat("ab", 32)
	fingerprintB := strings.Repeat("cd", 32)
	return fmt.Sprintf(`{
		"contract_version":"agent.enrollment/v2",
		"node_id":"node-01",
		"hostname":"NODE-01.EXAMPLE.TEST",
		"capabilities":["inventory","monitoring"],
		"roles":["agent","management-node","worker-node"],
		"site_id":"site-a",
		"management_zone_id":"zone-a",
		"collected_at":"2026-09-08T12:00:00Z",
		"hardware":{
			"machine_id":"machine-01","architecture":"amd64",
			"cpu":{"model":"Example CPU","sockets":1,"physical_cores":4,"logical_cores":8},
			"memory_bytes":17179869184,
			"storage":[{"id":"disk0","kind":"nvme","capacity_bytes":1099511627776,"boot":true}]
		},
		"network_interfaces":[
			{"id":"wan0","name":"eth1","kind":"physical","mac_address":"02:00:00:00:00:02","operational_state":"up","zone":"wan","addresses":["198.51.100.10/24"],"mtu":1500,"link_speed_mbps":1000},
			{"id":"lan0","name":"eth0","kind":"physical","mac_address":"02:00:00:00:00:01","operational_state":"up","zone":"management","addresses":["10.0.0.10/24"],"mtu":1500,"link_speed_mbps":1000}
		],
		"identity":{
			"agent_id":"agent-01","installation_id":"install-01","trusted_ca_fingerprint_sha256":"%s",
			"certificate":{"subject":"CN=node-01","issuer":"CN=CC CA","serial_number":"01","fingerprint_sha256":"%s","not_before":"2026-09-08T11:00:00Z","not_after":"2026-09-09T12:00:00Z","public_key_algorithm":"ed25519","dns_names":["node-01.example.test"]}
		},
		"capacity_observations":[
			{"metric":"cpu.utilization","target_id":"node-01","value":10,"unit":"percent","observed_at":"2026-09-08T11:59:00Z","evidence":"measured"},
			{"metric":"memory.used","target_id":"node-01","value":4294967296,"unit":"bytes","observed_at":"2026-09-08T11:59:00Z","evidence":"measured"},
			{"metric":"storage.used","target_id":"disk0","value":107374182400,"unit":"bytes","observed_at":"2026-09-08T11:59:00Z","evidence":"measured"},
			{"metric":"network.throughput","target_id":"lan0","value":10000000,"unit":"bits-per-second","observed_at":"2026-09-08T11:59:00Z","evidence":"measured"}
		]
	}`, fingerprintA, fingerprintB)
}
