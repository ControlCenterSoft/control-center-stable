package recovery

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestStrictMetadataDecoders(t *testing.T) {
	tests := []struct {
		name           string
		duplicateField string
		value          any
		decode         func([]byte) error
	}{
		{name: "recovery point", duplicateField: "object_id", value: validRecoveryPoint(), decode: func(raw []byte) error { _, err := DecodeRecoveryPoint(raw); return err }},
		{name: "provider", duplicateField: "provider_id", value: backupProvider(CapabilityBackup), decode: func(raw []byte) error { _, err := DecodeProviderMetadata(raw); return err }},
		{name: "fencing", duplicateField: "state", value: confirmedFencing(), decode: func(raw []byte) error { _, err := DecodeFencingMetadata(raw); return err }},
		{name: "backup", duplicateField: "object_id", value: validBackupMetadata(), decode: func(raw []byte) error { _, err := DecodeBackupMetadata(raw); return err }},
		{name: "restore", duplicateField: "object_id", value: validRestoreMetadata(), decode: func(raw []byte) error { _, err := DecodeRestoreMetadata(raw); return err }},
		{name: "objective", duplicateField: "object_id", value: validObjectiveEvidence(), decode: func(raw []byte) error { _, err := DecodeRecoveryObjectiveEvidence(raw); return err }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw, err := json.Marshal(test.value)
			if err != nil {
				t.Fatal(err)
			}
			if err := test.decode(raw); err != nil {
				t.Fatalf("valid JSON rejected: %v", err)
			}

			unknown := append([]byte(nil), raw[:len(raw)-1]...)
			unknown = append(unknown, []byte(`,"execute_now":true}`)...)
			if err := test.decode(unknown); err == nil || !strings.Contains(err.Error(), "unknown field") {
				t.Fatalf("unknown field error = %v", err)
			}

			duplicate := append([]byte(nil), raw[:len(raw)-1]...)
			duplicate = append(duplicate, []byte(`,"`+test.duplicateField+`":"shadow"}`)...)
			if err := test.decode(duplicate); err == nil || !strings.Contains(err.Error(), "duplicate field") {
				t.Fatalf("duplicate field error = %v", err)
			}

			if err := test.decode(append(raw, []byte(` {}`)...)); err == nil {
				t.Fatal("multiple JSON values accepted")
			}
		})
	}
}

func TestStrictMetadataDecoderSizeAndSyntaxLimits(t *testing.T) {
	if _, err := DecodeRecoveryPoint(nil); err == nil {
		t.Fatal("empty document accepted")
	}
	if _, err := DecodeRecoveryPoint([]byte(`{"schema_version":`)); err == nil {
		t.Fatal("malformed document accepted")
	}
	if _, err := DecodeRecoveryPoint(make([]byte, maxMetadataDocumentBytes+1)); err == nil {
		t.Fatal("oversized document accepted")
	}
	if _, err := DecodeRecoveryPoint([]byte(`[]`)); err == nil {
		t.Fatal("non-object document accepted")
	}
	restoreJSON, err := json.Marshal(validRestoreMetadata())
	if err != nil {
		t.Fatal(err)
	}
	var restoreFields map[string]json.RawMessage
	if err := json.Unmarshal(restoreJSON, &restoreFields); err != nil {
		t.Fatal(err)
	}
	delete(restoreFields, "requested_objects")
	restoreJSON, err = json.Marshal(restoreFields)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRestoreMetadata(restoreJSON); err == nil || !strings.Contains(err.Error(), "requested_objects") {
		t.Fatalf("missing required field error = %v", err)
	}
}

func TestStrictMetadataDecoderEnforcesNestedShape(t *testing.T) {
	point := validRecoveryPoint()
	point.State = RecoveryPointPartial
	point.Failure = testFailure()

	raw, err := json.Marshal(point)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	failure := document["failure"].(map[string]any)
	delete(failure, "retryable")
	missingRetryable, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRecoveryPoint(missingRetryable); err == nil || !strings.Contains(err.Error(), "failure.retryable") {
		t.Fatalf("missing nested required field error = %v", err)
	}

	document["failure"] = map[string]any{
		"code": "PROVIDER_FAILURE", "message": "provider operation failed", "retryable": false,
	}
	falseRetryable, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRecoveryPoint(falseRetryable); err != nil {
		t.Fatalf("explicit false retryable rejected: %v", err)
	}

	document["expires_at"] = nil
	nullOptional, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRecoveryPoint(nullOptional); err == nil || !strings.Contains(err.Error(), "expires_at") {
		t.Fatalf("explicit null optional field error = %v", err)
	}

	delete(document, "expires_at")
	document["objects"] = []any{nil}
	nullArrayItem, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRecoveryPoint(nullArrayItem); err == nil || !strings.Contains(err.Error(), "objects[0]") {
		t.Fatalf("null array item error = %v", err)
	}

	nestedDuplicate := strings.Replace(
		string(raw),
		`"object_id":"database-primary"`,
		`"object_id":"database-primary","object_id":"shadow"`,
		1,
	)
	if _, err := DecodeRecoveryPoint([]byte(nestedDuplicate)); err == nil || !strings.Contains(err.Error(), "duplicate field") || !strings.Contains(err.Error(), "$.objects[0]") {
		t.Fatalf("nested duplicate field error = %v", err)
	}
}

func TestRecoveryMetadataUsesDistributedObjectEnvelope(t *testing.T) {
	values := []any{
		validRecoveryPoint(),
		validBackupMetadata(),
		validRestoreMetadata(),
		validObjectiveEvidence(),
	}
	for _, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"object_id", "scope_id", "owner_scope", "generation", "resource_version", "created_at", "updated_at"} {
			if _, exists := document[field]; !exists {
				t.Fatalf("%T is missing distributed metadata field %q: %s", value, field, raw)
			}
		}
		if _, legacyID := document["id"]; legacyID {
			t.Fatalf("%T emitted legacy top-level id: %s", value, raw)
		}
		if _, ok := document["resource_version"].(string); !ok {
			t.Fatalf("%T resource_version is not opaque string: %s", value, raw)
		}
	}

	raw, err := json.Marshal(validRecoveryPoint())
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["resource_version"] = 7
	numericVersion, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRecoveryPoint(numericVersion); err == nil || !strings.Contains(err.Error(), "resource_version") {
		t.Fatalf("numeric resource_version error = %v", err)
	}
}

func TestMigrateV03MetadataIsDeterministicAndConservative(t *testing.T) {
	first, err := MigrateV03Metadata([]LegacyV03Record{
		{Kind: LegacyResource, ID: "resource-z"},
		{Kind: LegacyJob, ID: "job-b"},
		{Kind: LegacyConfigRevision, ID: "revision-a"},
		{Kind: LegacyJob, ID: "job-a"},
	})
	if err != nil {
		t.Fatalf("MigrateV03Metadata() error = %v", err)
	}
	second, err := MigrateV03Metadata([]LegacyV03Record{
		{Kind: LegacyJob, ID: "job-a"},
		{Kind: LegacyConfigRevision, ID: "revision-a"},
		{Kind: LegacyJob, ID: "job-b"},
		{Kind: LegacyResource, ID: "resource-z"},
	})
	if err != nil {
		t.Fatalf("MigrateV03Metadata() reordered error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("migration depends on input order:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if first.SchemaVersion != V03CompatibilitySchemaVersion || first.SourceProductVersion != V03ProductVersion {
		t.Fatalf("migration identity = %#v", first)
	}
	if len(first.RecoveryPoints) != 0 || len(first.Backups) != 0 || len(first.Restores) != 0 || len(first.ObjectiveEvidence) != 0 {
		t.Fatalf("0.3 generic records were promoted to typed recovery evidence: %#v", first)
	}
	if len(first.Quarantined) != 4 || first.Quarantined[0].Kind != LegacyConfigRevision || first.Quarantined[1].ID != "job-a" || first.Quarantined[2].ID != "job-b" || first.Quarantined[3].Kind != LegacyResource {
		t.Fatalf("quarantine order = %#v", first.Quarantined)
	}
	for _, record := range first.Quarantined {
		if record.Disposition != LegacyRequiresReview || record.Reason == "" {
			t.Fatalf("unsafe legacy disposition: %#v", record)
		}
	}
	if len(first.Warnings) != 3 {
		t.Fatalf("migration warnings = %#v", first.Warnings)
	}
}

func TestMigrateV03EmptyStateProducesStableEmptyCollections(t *testing.T) {
	result, err := MigrateV03Metadata(nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"recovery_points":[]`, `"backups":[]`, `"restores":[]`, `"objective_evidence":[]`, `"quarantined":[]`} {
		if !strings.Contains(string(raw), expected) {
			t.Fatalf("migration JSON %s missing %s", raw, expected)
		}
	}
	if len(result.Warnings) != 2 {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
}

func TestMigrateV03MetadataRejectsAmbiguousInput(t *testing.T) {
	tests := [][]LegacyV03Record{
		{{Kind: "BACKUP", ID: "legacy-1"}},
		{{Kind: LegacyJob, ID: "Legacy Job"}},
		{{Kind: LegacyJob, ID: "job-1"}, {Kind: LegacyJob, ID: "job-1"}},
	}
	for _, records := range tests {
		if _, err := MigrateV03Metadata(records); err == nil {
			t.Fatalf("invalid legacy records accepted: %#v", records)
		}
	}
}
