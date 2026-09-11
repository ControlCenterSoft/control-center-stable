package migrations

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
)

func TestReleasedMigrationChecksumsRemainImmutable(t *testing.T) {
	expected := map[string]string{
		"0001_initial.up.sql":                         "d672eacc8d416910a91985ceef669c94e5c13d95afccbadbf4b51dbc91f51d7a",
		"0002_local_identity_rbac_audit.up.sql":       "13813436248b037d18e75de644c1969120cc737311e295096664224e1c223f49",
		"0003_identity_persistence_invariants.up.sql": "7c2ad4d2c44fa77004adecb7609c91c22aa58b3ba5ba84947160dd101b86a2a1",
		"0004_change_execution_core.up.sql":           "6fbecffabd0716e07842d8843daa5ff87b0700ba773fbafb3df77697570bccc1",
		"0005_first_login_password_change.up.sql":     "b72668ae787c0bcc4685465bd22cb351cd2150a039c24c19c985bb3cf5bd4e6f",
		"0006_distributed_core_objects.up.sql":         "cd917082106fb2e4b449b07e7a270c734c9332fd5265b4bcf160cb4b632cb3bc",
		"0007_network_contract_objects.up.sql":         "177e6e15929b588bd91abd279f8bf1cdaa34626c86701e9ed5483382b2f3d9a9",
		"0008_builtin_rbac_permissions.up.sql":         "025fa2d9434d966c0a5deda99054fecb23a4f9362221abec696724b19822e27c",
		"0009_auth_session_activity.up.sql":            "aba2ae7f12ba26fc354c716c71daafd9225538b3b60f353bfa648fc2f84ffe2a",
	}

	for name, want := range expected {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read immutable migration %s: %v", name, err)
		}
		sum := sha256.Sum256(data)
		got := hex.EncodeToString(sum[:])
		if got != want {
			t.Fatalf("released migration %s changed: got %s want %s", name, got, want)
		}
	}
}
