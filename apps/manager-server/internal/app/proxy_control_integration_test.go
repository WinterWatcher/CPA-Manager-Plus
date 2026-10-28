package app_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
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
	if nodeList.Items[0].LatencyMS == nil || nodeList.Items[1].LatencyMS == nil || *nodeList.Items[0].LatencyMS > *nodeList.Items[1].LatencyMS {
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

func TestProductionDefaultFailsClosedWithoutMock(t *testing.T) {
	cfg := testutil.NewConfig(t)
	cfg.ProxyControl.Type = "" // explicitly empty (production default)
	st := testutil.NewStore(t, cfg)
	srv := httpapi.New(cfg, st, nil)

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	client := ts.Client()
	adminAuth := "Bearer " + testutil.AdminKey

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/proxy/status", nil)
	req.Header.Set("Authorization", adminAuth)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("status request: %v", err)
	}
	defer resp.Body.Close()

	var status model.ProxyStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}

	if status.ControllerAvailable {
		t.Fatal("production default must have controller_available=false")
	}
	if !status.ReadOnly {
		t.Fatal("production default must have read_only=true")
	}
	if status.CurrentNode != nil {
		t.Fatalf("production default must have current_node=nil, got %#v", status.CurrentNode)
	}

	// Verify nodes list is empty
	reqNodes, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/proxy/nodes", nil)
	reqNodes.Header.Set("Authorization", adminAuth)
	respNodes, err := client.Do(reqNodes)
	if err != nil {
		t.Fatalf("nodes request: %v", err)
	}
	defer respNodes.Body.Close()

	var nodeList model.NodeList
	if err := json.NewDecoder(respNodes.Body).Decode(&nodeList); err != nil {
		t.Fatalf("decode nodeList: %v", err)
	}
	if len(nodeList.Items) != 0 || nodeList.Total != 0 {
		t.Fatalf("expected 0 items in production default, got %d", nodeList.Total)
	}
}

func TestSkyVPNControllerIntegrationLifecycle(t *testing.T) {
	currentRaw := "JP-Tokyo-Residential-01"
	targetRaw := "JP-Tokyo-Residential-02"
	allRaw := []string{currentRaw, targetRaw, "SG-Singapore-01"}

	var mu sync.Mutex
	skyvpnCurrent := currentRaw
	putCalls := 0

	skyvpnHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		auth := r.Header.Get("Authorization")
		if auth != "Bearer skyvpn-secret-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		path := r.URL.Path
		switch {
		case r.Method == http.MethodGet && path == "/proxies":
			resp := map[string]any{
				"proxies": map[string]any{
					"select": map[string]any{
						"all":  allRaw,
						"now":  skyvpnCurrent,
						"type": "Selector",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)

		case r.Method == http.MethodPut && path == "/proxies/select":
			putCalls++
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if name, ok := body["name"]; ok && name != "" {
				skyvpnCurrent = name
			}
			w.WriteHeader(http.StatusNoContent)

		case r.Method == http.MethodGet && strings.HasPrefix(path, "/proxies/") && strings.HasSuffix(path, "/delay"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"delay": 125})

		default:
			http.NotFound(w, r)
		}
	})

	skyvpnServer := httptest.NewServer(skyvpnHandler)
	t.Cleanup(skyvpnServer.Close)

	cfg := testutil.NewConfig(t)
	cfg.ProxyControl = config.ProxyControlConfig{
		Type:           "skyvpn",
		BaseURL:        skyvpnServer.URL,
		Token:          "skyvpn-secret-token",
		TimeoutSeconds: 5,
		StateFile:      filepath.Join(t.TempDir(), "proxy-selection.json"),
	}
	st := testutil.NewStore(t, cfg)
	srv := httpapi.New(cfg, st, nil)

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	client := ts.Client()
	adminAuth := "Bearer " + testutil.AdminKey

	// 1. GET /api/proxy/status
	reqStatus, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/proxy/status", nil)
	reqStatus.Header.Set("Authorization", adminAuth)
	respStatus, err := client.Do(reqStatus)
	if err != nil {
		t.Fatalf("status request: %v", err)
	}
	var status model.ProxyStatus
	_ = json.NewDecoder(respStatus.Body).Decode(&status)
	_ = respStatus.Body.Close()

	if !status.ControllerAvailable || status.ReadOnly || status.CurrentNode == nil {
		t.Fatalf("expected available and current node, got %#v", status)
	}
	if !status.CurrentNode.Selected {
		t.Fatal("expected current node to be selected")
	}

	// 2. GET /api/proxy/nodes
	reqNodes, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/proxy/nodes", nil)
	reqNodes.Header.Set("Authorization", adminAuth)
	respNodes, err := client.Do(reqNodes)
	if err != nil {
		t.Fatalf("nodes request: %v", err)
	}
	var nodeList model.NodeList
	_ = json.NewDecoder(respNodes.Body).Decode(&nodeList)
	_ = respNodes.Body.Close()

	if nodeList.Total != 3 {
		t.Fatalf("expected 3 nodes, got %d", nodeList.Total)
	}

	var targetPublicID string
	for _, n := range nodeList.Items {
		if !n.Selected && n.Country == "JP" {
			targetPublicID = n.ID
			break
		}
	}
	if targetPublicID == "" {
		t.Fatal("target JP public node ID not found")
	}

	// 3. Dry-run select must not call SkyVPN PUT
	dryRunBody, _ := json.Marshal(model.SelectRequest{NodeID: targetPublicID, DryRun: true})
	reqDryRun, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/proxy/select/dry-run", bytes.NewReader(dryRunBody))
	reqDryRun.Header.Set("Authorization", adminAuth)
	respDryRun, err := client.Do(reqDryRun)
	if err != nil {
		t.Fatalf("dry run request: %v", err)
	}
	var dryRunResult model.SelectResult
	_ = json.NewDecoder(respDryRun.Body).Decode(&dryRunResult)
	_ = respDryRun.Body.Close()

	if !dryRunResult.Success || !dryRunResult.WouldSwitch {
		t.Fatalf("dry run should succeed with would_switch=true: %#v", dryRunResult)
	}
	mu.Lock()
	if putCalls != 0 {
		t.Fatalf("dry run must NOT call PUT /proxies/select, called %d times", putCalls)
	}
	mu.Unlock()

	// 4. Select target node with confirm=true
	selectBody, _ := json.Marshal(model.SelectRequest{NodeID: targetPublicID, Confirm: true})
	reqSelect, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/proxy/select", bytes.NewReader(selectBody))
	reqSelect.Header.Set("Authorization", adminAuth)
	respSelect, err := client.Do(reqSelect)
	if err != nil {
		t.Fatalf("select request: %v", err)
	}
	var selectResult model.SelectResult
	_ = json.NewDecoder(respSelect.Body).Decode(&selectResult)
	_ = respSelect.Body.Close()

	if !selectResult.Success || selectResult.RolledBack {
		t.Fatalf("select should succeed: %#v", selectResult)
	}
	if selectResult.CurrentNode == nil || selectResult.CurrentNode.ID != targetPublicID {
		t.Fatalf("current node after switch should be %s, got %#v", targetPublicID, selectResult.CurrentNode)
	}

	// Confirm SkyVPN server now has targetRaw
	mu.Lock()
	if skyvpnCurrent != targetRaw {
		t.Fatalf("skyvpn server current node expected %s, got %s", targetRaw, skyvpnCurrent)
	}
	if putCalls != 1 {
		t.Fatalf("expected exactly 1 PUT call, got %d", putCalls)
	}
	mu.Unlock()

	// Verify no token leak in responses
	statusJSON, _ := json.Marshal(status)
	if strings.Contains(string(statusJSON), "skyvpn-secret-token") {
		t.Fatal("token leaked in status response")
	}
}
