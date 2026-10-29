package modelprice

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	modelpricerepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/modelprice"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

func TestCandidateSyncDoesNotOverwriteActivePrices(t *testing.T) {
	// B1: Active价格与Candidate价格分离，Sync不直接覆盖active
	ctx := context.Background()
	st := testutil.NewStore(t, testutil.NewConfig(t))

	// 1. Set initial active price for "test-model"
	initialPrices := map[string]store.ModelPrice{
		"test-model": {
			Prompt:      1.0,
			Completion:  2.0,
			Capability:  "chat",
			BillingUnit: "tokens",
		},
	}
	if err := st.SaveModelPrices(ctx, initialPrices); err != nil {
		t.Fatalf("save initial price: %v", err)
	}

	// 2. Upstream sync server provides changed price (price increase to 5.0 / 10.0)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"test-model": {"input_cost_per_token": 0.000005, "output_cost_per_token": 0.000010}
		}`))
	}))
	t.Cleanup(source.Close)
	syncURL := source.URL

	svc := New(st, &syncURL)
	syncRes, err := svc.Sync(ctx, SyncRequest{Models: []string{"test-model"}})
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if syncRes.Imported != 1 {
		t.Fatalf("expected 1 candidate imported, got %d", syncRes.Imported)
	}

	// 3. Verify active price in database is UNCHANGED! (Still 1.0 / 2.0)
	activePrices, err := st.LoadModelPrices(ctx)
	if err != nil {
		t.Fatalf("load active prices: %v", err)
	}
	active := activePrices["test-model"]
	if active.Prompt != 1.0 || active.Completion != 2.0 {
		t.Fatalf("active price was overwritten! Prompt=%v, Completion=%v", active.Prompt, active.Completion)
	}

	// 4. Verify candidate is recorded in candidate table with status = pending, change_kind = price_increase
	candidates, total, err := svc.ListCandidates(ctx, modelpricerepo.CandidateFilter{
		Model: "test-model",
	})
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if total != 1 || len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got total=%d, len=%d", total, len(candidates))
	}
	cand := candidates[0]
	if cand.Status != model.CandidateStatusPending || cand.ChangeKind != model.ChangeKindPriceIncrease {
		t.Fatalf("unexpected candidate status=%s, changeKind=%s", cand.Status, cand.ChangeKind)
	}
	if cand.PromptPer1M != 5.0 || cand.CompletionPer1M != 10.0 {
		t.Fatalf("unexpected candidate price prompt=%v, comp=%v", cand.PromptPer1M, cand.CompletionPer1M)
	}

	// 5. Repeat sync: must be IDEMPOTENT (no duplicate candidate rows)
	_, errRepeat := svc.Sync(ctx, SyncRequest{Models: []string{"test-model"}})
	if errRepeat != nil {
		t.Fatalf("repeat sync failed: %v", errRepeat)
	}
	candidatesAfter, totalAfter, _ := svc.ListCandidates(ctx, modelpricerepo.CandidateFilter{
		Model: "test-model",
	})
	if totalAfter != 1 || len(candidatesAfter) != 1 {
		t.Fatalf("sync was not idempotent! got %d candidates", totalAfter)
	}
}

func TestPriceChangeDetectionCriticalBlockedAndFreeze(t *testing.T) {
	// B2: 0 -> 非0 (free_to_paid), billing_unit变化, upstream_model变化 -> critical + blocked + freeze
	ctx := context.Background()
	st := testutil.NewStore(t, testutil.NewConfig(t))

	// Set free active model
	if err := st.SaveModelPrices(ctx, map[string]store.ModelPrice{
		"free-model": {
			Prompt:        0,
			Completion:    0,
			UnitPrice:     0,
			BillingUnit:   "tokens",
			SourceModelID: "upstream-v1",
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Sync server returns paid price for free-model (Free to Paid attack/drift)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"free-model": {"input_cost_per_token": 0.000002, "output_cost_per_token": 0.000004}
		}`))
	}))
	t.Cleanup(source.Close)
	syncURL := source.URL

	svc := New(st, &syncURL)
	_, err := svc.Sync(ctx, SyncRequest{Models: []string{"free-model"}})
	if err != nil {
		t.Fatal(err)
	}

	// Candidate must be BLOCKED
	candidates, _, err := svc.ListCandidates(ctx, modelpricerepo.CandidateFilter{Model: "free-model"})
	if err != nil || len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %v", err)
	}
	if candidates[0].Status != model.CandidateStatusBlocked || candidates[0].ChangeKind != model.ChangeKindFreeToPaid {
		t.Fatalf("expected blocked free_to_paid, got status=%s, kind=%s", candidates[0].Status, candidates[0].ChangeKind)
	}

	// Model must be automatically FROZEN
	freezeStatus, err := svc.GetFreezeStatus(ctx, candidates[0].Provider, "free-model")
	if err != nil || !freezeStatus.IsFrozen {
		t.Fatalf("expected model to be frozen, got status=%#v, err=%v", freezeStatus, err)
	}

	// Unfreeze without review must be REJECTED!
	errUnfreeze := svc.UnfreezeModel(ctx, candidates[0].Provider, "free-model", "admin", "trying to unfreeze")
	if errUnfreeze == nil {
		t.Fatal("expected unfreeze to be rejected while blocked candidates exist")
	}
}

func TestCandidateApprovalAndSnapshotLifecycle(t *testing.T) {
	// B4: Approve/Reject candidate, snapshot generation, and audit logging
	ctx := context.Background()
	st := testutil.NewStore(t, testutil.NewConfig(t))

	// Initial active price
	if err := st.SaveModelPrices(ctx, map[string]store.ModelPrice{
		"my-model": {Prompt: 2.0, Completion: 4.0},
	}); err != nil {
		t.Fatal(err)
	}

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"my-model": {"input_cost_per_token": 0.000001, "output_cost_per_token": 0.000002}
		}`))
	}))
	t.Cleanup(source.Close)
	syncURL := source.URL

	svc := New(st, &syncURL)
	_, _ = svc.Sync(ctx, SyncRequest{Models: []string{"my-model"}})

	candidates, _, _ := svc.ListCandidates(ctx, modelpricerepo.CandidateFilter{Model: "my-model"})
	if len(candidates) != 1 {
		t.Fatalf("expected candidate, got %d", len(candidates))
	}
	candID := candidates[0].ID

	// 1. Approve candidate
	approved, errApprove := svc.ApproveCandidate(ctx, candID, "admin-reviewer", "approved lower price")
	if errApprove != nil {
		t.Fatalf("approve failed: %v", errApprove)
	}
	if approved.Status != model.CandidateStatusApproved {
		t.Fatalf("status not approved: %s", approved.Status)
	}

	// 2. Active price must now reflect the approved price (Prompt 1.0, Completion 2.0)
	activePrices, _ := st.LoadModelPrices(ctx)
	if activePrices["my-model"].Prompt != 1.0 || activePrices["my-model"].Completion != 2.0 {
		t.Fatalf("active price not updated after approval: %#v", activePrices["my-model"])
	}

	// 3. Verify price snapshot was generated (B3)
	snapshots, totalSnapshots, errSnap := svc.ListSnapshots(ctx, "", "my-model", 10, 0)
	if errSnap != nil || totalSnapshots != 1 || len(snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got total=%d, err=%v", totalSnapshots, errSnap)
	}
	if snapshots[0].PromptPer1M != 1.0 || snapshots[0].CreatedBy != "admin-reviewer" {
		t.Fatalf("unexpected snapshot content: %#v", snapshots[0])
	}

	// 4. Verify audit log entry was written
	audits, totalAudits, errAudit := svc.ListAuditEvents(ctx, "", "my-model", 10, 0)
	if errAudit != nil || totalAudits == 0 || len(audits) == 0 {
		t.Fatalf("expected audit log entries, got total=%d, err=%v", totalAudits, errAudit)
	}
	foundApprove := false
	for _, a := range audits {
		if a.Action == "approve" && a.Operator == "admin-reviewer" {
			foundApprove = true
			break
		}
	}
	if !foundApprove {
		t.Fatalf("approve action not found in audit events: %#v", audits)
	}

	// 5. Approval idempotency: calling ApproveCandidate again on approved candidate succeeds
	_, errApproveDup := svc.ApproveCandidate(ctx, candID, "admin-reviewer", "re-approve")
	if errApproveDup != nil {
		t.Fatalf("idempotent approve failed: %v", errApproveDup)
	}
}
