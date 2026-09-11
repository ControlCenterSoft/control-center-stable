package siteoffline

import "testing"

func admittedQueueEntry(t *testing.T) QueueEntry {
	t.Helper()
	decision, err := BuildAdmissionDecision(validRequest(OperationJob, "job.restart.plan"), validPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return *decision.QueueEntry
}

func reconnectState(entry QueueEntry) ReconnectState {
	return ReconnectState{
		SiteID:          entry.SiteID,
		ScopeID:         entry.ScopeID,
		ObjectID:        entry.ObjectID,
		Generation:      entry.BaseGeneration,
		ResourceVersion: entry.BaseResourceVersion,
	}
}

func TestClassifyReconnectBuildsApprovedChangeCandidateOnExactBase(t *testing.T) {
	entry := admittedQueueEntry(t)
	got, err := ClassifyReconnect(entry, reconnectState(entry))
	if err != nil {
		t.Fatalf("ClassifyReconnect() error = %v", err)
	}
	if got.Status != ReconciliationReady || got.Reason != ReconnectBaseMatches || !got.RequiresApprovedChange ||
		got.RequiresOperatorReview || got.ConflictID != "" || got.ProductionMutationEnabled {
		t.Fatalf("reconciliation decision = %#v", got)
	}
}

func TestClassifyReconnectRecognizesIdempotentReceipt(t *testing.T) {
	entry := admittedQueueEntry(t)
	current := reconnectState(entry)
	current.Generation++
	current.ResourceVersion = "rv:service:13"
	current.AppliedIntentDigest = entry.IntentDigest
	got, err := ClassifyReconnect(entry, current)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != ReconciliationAlreadyReflected || got.Reason != ReconnectIntentAlreadyExists ||
		got.RequiresApprovedChange || got.RequiresOperatorReview || got.ConflictID != "" {
		t.Fatalf("reconciliation decision = %#v", got)
	}
}

func TestClassifyReconnectProducesDeterministicConflictMetadata(t *testing.T) {
	entry := admittedQueueEntry(t)
	current := reconnectState(entry)
	current.Generation++
	current.ResourceVersion = "rv:service:13-other"
	first, err := ClassifyReconnect(entry, current)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ClassifyReconnect(entry, current)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != ReconciliationConflict || first.Reason != ReconnectGenerationAdvanced ||
		!first.RequiresOperatorReview || first.RequiresApprovedChange || first.ConflictID == "" ||
		first.DecisionID != second.DecisionID || first.ConflictID != second.ConflictID {
		t.Fatalf("conflict decisions = %#v %#v", first, second)
	}
}

func TestClassifyReconnectSeparatesBehindAndVersionDivergence(t *testing.T) {
	entry := admittedQueueEntry(t)
	behind := reconnectState(entry)
	behind.Generation--
	behind.ResourceVersion = "rv:service:11"
	got, err := ClassifyReconnect(entry, behind)
	if err != nil || got.Reason != ReconnectGenerationBehind {
		t.Fatalf("behind decision = %#v, error = %v", got, err)
	}

	diverged := reconnectState(entry)
	diverged.ResourceVersion = "rv:service:12-other"
	got, err = ClassifyReconnect(entry, diverged)
	if err != nil || got.Reason != ReconnectVersionDiverged {
		t.Fatalf("diverged decision = %#v, error = %v", got, err)
	}
}

func TestClassifyReconnectRejectsIdentityMismatch(t *testing.T) {
	entry := admittedQueueEntry(t)
	current := reconnectState(entry)
	current.ScopeID = "scope-site-b"
	if _, err := ClassifyReconnect(entry, current); err == nil {
		t.Fatal("cross-scope reconnect state was accepted")
	}
}
