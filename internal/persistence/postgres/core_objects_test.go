package postgres

import "testing"

func TestNewCoreObjectRepositoryRequiresDatabase(t *testing.T) {
	if _, err := NewCoreObjectRepository(nil); err == nil {
		t.Fatal("nil database was accepted")
	}
}
