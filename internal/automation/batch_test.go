package automation

import "testing"

func TestBatchTargets(t *testing.T) {
	batches := BatchTargets([]string{"pc-b", "pc-a", "pc-b", "pc-c"}, 2)
	if len(batches) != 2 {
		t.Fatalf("unexpected batch count: %d", len(batches))
	}
	if batches[0][0] != "pc-a" || batches[0][1] != "pc-b" || batches[1][0] != "pc-c" {
		t.Fatalf("unexpected batches: %#v", batches)
	}
	if result := BatchTargets([]string{"pc-a"}, 0); result != nil {
		t.Fatalf("expected nil for invalid batch size: %#v", result)
	}
}
