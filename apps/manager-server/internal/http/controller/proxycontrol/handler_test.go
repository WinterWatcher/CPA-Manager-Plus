package proxycontrol

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	adminauthsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/adminauth"
	proxycontrolsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proxycontrol"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

func newTestHandler(t testing.TB) (*Handler, string) {
	t.Helper()
	cfg := testutil.NewConfig(t)
	st := testutil.NewStore(t, cfg)
	mockControl := proxycontrolsvc.NewMockProxyControl()

	appCtx := &app.Context{
		Config:              cfg,
		AdminAuthService:    adminauthsvc.New(cfg, st),
		ProxyControlService: mockControl,
	}

	handler := &Handler{
		App:     appCtx,
		Service: mockControl,
	}
	return handler, "Bearer " + testutil.AdminKey
}

func TestHandlerUnauthorized(t *testing.T) {
	h, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/v0/management/proxy/status", nil)
	rec := httptest.NewRecorder()
	h.Handle(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", rec.Code)
	}
}

func TestHandlerStatusAndEgressSanitization(t *testing.T) {
	h, auth := newTestHandler(t)

	for _, path := range []string{"/v0/management/proxy/status", "/api/proxy/status"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", auth)
		rec := httptest.NewRecorder()
		h.Handle(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("path %s returned status %d", path, rec.Code)
		}

		var status model.ProxyStatus
		if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
			t.Fatalf("decode status: %v", err)
		}

		if !status.Enabled || !status.ControllerAvailable {
			t.Fatalf("expected enabled and available: %#v", status)
		}
		if status.CurrentNode == nil || status.CurrentNode.ID != "node-7f3a" {
			t.Fatalf("unexpected current node: %#v", status.CurrentNode)
		}
		if status.Egress == nil || status.Egress.IP != nil {
			t.Fatalf("egress IP should be sanitized to nil, got %v", status.Egress.IP)
		}
		if status.Egress.IPDisplay != "JP egress verified" {
			t.Fatalf("expected IPDisplay JP egress verified, got %s", status.Egress.IPDisplay)
		}
		if len(status.CheckStages) != 6 {
			t.Fatalf("expected 6 check stages, got %d", len(status.CheckStages))
		}

		// Security audit: response body must not leak sensitive tokens or urls
		body := rec.Body.String()
		for _, forbidden := range []string{"NODE_API_TOKEN", "vmess://", "vless://", "10.0.", "192.168."} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("forbidden string %q found in response: %s", forbidden, body)
			}
		}
	}
}

func TestHandlerNodesListAndFilters(t *testing.T) {
	h, auth := newTestHandler(t)

	// Query with country=JP and sort=latency
	req := httptest.NewRequest(http.MethodGet, "/v0/management/proxy/nodes?country=JP&sort=latency", nil)
	req.Header.Set("Authorization", auth)
	rec := httptest.NewRecorder()
	h.Handle(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}

	var list model.NodeList
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if list.Total != 4 {
		t.Fatalf("expected 4 JP nodes, got %d", list.Total)
	}
	if list.Items[0].LatencyMS > list.Items[1].LatencyMS {
		t.Fatalf("nodes not sorted by latency: %#v", list.Items)
	}
}

func TestHandlerPolicyGetAndPut(t *testing.T) {
	h, auth := newTestHandler(t)

	reqGet := httptest.NewRequest(http.MethodGet, "/v0/management/proxy/policy", nil)
	reqGet.Header.Set("Authorization", auth)
	recGet := httptest.NewRecorder()
	h.Handle(recGet, reqGet)

	var policy model.ProxyPolicy
	_ = json.NewDecoder(recGet.Body).Decode(&policy)
	if policy.AutoSwitch {
		t.Fatal("auto_switch must be false by default")
	}

	// Update with invalid country
	invalidBody, _ := json.Marshal(model.ProxyPolicy{
		AllowedCountries: []string{"TOOLONG"},
	})
	reqPutBad := httptest.NewRequest(http.MethodPut, "/v0/management/proxy/policy", bytes.NewReader(invalidBody))
	reqPutBad.Header.Set("Authorization", auth)
	recPutBad := httptest.NewRecorder()
	h.Handle(recPutBad, reqPutBad)
	if recPutBad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d", recPutBad.Code)
	}

	// Update with valid policy
	policy.CooldownSeconds = 450
	policy.ProbeConcurrency = 6
	validBody, _ := json.Marshal(policy)
	reqPutGood := httptest.NewRequest(http.MethodPut, "/v0/management/proxy/policy", bytes.NewReader(validBody))
	reqPutGood.Header.Set("Authorization", auth)
	recPutGood := httptest.NewRecorder()
	h.Handle(recPutGood, reqPutGood)
	if recPutGood.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", recPutGood.Code)
	}
}

func TestHandlerHealthCheckAndDryRun(t *testing.T) {
	h, auth := newTestHandler(t)

	// Health check (POST)
	hcReq := httptest.NewRequest(http.MethodPost, "/v0/management/proxy/health-check", bytes.NewReader([]byte(`{"dry_run":true}`)))
	hcReq.Header.Set("Authorization", auth)
	hcRec := httptest.NewRecorder()
	h.Handle(hcRec, hcReq)

	if hcRec.Code != http.StatusOK {
		t.Fatalf("health-check returned %d", hcRec.Code)
	}
	var hcRes model.HealthCheckResult
	_ = json.NewDecoder(hcRec.Body).Decode(&hcRes)
	if !hcRes.Healthy || len(hcRes.Stages) != 6 {
		t.Fatalf("health-check result: %#v", hcRes)
	}

	// Dry run select
	dryReq := httptest.NewRequest(http.MethodPost, "/v0/management/proxy/select/dry-run", bytes.NewReader([]byte(`{"reason":"test"}`)))
	dryReq.Header.Set("Authorization", auth)
	dryRec := httptest.NewRecorder()
	h.Handle(dryRec, dryReq)

	if dryRec.Code != http.StatusOK {
		t.Fatalf("dry-run returned %d", dryRec.Code)
	}
	var dryRes model.SelectResult
	_ = json.NewDecoder(dryRec.Body).Decode(&dryRes)
	if !dryRes.Success || !dryRes.WouldSwitch || dryRes.RecommendedNode == nil {
		t.Fatalf("dry-run result: %#v", dryRes)
	}
}

func TestHandlerSelectAndRollback(t *testing.T) {
	h, auth := newTestHandler(t)

	// Missing confirm flag returns 400
	reqNoConfirm := httptest.NewRequest(http.MethodPost, "/v0/management/proxy/select", bytes.NewReader([]byte(`{"node_id":"node-7f31","confirm":false}`)))
	reqNoConfirm.Header.Set("Authorization", auth)
	recNoConfirm := httptest.NewRecorder()
	h.Handle(recNoConfirm, reqNoConfirm)
	if recNoConfirm.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d", recNoConfirm.Code)
	}

	// Confirmed switch to node-7f31
	reqConfirm := httptest.NewRequest(http.MethodPost, "/v0/management/proxy/select", bytes.NewReader([]byte(`{"node_id":"node-7f31","confirm":true,"reason":"admin_override"}`)))
	reqConfirm.Header.Set("Authorization", auth)
	recConfirm := httptest.NewRecorder()
	h.Handle(recConfirm, reqConfirm)
	if recConfirm.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", recConfirm.Code)
	}
	var selRes model.SelectResult
	_ = json.NewDecoder(recConfirm.Body).Decode(&selRes)
	if !selRes.Success || selRes.CurrentNode.ID != "node-7f31" || selRes.RollbackID == "" {
		t.Fatalf("unexpected select result: %#v", selRes)
	}

	// Rollback without confirm returns 400
	reqRbBad := httptest.NewRequest(http.MethodPost, "/v0/management/proxy/rollback", bytes.NewReader([]byte(`{"rollback_id":"`+selRes.RollbackID+`","confirm":false}`)))
	reqRbBad.Header.Set("Authorization", auth)
	recRbBad := httptest.NewRecorder()
	h.Handle(recRbBad, reqRbBad)
	if recRbBad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d", recRbBad.Code)
	}

	// Confirmed rollback
	reqRbGood := httptest.NewRequest(http.MethodPost, "/v0/management/proxy/rollback", bytes.NewReader([]byte(`{"rollback_id":"`+selRes.RollbackID+`","confirm":true}`)))
	reqRbGood.Header.Set("Authorization", auth)
	recRbGood := httptest.NewRecorder()
	h.Handle(recRbGood, reqRbGood)
	if recRbGood.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", recRbGood.Code)
	}

	// Check status: restored to node-7f3a
	statusReq := httptest.NewRequest(http.MethodGet, "/v0/management/proxy/status", nil)
	statusReq.Header.Set("Authorization", auth)
	statusRec := httptest.NewRecorder()
	h.Handle(statusRec, statusReq)
	var status model.ProxyStatus
	_ = json.NewDecoder(statusRec.Body).Decode(&status)
	if status.CurrentNode.ID != "node-7f3a" {
		t.Fatalf("expected rollback to node-7f3a, got %s", status.CurrentNode.ID)
	}

	// Check history: should have audit entries
	histReq := httptest.NewRequest(http.MethodGet, "/v0/management/proxy/history", nil)
	histReq.Header.Set("Authorization", auth)
	histRec := httptest.NewRecorder()
	h.Handle(histRec, histReq)
	if histRec.Code != http.StatusOK {
		t.Fatalf("history returned %d", histRec.Code)
	}
	var histPage model.ProxyHistoryPage
	_ = json.NewDecoder(histRec.Body).Decode(&histPage)
	if histPage.Total == 0 {
		t.Fatal("expected audit history entries")
	}
}
