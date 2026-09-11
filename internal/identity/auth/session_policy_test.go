package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSessionSecurityPolicyReturnsEffectiveValuesAndAudits(t *testing.T) {
	service, _, log := newTestService(t)
	service.sessionIdleTimeout = 30 * time.Minute

	view, err := service.SessionSecurityPolicy(context.Background(), SessionSecurityPolicyInput{
		UserID: "user-1", SourceIP: "192.0.2.40",
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.AbsoluteTTLSeconds != 3600 || view.IdleTimeoutSeconds != 1800 {
		t.Fatalf("policy ttl=%d idle=%d, want 3600/1800", view.AbsoluteTTLSeconds, view.IdleTimeoutSeconds)
	}
	if !view.ActivityRefreshesIdleDeadline || view.ActivityExtendsAbsoluteExpiry {
		t.Fatalf("unexpected policy semantics: %#v", view)
	}

	records := log.Records()
	last := records[len(records)-1]
	if last.Action != "auth.session_policy_read" || last.Outcome != "success" || last.ActorID != "user-1" || last.SubjectID != "user-1" {
		t.Fatalf("unexpected audit event: %#v", last)
	}
	if last.SourceIP != "192.0.2.40" {
		t.Fatalf("audit source ip=%q", last.SourceIP)
	}
	if got, ok := last.Details["absolute_ttl_seconds"].(int64); !ok || got != 3600 {
		t.Fatalf("audit ttl=%#v", last.Details["absolute_ttl_seconds"])
	}
	if got, ok := last.Details["idle_timeout_seconds"].(int64); !ok || got != 1800 {
		t.Fatalf("audit idle timeout=%#v", last.Details["idle_timeout_seconds"])
	}
}

func TestSessionSecurityPolicyFailsClosedWhenAuditIsUnavailable(t *testing.T) {
	service, _, _ := newTestService(t)
	service.audit = rejectingAuditLog{}

	view, err := service.SessionSecurityPolicy(context.Background(), SessionSecurityPolicyInput{UserID: "user-1"})
	if !errors.Is(err, ErrAuditUnavailable) {
		t.Fatalf("policy read error=%v, want ErrAuditUnavailable", err)
	}
	if view != (SessionSecurityPolicyView{}) {
		t.Fatalf("policy leaked without audit evidence: %#v", view)
	}
}

func TestSessionSecurityPolicyRejectsUnknownIdentity(t *testing.T) {
	service, _, _ := newTestService(t)
	if _, err := service.SessionSecurityPolicy(context.Background(), SessionSecurityPolicyInput{UserID: "missing"}); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("unknown identity error=%v, want ErrUnauthenticated", err)
	}
}
