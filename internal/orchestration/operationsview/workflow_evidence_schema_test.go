package operationsview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOperationsWorkflowEvidenceSchemaMatchesImplementationBounds(t *testing.T) {
	path := filepath.Join("..", "..", "..", "api", "operations-workflow-evidence-v1.schema.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read workflow-evidence schema: %v", err)
	}

	var schema struct {
		Properties struct {
			ContractVersion struct {
				Const string `json:"const"`
			} `json:"contract_version"`
			ChangeID struct {
				MaxLength int `json:"maxLength"`
			} `json:"change_id"`
			RevisionID struct {
				MaxLength int `json:"maxLength"`
			} `json:"revision_id"`
			JobID struct {
				MaxLength int `json:"maxLength"`
			} `json:"job_id"`
			RecoveryPointID struct {
				MaxLength int `json:"maxLength"`
			} `json:"recovery_point_id"`
			RevisionDigest struct {
				Pattern string `json:"pattern"`
			} `json:"revision_digest"`
			ResultOutputDigest struct {
				Pattern string `json:"pattern"`
			} `json:"result_output_digest"`
			BlockReasons struct {
				MaxItems int  `json:"maxItems"`
				Unique   bool `json:"uniqueItems"`
			} `json:"block_reasons"`
			ExecutionAuthorized struct {
				Const bool `json:"const"`
			} `json:"execution_authorized"`
			ProductionMutationAllowed struct {
				Const bool `json:"const"`
			} `json:"production_mutation_allowed"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode workflow-evidence schema: %v", err)
	}

	if schema.Properties.ContractVersion.Const != OperationsWorkflowEvidenceContractVersion {
		t.Fatalf("contract version drift: schema=%q code=%q", schema.Properties.ContractVersion.Const, OperationsWorkflowEvidenceContractVersion)
	}
	for name, bound := range map[string]int{
		"change_id":         schema.Properties.ChangeID.MaxLength,
		"revision_id":       schema.Properties.RevisionID.MaxLength,
		"job_id":            schema.Properties.JobID.MaxLength,
		"recovery_point_id": schema.Properties.RecoveryPointID.MaxLength,
	} {
		if bound != MaxOperationsWorkflowEvidenceIdentifierLength {
			t.Fatalf("%s maxLength drift: schema=%d code=%d", name, bound, MaxOperationsWorkflowEvidenceIdentifierLength)
		}
	}
	for name, pattern := range map[string]string{
		"revision_digest":      schema.Properties.RevisionDigest.Pattern,
		"result_output_digest": schema.Properties.ResultOutputDigest.Pattern,
	} {
		if pattern != "^sha256:[0-9a-f]{64}$" {
			t.Fatalf("%s digest pattern drift: %q", name, pattern)
		}
	}
	if schema.Properties.BlockReasons.MaxItems != 4 || !schema.Properties.BlockReasons.Unique {
		t.Fatalf("block reason bounds drift: max=%d unique=%v", schema.Properties.BlockReasons.MaxItems, schema.Properties.BlockReasons.Unique)
	}
	if schema.Properties.ExecutionAuthorized.Const || schema.Properties.ProductionMutationAllowed.Const {
		t.Fatal("workflow evidence schema must not permit execution authority")
	}
}
