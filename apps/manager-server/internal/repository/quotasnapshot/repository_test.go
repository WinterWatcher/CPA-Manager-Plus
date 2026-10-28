package quotasnapshot_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	quotasnapshot "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/quotasnapshot"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

func TestListCandidatesKeepsLatestEvidenceForEveryActiveWindow(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	const (
		windowCount = 251
		historySize = 8
	)
	scopeFingerprint := quotasnapshot.ScopeFingerprint("all", "", nil)
	writes := make([]model.AccountQuotaObservationWrite, 0, windowCount*historySize)
	for windowIndex := 0; windowIndex < windowCount; windowIndex++ {
		providerWindowID := fmt.Sprintf("window-%03d", windowIndex)
		for historyIndex := 0; historyIndex < historySize; historyIndex++ {
			observedAtMS := int64(windowIndex*100 + historyIndex + 1)
			observationHash := fmt.Sprintf("observation-%03d-%d", windowIndex, historyIndex)
			writes = append(writes, model.AccountQuotaObservationWrite{
				Observation: model.AccountQuotaObservation{
					ObservationHash:     observationHash,
					AccountKey:          "account-1",
					Provider:            "antigravity",
					Source:              "api_query",
					SourceObservationID: observationHash,
					InventoryScopeKey:   "antigravity:quota-windows",
					InventoryMode:       "partial",
					ObservedAtMS:        observedAtMS,
					WindowCount:         1,
					CreatedAtMS:         observedAtMS,
				},
				Snapshots: []model.AccountQuotaSnapshot{{
					AccountKey:          "account-1",
					Provider:            "antigravity",
					ProviderWindowID:    providerWindowID,
					WindowKind:          "model_quota",
					WindowMode:          "unknown",
					ModelScopeKind:      "all",
					ScopeFingerprint:    scopeFingerprint,
					ContentHash:         fmt.Sprintf("content-%03d-%d", windowIndex, historyIndex),
					InventoryScopeKey:   "antigravity:quota-windows",
					Source:              "api_query",
					SourceObservationID: observationHash,
					ObservedAtMS:        observedAtMS,
					BoundaryAccuracy:    "unknown",
					CreatedAtMS:         observedAtMS,
				}},
			})
		}
	}
	if err := st.QuotaSnapshots.InsertObservationWrites(context.Background(), writes); err != nil {
		t.Fatalf("insert quota evidence: %v", err)
	}

	candidates, err := st.QuotaSnapshots.ListCandidates(
		context.Background(),
		"account-1",
		"antigravity",
		2000,
	)
	if err != nil {
		t.Fatalf("list quota candidates: %v", err)
	}
	windows := make(map[string]struct{}, windowCount)
	for _, candidate := range candidates {
		windows[candidate.ProviderWindowID] = struct{}{}
	}
	if len(windows) != windowCount {
		t.Fatalf("candidate windows = %d, want %d", len(windows), windowCount)
	}
}

func TestPartialObservationRefreshPreservesExistingWindows(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	scopeFingerprint := quotasnapshot.ScopeFingerprint("all", "", nil)

	// Step 1: Write initial 4 windows (Claude 5h/weekly, Gemini 5h/weekly)
	initialWindows := []string{"claude-5h", "claude-weekly", "gemini-5h", "gemini-weekly"}
	initialSnapshots := make([]model.AccountQuotaSnapshot, 0, len(initialWindows))
	for _, wid := range initialWindows {
		initialSnapshots = append(initialSnapshots, model.AccountQuotaSnapshot{
			AccountKey:          "account-ag-1",
			Provider:            "antigravity",
			ProviderWindowID:    wid,
			WindowKind:          "model_quota",
			WindowMode:          "fixed",
			ModelScopeKind:      "all",
			ScopeFingerprint:    scopeFingerprint,
			ContentHash:         "hash-" + wid,
			InventoryScopeKey:   "antigravity:quota-windows",
			Source:              "api_query",
			SourceObservationID: "obs-1",
			ObservedAtMS:        1000,
			BoundaryAccuracy:    "exact",
			CreatedAtMS:         1000,
		})
	}
	if err := st.QuotaSnapshots.InsertObservationWrites(ctx, []model.AccountQuotaObservationWrite{
		{
			Observation: model.AccountQuotaObservation{
				ObservationHash:     "obs-1",
				AccountKey:          "account-ag-1",
				Provider:            "antigravity",
				Source:              "api_query",
				SourceObservationID: "obs-1",
				InventoryScopeKey:   "antigravity:quota-windows",
				InventoryMode:       "complete",
				ObservedAtMS:        1000,
				WindowCount:         len(initialSnapshots),
				CreatedAtMS:         1000,
			},
			Snapshots: initialSnapshots,
		},
	}); err != nil {
		t.Fatalf("insert initial windows: %v", err)
	}

	// Step 2: Partial refresh only returns 2 windows (Gemini 5h, Gemini weekly)
	partialWindows := []string{"gemini-5h", "gemini-weekly"}
	partialSnapshots := make([]model.AccountQuotaSnapshot, 0, len(partialWindows))
	for _, wid := range partialWindows {
		partialSnapshots = append(partialSnapshots, model.AccountQuotaSnapshot{
			AccountKey:          "account-ag-1",
			Provider:            "antigravity",
			ProviderWindowID:    wid,
			WindowKind:          "model_quota",
			WindowMode:          "fixed",
			ModelScopeKind:      "all",
			ScopeFingerprint:    scopeFingerprint,
			ContentHash:         "hash-updated-" + wid,
			InventoryScopeKey:   "antigravity:quota-windows",
			Source:              "api_query",
			SourceObservationID: "obs-2",
			ObservedAtMS:        2000,
			BoundaryAccuracy:    "exact",
			CreatedAtMS:         2000,
		})
	}
	if err := st.QuotaSnapshots.InsertObservationWrites(ctx, []model.AccountQuotaObservationWrite{
		{
			Observation: model.AccountQuotaObservation{
				ObservationHash:     "obs-2",
				AccountKey:          "account-ag-1",
				Provider:            "antigravity",
				Source:              "api_query",
				SourceObservationID: "obs-2",
				InventoryScopeKey:   "antigravity:quota-windows",
				InventoryMode:       "partial",
				ObservedAtMS:        2000,
				WindowCount:         len(partialSnapshots),
				CreatedAtMS:         2000,
			},
			Snapshots: partialSnapshots,
		},
	}); err != nil {
		t.Fatalf("insert partial refresh: %v", err)
	}

	// Step 3: Verify all 4 windows remain active
	candidates, err := st.QuotaSnapshots.ListCandidates(ctx, "account-ag-1", "antigravity", 100)
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	activeMap := make(map[string]bool)
	for _, c := range candidates {
		activeMap[c.ProviderWindowID] = true
	}
	for _, wid := range initialWindows {
		if !activeMap[wid] {
			t.Errorf("window %q was lost after partial refresh", wid)
		}
	}
}
