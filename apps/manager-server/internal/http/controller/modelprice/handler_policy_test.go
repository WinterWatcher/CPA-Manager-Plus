package modelprice

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	adminauthsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/adminauth"
	modelpricesvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/modelprice"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

func TestPolicyAPI_ExportListGetRevokeRollback(t *testing.T) {
	cfg := testutil.NewConfig(t)
	st := testutil.NewStore(t, cfg)
	ctx := context.Background()

	// 1. Seed active price
	_ = st.ModelPrices.ReplaceAll(ctx, map[string]store.ModelPrice{
		"gpt-4o-mini": {
			Provider:   "openai",
			Prompt:     0.15,
			Completion: 0.60,
			Cache:      0.075,
			Source:     "manual",
		},
	})

	handler := &Handler{App: &app.Context{
		Config:            cfg,
		AdminAuthService:  adminauthsvc.New(cfg, st),
		ModelPriceService: modelpricesvc.New(st, nil),
	}}

	// 2. Export Policy via POST /v0/management/model-prices/policies/export
	exportReqBody := `{"provider":"openai","model":"gpt-4o-mini"}`
	req := httptest.NewRequest(http.MethodPost, "/v0/management/model-prices/policies/export", bytes.NewBufferString(exportReqBody))
	req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.Handle(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("export policy failed, code: %d, body: %s", w.Code, w.Body.String())
	}
	var exportResp struct {
		Success  bool                          `json:"success"`
		Snapshot *modelpricesvc.PolicySnapshot `json:"snapshot"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &exportResp); err != nil {
		t.Fatalf("failed to decode export response: %v", err)
	}
	if !exportResp.Success || exportResp.Snapshot == nil {
		t.Fatalf("expected success and snapshot, got: %+v", exportResp)
	}
	if exportResp.Snapshot.PromptPer1M != 0.15 || exportResp.Snapshot.CompletionPer1M != 0.60 {
		t.Fatalf("unexpected prices in exported snapshot: %+v", exportResp.Snapshot)
	}

	// 3. List Policies via GET /v0/management/model-prices/policies
	req = httptest.NewRequest(http.MethodGet, "/v0/management/model-prices/policies?provider=openai", nil)
	req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
	w = httptest.NewRecorder()
	handler.Handle(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list policies failed, code: %d, body: %s", w.Code, w.Body.String())
	}
	var listResp struct {
		Policies []any `json:"policies"`
		Total    int64 `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("failed to decode list response: %v", err)
	}
	if listResp.Total != 1 || len(listResp.Policies) != 1 {
		t.Fatalf("expected 1 policy in list, got total=%d len=%d", listResp.Total, len(listResp.Policies))
	}

	// 4. Get Policy via GET /v0/management/model-prices/policies/1
	req = httptest.NewRequest(http.MethodGet, "/v0/management/model-prices/policies/1", nil)
	req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
	w = httptest.NewRecorder()
	handler.Handle(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("get policy failed, code: %d, body: %s", w.Code, w.Body.String())
	}

	// 5. Export Policy Direct via GET /v0/management/model-prices/policies/1/export
	req = httptest.NewRequest(http.MethodGet, "/v0/management/model-prices/policies/1/export", nil)
	req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
	w = httptest.NewRecorder()
	handler.Handle(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("get policy export failed, code: %d, body: %s", w.Code, w.Body.String())
	}
	var directSnap modelpricesvc.PolicySnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &directSnap); err != nil {
		t.Fatalf("failed to decode direct export snapshot: %v", err)
	}
	if directSnap.PublicModel != "gpt-4o-mini" {
		t.Fatalf("unexpected direct export model: %s", directSnap.PublicModel)
	}

	// 6. Update Price and Export Version 2
	_ = st.ModelPrices.ReplaceAll(ctx, map[string]store.ModelPrice{
		"gpt-4o-mini": {
			Provider:   "openai",
			Prompt:     0.20,
			Completion: 0.80,
			Source:     "manual",
		},
	})
	req = httptest.NewRequest(http.MethodPost, "/v0/management/model-prices/policies/export", bytes.NewBufferString(exportReqBody))
	req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
	w = httptest.NewRecorder()
	handler.Handle(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("export v2 failed: %s", w.Body.String())
	}

	// 7. Rollback Policy via POST /v0/management/model-prices/policies/2/rollback
	rollbackReqBody := `{"reason":"rollback to v1"}`
	req = httptest.NewRequest(http.MethodPost, "/v0/management/model-prices/policies/2/rollback", bytes.NewBufferString(rollbackReqBody))
	req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
	w = httptest.NewRecorder()
	handler.Handle(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("rollback failed, code: %d, body: %s", w.Code, w.Body.String())
	}

	// 8. Revoke Policy via POST /v0/management/model-prices/policies/3/revoke
	revokeReqBody := `{"reason":"deprecating policy"}`
	req = httptest.NewRequest(http.MethodPost, "/v0/management/model-prices/policies/3/revoke", bytes.NewBufferString(revokeReqBody))
	req.Header.Set("Authorization", "Bearer "+testutil.AdminKey)
	w = httptest.NewRecorder()
	handler.Handle(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("revoke failed, code: %d, body: %s", w.Code, w.Body.String())
	}
}
