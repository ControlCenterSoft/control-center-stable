package market

import "testing"

func TestMarketCompatibility(t *testing.T) {
	if !IsCompatible([]string{"linux", "windows"}, []string{"amd64"}, CompatibilityRequest{Platform: "linux", Arch: "amd64"}) {
		t.Fatal("expected compatible module")
	}
	if IsCompatible([]string{"linux"}, []string{"amd64"}, CompatibilityRequest{Platform: "windows", Arch: "amd64"}) {
		t.Fatal("expected incompatible platform")
	}
	values := NormalizeCompatibilitySet([]string{"linux", "windows", "linux"})
	if len(values) != 2 || values[0] != "linux" || values[1] != "windows" {
		t.Fatalf("unexpected normalized values: %#v", values)
	}
}
