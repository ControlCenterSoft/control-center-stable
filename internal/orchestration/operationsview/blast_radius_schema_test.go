package operationsview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBlastRadiusSchemaMatchesImplementationBounds(t *testing.T) {
	path := filepath.Join("..", "..", "..", "api", "operations-blast-radius-v1.schema.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read blast-radius schema: %v", err)
	}

	var schema struct {
		Properties struct {
			ContractVersion struct {
				Const string `json:"const"`
			} `json:"contract_version"`
			ResourceCount struct {
				Maximum int `json:"maximum"`
			} `json:"resource_count"`
			Resources struct {
				MaxItems int `json:"maxItems"`
			} `json:"resources"`
		} `json:"properties"`
		Defs struct {
			Identifier struct {
				MaxLength int `json:"maxLength"`
			} `json:"identifier"`
			Kind struct {
				MaxLength int `json:"maxLength"`
			} `json:"kind"`
			ReasonCode struct {
				MaxLength int `json:"maxLength"`
			} `json:"reason_code"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode blast-radius schema: %v", err)
	}

	if schema.Properties.ContractVersion.Const != BlastRadiusContractVersion {
		t.Fatalf("contract version drift: schema=%q code=%q", schema.Properties.ContractVersion.Const, BlastRadiusContractVersion)
	}
	if schema.Properties.ResourceCount.Maximum != MaxBlastRadiusResources || schema.Properties.Resources.MaxItems != MaxBlastRadiusResources {
		t.Fatalf("resource bound drift: count=%d items=%d code=%d", schema.Properties.ResourceCount.Maximum, schema.Properties.Resources.MaxItems, MaxBlastRadiusResources)
	}
	if schema.Defs.Identifier.MaxLength != MaxBlastRadiusIdentifierLength {
		t.Fatalf("identifier bound drift: schema=%d code=%d", schema.Defs.Identifier.MaxLength, MaxBlastRadiusIdentifierLength)
	}
	if schema.Defs.Kind.MaxLength != MaxBlastRadiusKindLength {
		t.Fatalf("kind bound drift: schema=%d code=%d", schema.Defs.Kind.MaxLength, MaxBlastRadiusKindLength)
	}
	if schema.Defs.ReasonCode.MaxLength != MaxBlastRadiusReasonCodeLength {
		t.Fatalf("reason-code bound drift: schema=%d code=%d", schema.Defs.ReasonCode.MaxLength, MaxBlastRadiusReasonCodeLength)
	}
}
