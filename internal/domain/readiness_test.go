package domain

import "testing"

func TestEvaluateDomainReadiness(t *testing.T) {
	ready := EvaluateDomainReadiness("samba-ad", true, true, true)
	if !ready.Ready || len(ready.Blockers) != 0 {
		t.Fatalf("expected ready provider: %#v", ready)
	}
	blocked := EvaluateDomainReadiness("freeipa", false, true, false)
	if blocked.Ready || len(blocked.Blockers) != 2 {
		t.Fatalf("expected readiness blockers: %#v", blocked)
	}
	if blocked.Blockers[0] != "dns-not-ready" || blocked.Blockers[1] != "storage-not-ready" {
		t.Fatalf("unexpected blockers: %#v", blocked.Blockers)
	}
}
