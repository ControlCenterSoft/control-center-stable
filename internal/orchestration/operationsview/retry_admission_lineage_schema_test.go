package operationsview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestJobRetryAdmissionSchemaIncludesAlreadyRetriedBoundary(t *testing.T) {
	path := filepath.Join("..", "..", "..", "api", "operations-job-retry-admission-v1.schema.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read retry admission schema: %v", err)
	}
	var schema struct {
		Required   []string `json:"required"`
		Properties struct {
			SourceAlreadyRetried struct {
				Type string `json:"type"`
			} `json:"source_already_retried"`
			Blockers struct {
				Items struct {
					Enum []string `json:"enum"`
				} `json:"items"`
			} `json:"blockers"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode retry admission schema: %v", err)
	}
	contains := func(values []string, want string) bool {
		for _, value := range values {
			if value == want {
				return true
			}
		}
		return false
	}
	if !contains(schema.Required, "source_already_retried") {
		t.Fatal("source_already_retried must be required retry admission evidence")
	}
	if schema.Properties.SourceAlreadyRetried.Type != "boolean" {
		t.Fatalf("source_already_retried schema type = %q, want boolean", schema.Properties.SourceAlreadyRetried.Type)
	}
	if !contains(schema.Properties.Blockers.Items.Enum, JobRetryBlockerSourceAlreadyRetried) {
		t.Fatalf("retry admission blocker enum lacks %q", JobRetryBlockerSourceAlreadyRetried)
	}
}
