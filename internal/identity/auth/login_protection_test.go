package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func testLoginProtectionPolicy() LoginProtectionPolicy {
	return LoginProtectionPolicy{
		FailureWindow:       2 * time.Minute,
		AccountFailureLimit: 2,
		SourceFailureLimit:  4,
		BlockDuration:       3 * time.Minute,
		MaxTrackedKeys:      128,
	}
}

func TestLoginProtectionPolicyValidation(t *testing.T) {
	policy := testLoginProtectionPolicy()
	if err := policy.validate(); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}
	policy.AccountFailureLimit = 1
	if err := policy.validate(); err == nil {
		t.Fatal("unsafe account failure limit accepted")
	}
}

func TestLoginProtectorBlocksAccountAndExpires(t *testing.T) {
	protector := newLoginProtector(testLoginProtectionPolicy())
	now := time.Date(2026, 9, 10, 2, 0, 0, 0, time.UTC)

	if protector.Blocked("administrator", "192.0.2.10", now) {
		t.Fatal("fresh login key unexpectedly blocked")
	}
	if protector.RecordFailure("administrator", "192.0.2.10", now) {
		t.Fatal("first failure blocked account")
	}
	if !protector.RecordFailure("administrator", "192.0.2.11", now.Add(time.Second)) {
		t.Fatal("account failure threshold did not block")
	}
	if !protector.Blocked("administrator", "198.51.100.20", now.Add(time.Minute)) {
		t.Fatal("account block did not apply across source addresses")
	}
	if protector.Blocked("administrator", "198.51.100.20", now.Add(3*time.Minute+2*time.Second)) {
		t.Fatal("expired account block remained active")
	}
}

func TestLoginProtectorBlocksUsernameSprayBySource(t *testing.T) {
	policy := testLoginProtectionPolicy()
	policy.AccountFailureLimit = 4
	policy.SourceFailureLimit = 4
	protector := newLoginProtector(policy)
	now := time.Date(2026, 9, 10, 2, 0, 0, 0, time.UTC)

	for i := 0; i < 3; i++ {
		if protector.RecordFailure(fmt.Sprintf("missing-%d", i), "203.0.113.7", now.Add(time.Duration(i)*time.Second)) {
			t.Fatalf("source blocked before configured threshold at failure %d", i+1)
		}
	}
	if !protector.RecordFailure("missing-3", "203.0.113.7", now.Add(3*time.Second)) {
		t.Fatal("source spray threshold did not block")
	}
	if !protector.Blocked("administrator", "203.0.113.7", now.Add(time.Minute)) {
		t.Fatal("source block did not protect a different username")
	}
}

func TestLoginProtectorBoundsTrackedKeys(t *testing.T) {
	policy := testLoginProtectionPolicy()
	protector := newLoginProtector(policy)
	now := time.Date(2026, 9, 10, 2, 0, 0, 0, time.UTC)

	for i := 0; i < 200; i++ {
		protector.RecordFailure(fmt.Sprintf("user-%d", i), fmt.Sprintf("198.51.100.%d", (i%250)+1), now.Add(time.Duration(i)*time.Millisecond))
	}
	if got := len(protector.buckets); got > policy.MaxTrackedKeys {
		t.Fatalf("tracked login keys exceeded bound: got=%d max=%d", got, policy.MaxTrackedKeys)
	}
}

func TestServiceLoginProtectionBlocksWithoutAccountEnumeration(t *testing.T) {
	service, _, _ := newTestService(t)
	service.loginProtection = newLoginProtector(testLoginProtectionPolicy())
	now := time.Date(2026, 9, 10, 2, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	for i := 0; i < 2; i++ {
		_, err := service.Login(context.Background(), LoginInput{
			Username: "administrator",
			Password: "incorrect password value",
			SourceIP: fmt.Sprintf("192.0.2.%d", i+1),
		})
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("failed login %d returned %v", i+1, err)
		}
	}

	_, err := service.Login(context.Background(), LoginInput{
		Username: "administrator",
		Password: "synthetic test password long enough",
		SourceIP: "198.51.100.9",
	})
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("blocked correct credential did not use generic error: %v", err)
	}

	now = now.Add(3*time.Minute + time.Second)
	issued, err := service.Login(context.Background(), LoginInput{
		Username: "administrator",
		Password: "synthetic test password long enough",
		SourceIP: "198.51.100.9",
	})
	if err != nil || issued.Token == "" {
		t.Fatalf("login did not recover after block expiry: issued=%#v err=%v", issued, err)
	}
}
