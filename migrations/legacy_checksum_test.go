package migrations

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
)

func TestV031MigrationChecksumsRemainImmutable(t *testing.T) {
	expected := map[string]string{
		"0001_initial.up.sql":                         "d672eacc8d416910a91985ceef669c94e5c13d95afccbadbf4b51dbc91f51d7a",
		"0002_local_identity_rbac_audit.up.sql":       "13813436248b037d18e75de644c1969120cc737311e295096664224e1c223f49",
		"0003_identity_persistence_invariants.up.sql": "7c2ad4d2c44fa77004adecb7609c91c22aa58b3ba5ba84947160dd101b86a2a1",
		"0004_change_execution_core.up.sql":           "6fbecffabd0716e07842d8843daa5ff87b0700ba773fbafb3df77697570bccc1",
	}

	for name, want := range expected {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read immutable migration %s: %v", name, err)
		}
		sum := sha256.Sum256(data)
		got := hex.EncodeToString(sum[:])
		if got != want {
			t.Fatalf("historical migration %s changed: got %s want %s", name, got, want)
		}
	}
}

// TestV026PublishedMigrationsRemainByteImmutable protects every SQL migration
// shipped in the canonical v0.26.0 Public Stable baseline. The expected values
// are the Git blob object IDs from that immutable release tree; recomputing the
// object ID locally is an exact byte-for-byte content check, including line
// endings and trailing whitespace.
func TestV026PublishedMigrationsRemainByteImmutable(t *testing.T) {
	expected := map[string]string{
		"0001_initial.down.sql":                        "424a6a478f52e6ea45ecac4736ac8cc7a7983e5f",
		"0001_initial.up.sql":                          "61831c6c49866473eecef8f2bcbec50ee862bc14",
		"0002_local_identity_rbac_audit.up.sql":        "b6a4cf428805c252732c628f1c79b6eb7b820c25",
		"0003_identity_persistence_invariants.up.sql":  "07feec8974b4cc96f7309ab94c6c7cb30359b002",
		"0004_change_execution_core.down.sql":          "a85c9f2d62a6d71feed758ab4ae71cf94337a929",
		"0004_change_execution_core.up.sql":            "931fe59967aabb97aeb5c3eb340d9c64d90630ed",
		"0005_first_login_password_change.down.sql":    "eab295e23ab3016c1bf436d3421c24c91f56678f",
		"0005_first_login_password_change.up.sql":      "8d33af153ba04dc7f9c90e8ad7a6926af49b835b",
		"0006_distributed_core_objects.down.sql":       "37d84e6eb46374c5480ee908cc81e7e7fc1f24c3",
		"0006_distributed_core_objects.up.sql":         "f4b8c14f754ef13143165cc0f3839649f2b1a22f",
		"0007_network_contract_objects.down.sql":       "b9ba74ac99d8e351b24ea8e39c738fc31169fd27",
		"0007_network_contract_objects.up.sql":         "ab46c0530dfe2d274d2451714dba2c28c54f4b0e",
		"0008_builtin_rbac_permissions.down.sql":       "be7ea635f6fdb7e7d8930d6738cce869f86d0b87",
		"0008_builtin_rbac_permissions.up.sql":         "b0ebaba27eb43e80ffc66018877a92fa836dd315",
		"0009_auth_session_activity.down.sql":          "576a6273333c456a59a4f273a3c41c0c9ae0f713",
		"0009_auth_session_activity.up.sql":            "32cb31befa0d46350d800481c0a61ae31477ef4e",
		"0010_legacy_03_schema_compatibility.down.sql": "b734ce95a502e6172454fb95701bf5530acd3706",
		"0010_legacy_03_schema_compatibility.up.sql":   "aea17eda8d0589644d5aac656be5cb17c1ec449b",
	}

	for name, want := range expected {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read published migration %s: %v", name, err)
		}

		h := sha1.New()
		_, _ = fmt.Fprintf(h, "blob %d%c", len(data), byte(0))
		_, _ = h.Write(data)
		got := hex.EncodeToString(h.Sum(nil))
		if got != want {
			t.Fatalf("published migration %s changed byte-for-byte: got git blob %s want %s", name, got, want)
		}
	}
}
