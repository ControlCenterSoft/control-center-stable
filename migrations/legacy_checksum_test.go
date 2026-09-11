package migrations

import (
	"crypto/sha256"
	"encoding/hex"
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
