package acceptance

import (
	"errors"
	"testing"
)

func TestMatrixNormalizesOrderAndTags(t *testing.T) {
	matrix, err := Matrix([]Scenario{
		{Name: "install", Platform: "windows", Tags: []string{"pxe", "core"}},
		{Name: "inventory", Platform: "linux", Tags: []string{"market", "agent"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if matrix[0].Platform != "linux" || matrix[1].Platform != "windows" {
		t.Fatalf("unexpected matrix order: %#v", matrix)
	}
	if matrix[0].Tags[0] != "agent" {
		t.Fatalf("tags were not normalized: %#v", matrix[0].Tags)
	}
}

func TestMatrixRejectsDuplicateScenario(t *testing.T) {
	_, err := Matrix([]Scenario{{Name: "install", Platform: "linux"}, {Name: "install", Platform: "linux"}})
	if !errors.Is(err, ErrInvalidScenario) {
		t.Fatalf("got %v, want ErrInvalidScenario", err)
	}
}
