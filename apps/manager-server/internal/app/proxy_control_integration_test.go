package app_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/httpapi"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
)

func TestProxyControlEndToEndLifecycle(t *testing.T) {
	cfg := testutil.NewConfig(t)
	st := testutil.NewStore(t, cfg)
	srv := httpapi.New(cfg, st, nil)

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	client := ts.Client()
	adminAuth := "Bearer " + testutil.AdminKey

	// 1. Initial Status check via both /v0/management/proxy/status and /api/proxy/status
	for _, path := range []string{"/v0/management/proxy/status", "/api/proxy/status"} {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.Header.Set("Authorization", adminAuth)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s error: %v", path, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, resp.StatusCode)
		}

		var status model.ProxyStatus
		if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
			t.Fatalf("decode status: %v", err)
		}
		_ = resp.Body.Close()

		if !status.Enabled || !status.ControllerAvailable {
			t.Fatalf("expected enabled and available: %#v", status)
		}
		if status.CurrentNode == nil || status.CurrentNode.ID != "node-7f3a" {
			t.Fatalf("expected current node node-7f3a, got %#v", status.CurrentNode)
		}
		if status.Egress == nil || status.Egress.IP != nil {
			t.Fatalf("egress IP must be sanitized to nil, got %v", status.Egress.IP)
		}
		if len(status.CheckStages) != 6 {
			t.Fatalf("expected 6 health check stages, got %d", len(status.CheckStages))
		}
	}

	// 2. Candidate Nodes list and filters
	reqNodes, _ := http.NewRequest(http.MethodGet, ts.URL+"/v0/management/proxy/nodes?country=JP&sort=latency", nil)
	reqNodes.Header.Set("Authorization", adminAuth)
	respNodes, err := client.Do(reqNodes)
	if err != nil {
		t.Fatalf("GET nodes error: %v", err)
	}
	var nodeList model.NodeList
	_ = json.NewDecoder(respNodes.Body).Decode(&nodeList)
	_ = respNodes.Body.Close()

	if nodeList.Total != 4 {
		t.Fatalf("expected 4 JP nodes, got %d", nodeList.Total)
	}
	if nodeList.Items[0].LatencyMS > nodeList.Items[1].LatencyMS {
		t.Fatalf("nodes not sorted by latency: %#v", nodeList.Items)
	}

	// 3. Policy Get & Put Validation
	badPolicyBytes, _ := json.Marshal(model.ProxyPolicy{
		AllowedCountries: []string{"TOOLONG"},
	})
	reqPutBad, _ := http.NewRequest(http.MethodPut, ts.URL+"/v0/management/proxy/policy", bytes.NewReader(badPolicyBytes))
	reqPutBad.Header.Set("Authorization", adminAuth)
	respPutBad, err := client.Do(reqPutBad)
	if err != nil {
		t.Fatalf("PUT bad policy error: %v", err)
	}
	if respPutBad.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad policy country, got %d", respPutBad.StatusCode)
	}
	_ = respPutBad.Body.Close()

	// Valid policy update
	validPolicy := model.DefaultProxyPolicy()
	validPolicy.CooldownSeconds = 450
	validPolicy.ProbeConcurrency = 6
	validPolicyBytes, _ := json.Marshal(validPolicy)
	reqPutGood, _ := http.NewRequest(http.MethodPut, ts.URL+"/v0/management/proxy/policy", bytes.NewReader(validPolicyBytes))
	reqPutGood.Header.Set("Authorization", adminAuth)
	respPutGood, err := client.Do(reqPutGood)
	if err != nil {
		t.Fatalf("PUT valid policy error: %v", err)
	}
	if respPutGood.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for valid policy, got %d", respPutGood.StatusCode)
	}
	_ = respPutGood.Body.Close()

	// 4. Health Check
	reqHC, _ := http.NewRequest(http.MethodPost, ts.URL+"/v0/management/proxy/health-check", bytes.NewReader([]byte(`{"dry_run":true}`)))
	reqHC.Header.Set("Authorization", adminAuth)
	respHC, err := client.Do(reqHC)
	if err != nil {
		t.Fatalf("POST health-check error: %v", err)
	}
	var hcResult model.HealthCheckResult
	_ = json.NewDecoder(respHC.Body).Decode(&hcResult)
	_ = respHC.Body.Close()
	if !hcResult.Healthy || len(hcResult.Stages) != 6 {
		t.Fatalf("expected healthy 6 stages, got %#v", hcResult)
	}

	// 5. Dry-run select: must compute recommendation WITHOUT changing live node
	reqDry, _ := http.NewRequest(http.MethodPost, ts.URL+"/v0/management/proxy/select/dry-run", bytes.NewReader([]byte(`{"reason":"integration_dry_run"}`)))
	reqDry.Header.Set("Authorization", adminAuth)
	respDry, err := client.Do(reqDry)
	if err != nil {
		t.Fatalf("POST dry-run error: %v", err)
	}
	var dryResult model.SelectResult
	_ = json.NewDecoder(respDry.Body).Decode(&dryResult)
	_ = respDry.Body.Close()

	if !dryResult.Success || !dryResult.WouldSwitch || dryResult.RecommendedNode == nil {
		t.Fatalf("unexpected dry-run result: %#v", dryResult)
	}
	if dryResult.RecommendedNode.ID != "node-7f31" {
		t.Fatalf("expected recommended node-7f31 (145ms), got %s", dryResult.RecommendedNode.ID)
	}

	// Verify current node is still node-7f3a
	reqStatusVerify, _ := http.NewRequest(http.MethodGet, ts.URL+"/v0/management/proxy/status", nil)
	reqStatusVerify.Header.Set("Authorization", adminAuth)
	respStatusVerify, _ := client.Do(reqStatusVerify)
	var statusAfterDry model.ProxyStatus
	_ = json.NewDecoder(respStatusVerify.Body).Decode(&statusAfterDry)
	_ = respStatusVerify.Body.Close()
	if statusAfterDry.CurrentNode.ID != "node-7f3a" {
		t.Fatalf("dry-run mutated current node to %s", statusAfterDry.CurrentNode.ID)
	}

	// 6. Confirmed Select: switch to node-7f31
	reqSelectNoConfirm, _ := http.NewRequest(http.MethodPost, ts.URL+"/v0/management/proxy/select", bytes.NewReader([]byte(`{"node_id":"node-7f31","confirm":false}`)))
	reqSelectNoConfirm.Header.Set("Authorization", adminAuth)
	respSelectNoConfirm, _ := client.Do(reqSelectNoConfirm)
	if respSelectNoConfirm.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for unconfirmed select, got %d", respSelectNoConfirm.StatusCode)
	}
	_ = respSelectNoConfirm.Body.Close()

	reqSelectGood, _ := http.NewRequest(http.MethodPost, ts.URL+"/v0/management/proxy/select", bytes.NewReader([]byte(`{"node_id":"node-7f31","confirm":true,"reason":"admin_migration"}`)))
	reqSelectGood.Header.Set("Authorization", adminAuth)
	respSelectGood, err := client.Do(reqSelectGood)
	if err != nil {
		t.Fatalf("POST select error: %v", err)
	}
	if respSelectGood.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for confirmed select, got %d", respSelectGood.StatusCode)
	}
	var selectRes model.SelectResult
	_ = json.NewDecoder(respSelectGood.Body).Decode(&selectRes)
	_ = respSelectGood.Body.Close()

	if !selectRes.Success || selectRes.CurrentNode.ID != "node-7f31" || selectRes.RollbackID == "" {
		t.Fatalf("unexpected select result: %#v", selectRes)
	}

	// Verify status now shows node-7f31
	reqStatusAfterSelect, _ := http.NewRequest(http.MethodGet, ts.URL+"/v0/management/proxy/status", nil)
	reqStatusAfterSelect.Header.Set("Authorization", adminAuth)
	respStatusAfterSelect, _ := client.Do(reqStatusAfterSelect)
	var statusSwitched model.ProxyStatus
	_ = json.NewDecoder(respStatusAfterSelect.Body).Decode(&statusSwitched)
	_ = respStatusAfterSelect.Body.Close()
	if statusSwitched.CurrentNode.ID != "node-7f31" {
		t.Fatalf("expected current node node-7f31, got %s", statusSwitched.CurrentNode.ID)
	}

	// 7. Confirmed Rollback: restores node-7f3a
	reqRollback, _ := http.NewRequest(http.MethodPost, ts.URL+"/v0/management/proxy/rollback", bytes.NewReader([]byte(`{"rollback_id":"`+selectRes.RollbackID+`","confirm":true}`)))
	reqRollback.Header.Set("Authorization", adminAuth)
	respRollback, err := client.Do(reqRollback)
	if err != nil {
		t.Fatalf("POST rollback error: %v", err)
	}
	if respRollback.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for rollback, got %d", respRollback.StatusCode)
	}
	_ = respRollback.Body.Close()

	// Verify status restored
	reqStatusAfterRollback, _ := http.NewRequest(http.MethodGet, ts.URL+"/v0/management/proxy/status", nil)
	reqStatusAfterRollback.Header.Set("Authorization", adminAuth)
	respStatusAfterRollback, _ := client.Do(reqStatusAfterRollback)
	var statusRestored model.ProxyStatus
	_ = json.NewDecoder(respStatusAfterRollback.Body).Decode(&statusRestored)
	_ = respStatusAfterRollback.Body.Close()
	if statusRestored.CurrentNode.ID != "node-7f3a" {
		t.Fatalf("expected restored node node-7f3a, got %s", statusRestored.CurrentNode.ID)
	}

	// 8. History Audit Check
	reqHist, _ := http.NewRequest(http.MethodGet, ts.URL+"/v0/management/proxy/history", nil)
	reqHist.Header.Set("Authorization", adminAuth)
	respHist, err := client.Do(reqHist)
	if err != nil {
		t.Fatalf("GET history error: %v", err)
	}
	var histPage model.ProxyHistoryPage
	_ = json.NewDecoder(respHist.Body).Decode(&histPage)
	_ = respHist.Body.Close()

	if histPage.Total < 4 {
		t.Fatalf("expected at least 4 history records, got %d", histPage.Total)
	}

	// Security audit on history response
	histRaw, _ := json.Marshal(histPage)
	histStr := string(histRaw)
	for _, forbidden := range []string{"NODE_API_TOKEN", "vmess://", "vless://", "10.0.", "192.168."} {
		if strings.Contains(histStr, forbidden) {
			t.Fatalf("forbidden string %q found in history response: %s", forbidden, histStr)
		}
	}
}
