package modelprice

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	modelpricerepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/modelprice"
	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

func setupTestStoreForPolicy(t *testing.T) (*store.Store, *Service, context.Context) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test_policy.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := sqliterepo.Migrate(db); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	st := store.New(db)
	svc := New(st, nil)
	ctx := context.Background()
	return st, svc, ctx
}

func TestPolicyExport_OnlyApprovedAndUnfrozenCanExport(t *testing.T) {
	st, svc, ctx := setupTestStoreForPolicy(t)

	// 1. Unpriced model cannot be exported
	_, err := svc.ExportApprovedPolicySnapshot(ctx, "openai", "gpt-4o", "admin")
	if err == nil {
		t.Fatal("expected error exporting unpriced model")
	}

	// 2. Set an active price
	prices := map[string]store.ModelPrice{
		"gpt-4o": {
			Provider:   "openai",
			Prompt:     2.5,
			Completion: 10.0,
			Cache:      1.25,
			Source:     "manual",
		},
	}
	if err := st.ModelPrices.ReplaceAll(ctx, prices); err != nil {
		t.Fatalf("failed to set active price: %v", err)
	}

	// 3. Freeze model -> export should fail
	if err := svc.FreezeModel(ctx, "openai", "gpt-4o", "admin", "testing freeze"); err != nil {
		t.Fatalf("failed to freeze model: %v", err)
	}
	_, err = svc.ExportApprovedPolicySnapshot(ctx, "openai", "gpt-4o", "admin")
	if err == nil || !strings.Contains(err.Error(), "frozen") {
		t.Fatalf("expected frozen error, got: %v", err)
	}

	// 4. Unfreeze model -> export succeeds
	if err := svc.UnfreezeModel(ctx, "openai", "gpt-4o", "admin", "unfreeze for test"); err != nil {
		t.Fatalf("failed to unfreeze model: %v", err)
	}
	snap, err := svc.ExportApprovedPolicySnapshot(ctx, "openai", "gpt-4o", "admin")
	if err != nil {
		t.Fatalf("expected export to succeed, got: %v", err)
	}
	if snap.Provider != "openai" || snap.PublicModel != "gpt-4o" {
		t.Fatalf("unexpected snapshot provider/model: %s/%s", snap.Provider, snap.PublicModel)
	}
	if snap.PromptPer1M != 2.5 || snap.CompletionPer1M != 10.0 {
		t.Fatalf("unexpected prices in snapshot: %+v", snap)
	}
	if snap.ContentHash == "" {
		t.Fatal("expected non-empty content_hash")
	}
}

func TestPolicyExport_IdempotentExport(t *testing.T) {
	st, svc, ctx := setupTestStoreForPolicy(t)

	prices := map[string]store.ModelPrice{
		"claude-3-haiku": {
			Provider:   "anthropic",
			Prompt:     0.25,
			Completion: 1.25,
			Source:     "manual",
		},
	}
	if err := st.ModelPrices.ReplaceAll(ctx, prices); err != nil {
		t.Fatalf("failed to set active price: %v", err)
	}

	snap1, err := svc.ExportApprovedPolicySnapshot(ctx, "anthropic", "claude-3-haiku", "admin")
	if err != nil {
		t.Fatalf("first export failed: %v", err)
	}

	snap2, err := svc.ExportApprovedPolicySnapshot(ctx, "anthropic", "claude-3-haiku", "admin")
	if err != nil {
		t.Fatalf("second export failed: %v", err)
	}

	if snap1.PolicyVersion != snap2.PolicyVersion || snap1.ContentHash != snap2.ContentHash {
		t.Fatalf("expected idempotent export: %s vs %s", snap1.PolicyVersion, snap2.PolicyVersion)
	}
}

func TestPolicyExport_NewVersionRetainsHistory(t *testing.T) {
	st, svc, ctx := setupTestStoreForPolicy(t)

	// Version 1
	if err := st.ModelPrices.ReplaceAll(ctx, map[string]store.ModelPrice{
		"test-model": {Provider: "test-prov", Prompt: 1.0, Completion: 2.0, Source: "manual"},
	}); err != nil {
		t.Fatal(err)
	}
	snap1, err := svc.ExportApprovedPolicySnapshot(ctx, "test-prov", "test-model", "admin")
	if err != nil {
		t.Fatal(err)
	}

	// Update active price -> Version 2
	if err := st.ModelPrices.ReplaceAll(ctx, map[string]store.ModelPrice{
		"test-model": {Provider: "test-prov", Prompt: 1.5, Completion: 3.0, Source: "manual"},
	}); err != nil {
		t.Fatal(err)
	}
	snap2, err := svc.ExportApprovedPolicySnapshot(ctx, "test-prov", "test-model", "admin")
	if err != nil {
		t.Fatal(err)
	}

	if snap1.PolicyVersion == snap2.PolicyVersion {
		t.Fatalf("expected different versions: %s == %s", snap1.PolicyVersion, snap2.PolicyVersion)
	}

	// Verify history has both policies
	policies, total, err := svc.ListApprovedPolicySnapshots(ctx, modelpricerepo.PolicyFilter{
		Provider: "test-prov",
		Model:    "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	if total < 2 || len(policies) < 2 {
		t.Fatalf("expected at least 2 historical policies, got total=%d, len=%d", total, len(policies))
	}
}

func TestPolicyExport_RollbackAndRevoke(t *testing.T) {
	st, svc, ctx := setupTestStoreForPolicy(t)

	// Version 1
	_ = st.ModelPrices.ReplaceAll(ctx, map[string]store.ModelPrice{
		"roll-model": {Provider: "roll-prov", Prompt: 1.0, Completion: 2.0, Source: "manual"},
	})
	snap1, _ := svc.ExportApprovedPolicySnapshot(ctx, "roll-prov", "roll-model", "admin")

	// Version 2
	_ = st.ModelPrices.ReplaceAll(ctx, map[string]store.ModelPrice{
		"roll-model": {Provider: "roll-prov", Prompt: 3.0, Completion: 6.0, Source: "manual"},
	})
	_, _ = svc.ExportApprovedPolicySnapshot(ctx, "roll-prov", "roll-model", "admin")

	// Get policy 2 ID
	p2, _, err := svc.GetPolicySnapshot(ctx, 2)
	if err != nil {
		t.Fatalf("failed to get policy 2: %v", err)
	}

	// Rollback policy 2
	rolledSnap, err := svc.RollbackPolicySnapshot(ctx, p2.ID, "admin", "rolling back due to price spike")
	if err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	if rolledSnap.PromptPer1M != snap1.PromptPer1M {
		t.Fatalf("expected rolled back price to match snap1: got %v want %v", rolledSnap.PromptPer1M, snap1.PromptPer1M)
	}

	// Check audit events for rollback
	events, _, err := svc.ListAuditEvents(ctx, "roll-prov", "roll-model", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	foundRollback := false
	for _, e := range events {
		if e.Action == "rollback_policy" {
			foundRollback = true
			break
		}
	}
	if !foundRollback {
		t.Fatal("expected audit event for rollback_policy")
	}

	// Test Revoke on the rolled snap
	pol, _, _ := svc.GetPolicySnapshot(ctx, 3)
	if err := svc.RevokePolicySnapshot(ctx, pol.ID, "admin", "revoking policy"); err != nil {
		t.Fatalf("revoke failed: %v", err)
	}
	polRevoked, _, _ := svc.GetPolicySnapshot(ctx, pol.ID)
	if polRevoked.Status != "revoked" {
		t.Fatalf("expected policy status to be revoked, got %s", polRevoked.Status)
	}
}
