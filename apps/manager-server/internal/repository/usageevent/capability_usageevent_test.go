package usageevent_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	sqliterepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/sqlite"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/usageevent"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

func openTestUsageEventDB(t *testing.T) (*sql.DB, usageevent.Repository) {
	t.Helper()
	db, err := sqliterepo.Open(filepath.Join(t.TempDir(), "usageevent.sqlite"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, usageevent.New(db)
}

func TestUsageEventCapabilityPersistenceAndDeduplication(t *testing.T) {
	ctx := context.Background()
	_, repo := openTestUsageEventDB(t)

	// 1. Asr event with non-token quantity
	asrEvent, err := usage.NormalizeRaw([]byte(`{
		"timestamp": "2026-10-28T10:00:00Z",
		"model": "whisper-1",
		"capability": "asr",
		"billing_unit": "audio_seconds",
		"quantity": 42.5,
		"input_quantity": 42.5
	}`))
	if err != nil {
		t.Fatalf("NormalizeRaw asr: %v", err)
	}

	// 2. Legacy chat event
	chatEvent, err := usage.NormalizeRaw([]byte(`{
		"timestamp": "2026-10-28T10:00:01Z",
		"model": "gpt-4o",
		"input_tokens": 150,
		"output_tokens": 75
	}`))
	if err != nil {
		t.Fatalf("NormalizeRaw chat: %v", err)
	}

	res, err := repo.InsertBatch(ctx, []model.UsageEvent{asrEvent, chatEvent})
	if err != nil {
		t.Fatalf("InsertBatch failed: %v", err)
	}
	if res.Inserted != 2 || res.Skipped != 0 {
		t.Fatalf("expected 2 inserted, 0 skipped, got %#v", res)
	}

	// 3. Deduplication check: inserting identical hash must be skipped without duplicate billing
	resDup, err := repo.InsertBatch(ctx, []model.UsageEvent{asrEvent})
	if err != nil {
		t.Fatalf("InsertBatch duplicate: %v", err)
	}
	if resDup.Inserted != 0 || resDup.Skipped != 1 {
		t.Fatalf("expected 0 inserted, 1 skipped, got %#v", resDup)
	}

	// 4. Verify ListRecent loads capability and billing unit
	recent, err := repo.ListRecent(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecent failed: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("expected 2 recent events, got %d", len(recent))
	}

	var foundASR, foundChat bool
	for _, ev := range recent {
		if ev.Model == "whisper-1" {
			foundASR = true
			if ev.Capability != "asr" || ev.BillingUnit != "audio_seconds" || ev.Quantity != 42.5 || ev.InputQuantity != 42.5 {
				t.Fatalf("persisted ASR event fields mismatch: %#v", ev)
			}
		}
		if ev.Model == "gpt-4o" {
			foundChat = true
			if ev.Capability != "" || ev.BillingUnit != "" || ev.Quantity != 0 {
				t.Fatalf("persisted Chat event gained capability: %#v", ev)
			}
		}
	}
	if !foundASR || !foundChat {
		t.Fatalf("did not find expected events in ListRecent: %#v", recent)
	}
}
