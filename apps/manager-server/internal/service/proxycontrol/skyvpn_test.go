package proxycontrol_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/config"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proxycontrol"
)

type mockSkyVPNServer struct {
	mu           sync.Mutex
	currentNode  string
	allNodes     []string
	delayMap     map[string]int
	putCalls     int32
	lastPutBody  map[string]string
	authHeader   string
	forcePutFail bool
}

func newMockSkyVPNServer(initialCurrent string, allNodes []string) (*httptest.Server, *mockSkyVPNServer) {
	state := &mockSkyVPNServer{
		currentNode: initialCurrent,
		allNodes:    allNodes,
		delayMap:    make(map[string]int),
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		state.authHeader = r.Header.Get("Authorization")
		state.mu.Unlock()

		path := r.URL.Path

		switch {
		case r.Method == http.MethodGet && path == "/proxies":
			state.mu.Lock()
			defer state.mu.Unlock()
			resp := map[string]any{
				"proxies": map[string]any{
					"select": map[string]any{
						"all":  state.allNodes,
						"now":  state.currentNode,
						"type": "Selector",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)

		case r.Method == http.MethodPut && path == "/proxies/select":
			atomic.AddInt32(&state.putCalls, 1)
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.forcePutFail {
				http.Error(w, "internal failure", http.StatusInternalServerError)
				return
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			state.lastPutBody = body
			if target, ok := body["name"]; ok && target != "" {
				state.currentNode = target
			}
			w.WriteHeader(http.StatusNoContent)

		case r.Method == http.MethodGet && strings.HasPrefix(path, "/proxies/") && strings.HasSuffix(path, "/delay"):
			// Extract encoded node name between /proxies/ and /delay
			middle := strings.TrimPrefix(path, "/proxies/")
			encodedNode := strings.TrimSuffix(middle, "/delay")
			rawName, _ := url.PathUnescape(encodedNode)

			state.mu.Lock()
			delay, hasDelay := state.delayMap[rawName]
			state.mu.Unlock()

			if !hasDelay || delay <= 0 {
				http.Error(w, "timeout or unreachable", http.StatusGatewayTimeout)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"delay": delay,
			})

		default:
			http.NotFound(w, r)
		}
	})

	ts := httptest.NewServer(handler)
	return ts, state
}

func TestSkyVPNResponseParsingAndNodeSanitization(t *testing.T) {
	rawCurrent := "JP-Tokyo-Residential-01-SecretToken"
	rawNodes := []string{
		"JP-Tokyo-Residential-01-SecretToken",
		"SG-Singapore-01-192.168.1.100",
		"US-SiliconValley-HighSpeed-550e8400-e29b-41d4-a716-446655440000",
		"vmess://user:pass@1.2.3.4:8080#JP-Osaka-02",
		"🇯🇵 日本东京 [BGP] 03",
	}

	ts, _ := newMockSkyVPNServer(rawCurrent, rawNodes)
	defer ts.Close()

	secretToken := "secret-skyvpn-bearer-token-123456"
	pc := proxycontrol.NewSkyVPNProxyControl(proxycontrol.SkyVPNConfig{
		BaseURL:        ts.URL,
		Token:          secretToken,
		TimeoutSeconds: 5,
		DataKey:        "my-encryption-key",
	})

	ctx := context.Background()

	// 1. Status check
	status, err := pc.Status(ctx)
	if err != nil {
		t.Fatalf("Status error: %v", err)
	}
	if !status.ControllerAvailable || status.ReadOnly {
		t.Fatalf("expected controller_available=true, read_only=false, got %#v", status)
	}
	if status.CurrentNode == nil {
		t.Fatal("current_node must not be nil")
	}
	if !status.CurrentNode.Selected {
		t.Fatal("current_node must have selected=true")
	}

	// 2. Nodes check
	nodeList, err := pc.Nodes(ctx, model.NodeFilter{Page: 1, PageSize: 50})
	if err != nil {
		t.Fatalf("Nodes error: %v", err)
	}
	if nodeList.Total != 5 {
		t.Fatalf("expected 5 nodes, got %d", nodeList.Total)
	}

	// Check that public IDs are stable and formatted as node-xxxx
	idMap := make(map[string]bool)
	for _, n := range nodeList.Items {
		if !strings.HasPrefix(n.ID, "node-") {
			t.Errorf("expected ID prefix node-, got %s", n.ID)
		}
		if idMap[n.ID] {
			t.Errorf("duplicate node ID: %s", n.ID)
		}
		idMap[n.ID] = true

		// SECURITY VERIFICATION: No raw token, IP, UUID, or URI must leak into public fields
		serialized, _ := json.Marshal(n)
		str := string(serialized)
		if strings.Contains(str, secretToken) {
			t.Fatalf("SECRET TOKEN LEAKED in node JSON: %s", str)
		}
		if strings.Contains(str, "192.168.1.100") || strings.Contains(str, "1.2.3.4") {
			t.Fatalf("RAW IP LEAKED in node JSON: %s", str)
		}
		if strings.Contains(str, "550e8400-e29b") {
			t.Fatalf("RAW UUID LEAKED in node JSON: %s", str)
		}
		if strings.Contains(str, "vmess://") {
			t.Fatalf("RAW URI LEAKED in node JSON: %s", str)
		}
		if strings.Contains(str, "pass@") {
			t.Fatalf("RAW PASSWORD LEAKED in node JSON: %s", str)
		}
	}
}

func TestSkyVPNDryRunNeverCallsPutSelect(t *testing.T) {
	rawCurrent := "JP-Tokyo-01"
	rawNodes := []string{"JP-Tokyo-01", "JP-Tokyo-02", "SG-Singapore-01"}

	ts, state := newMockSkyVPNServer(rawCurrent, rawNodes)
	defer ts.Close()

	pc := proxycontrol.NewSkyVPNProxyControl(proxycontrol.SkyVPNConfig{
		BaseURL:        ts.URL,
		TimeoutSeconds: 5,
	})

	ctx := context.Background()

	// Dry-run recommendation
	res, err := pc.DryRunSelect(ctx, model.SelectRequest{DryRun: true})
	if err != nil {
		t.Fatalf("DryRunSelect error: %v", err)
	}
	if !res.Success {
		t.Fatalf("DryRunSelect failed: %#v", res)
	}

	// Verify PUT /proxies/select was NEVER called
	if calls := atomic.LoadInt32(&state.putCalls); calls != 0 {
		t.Fatalf("DryRunSelect must NEVER call PUT /proxies/select, but called %d times", calls)
	}
}

func TestSkyVPNSelectAndAutoRollbackOnHealthFailure(t *testing.T) {
	rawCurrent := "JP-Tokyo-01"
	rawTarget := "JP-Tokyo-02"
	rawNodes := []string{rawCurrent, rawTarget}

	ts, state := newMockSkyVPNServer(rawCurrent, rawNodes)
	defer ts.Close()

	tmpDir := t.TempDir()
	stateFile := filepath.Join(tmpDir, "proxy-selection.json")

	pc := proxycontrol.NewSkyVPNProxyControl(proxycontrol.SkyVPNConfig{
		BaseURL:        ts.URL,
		TimeoutSeconds: 5,
		StateFile:      stateFile,
	})

	ctx := context.Background()

	// 1. Get nodes to obtain target public ID
	nodeList, _ := pc.Nodes(ctx, model.NodeFilter{Page: 1, PageSize: 10})
	var targetID string
	for _, n := range nodeList.Items {
		if !n.Selected {
			targetID = n.ID
			break
		}
	}
	if targetID == "" {
		t.Fatal("target node ID not found")
	}

	// Case A: Post-switch health check FAILS (delayMap has no delay for target -> timeout/unreachable)
	// Must trigger automatic rollback!
	selectRes, err := pc.Select(ctx, model.SelectRequest{
		NodeID:  targetID,
		Confirm: true,
	})
	if err != nil {
		t.Fatalf("Select unexpected error: %v", err)
	}
	if selectRes.Success {
		t.Fatal("Select should have failed because post-switch probe failed")
	}
	if !selectRes.RolledBack {
		t.Fatal("Select must have rolled_back=true on probe failure")
	}
	if selectRes.Reason != model.ErrCodePostSwitchHealthFailed {
		t.Fatalf("expected reason post_switch_health_failed, got %s", selectRes.Reason)
	}

	// Verify server current node was restored to rawCurrent
	state.mu.Lock()
	serverCurrent := state.currentNode
	state.mu.Unlock()
	if serverCurrent != rawCurrent {
		t.Fatalf("server node should have been rolled back to %s, got %s", rawCurrent, serverCurrent)
	}

	// Case B: Configure target delay so health check succeeds
	state.mu.Lock()
	state.delayMap[rawTarget] = 135
	state.mu.Unlock()

	selectSuccess, err := pc.Select(ctx, model.SelectRequest{
		NodeID:  targetID,
		Confirm: true,
	})
	if err != nil {
		t.Fatalf("Select error: %v", err)
	}
	if !selectSuccess.Success {
		t.Fatalf("Select expected success, got %#v", selectSuccess)
	}
	if selectSuccess.RolledBack {
		t.Fatal("Select should not be rolled back on success")
	}
	if selectSuccess.CurrentNode == nil || selectSuccess.CurrentNode.ID != targetID {
		t.Fatalf("current node after switch should be %s, got %#v", targetID, selectSuccess.CurrentNode)
	}
	if selectSuccess.CurrentNode.LatencyMS == nil || *selectSuccess.CurrentNode.LatencyMS != 135 {
		t.Fatalf("latency_ms should be 135, got %v", selectSuccess.CurrentNode.LatencyMS)
	}

	// Case C: Idempotent switch to the already current node
	idempotentRes, err := pc.Select(ctx, model.SelectRequest{
		NodeID:  targetID,
		Confirm: true,
	})
	if err != nil {
		t.Fatalf("Idempotent select error: %v", err)
	}
	if !idempotentRes.Success || idempotentRes.WouldSwitch {
		t.Fatalf("Idempotent select should succeed with would_switch=false: %#v", idempotentRes)
	}
}

func TestSkyVPNManualRollback(t *testing.T) {
	rawCurrent := "JP-Tokyo-01"
	rawTarget := "JP-Tokyo-02"
	rawNodes := []string{rawCurrent, rawTarget}

	ts, state := newMockSkyVPNServer(rawCurrent, rawNodes)
	defer ts.Close()

	state.mu.Lock()
	state.delayMap[rawTarget] = 110
	state.mu.Unlock()

	pc := proxycontrol.NewSkyVPNProxyControl(proxycontrol.SkyVPNConfig{
		BaseURL:        ts.URL,
		TimeoutSeconds: 5,
		StateFile:      filepath.Join(t.TempDir(), "proxy-selection.json"),
	})

	ctx := context.Background()
	nodeList, _ := pc.Nodes(ctx, model.NodeFilter{Page: 1, PageSize: 10})
	var targetID string
	for _, n := range nodeList.Items {
		if !n.Selected {
			targetID = n.ID
			break
		}
	}

	// Perform successful switch
	res, err := pc.Select(ctx, model.SelectRequest{NodeID: targetID, Confirm: true})
	if err != nil || !res.Success {
		t.Fatalf("Select failed: %v, %#v", err, res)
	}
	if res.RollbackID == "" {
		t.Fatal("expected non-empty rollback ID")
	}

	// Trigger manual rollback using rollback ID
	if err := pc.Rollback(ctx, res.RollbackID); err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}

	state.mu.Lock()
	serverCurrent := state.currentNode
	state.mu.Unlock()
	if serverCurrent != rawCurrent {
		t.Fatalf("expected node restored to %s, got %s", rawCurrent, serverCurrent)
	}
}

func TestSkyVPNControllerUnavailableHandling(t *testing.T) {
	// Point to an invalid / unreachable address
	pc := proxycontrol.NewSkyVPNProxyControl(proxycontrol.SkyVPNConfig{
		BaseURL:        "http://127.0.0.1:59999",
		TimeoutSeconds: 1,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	status, err := pc.Status(ctx)
	if err != nil {
		t.Fatalf("Status should not return hard error, got %v", err)
	}
	if status.ControllerAvailable {
		t.Fatal("controller_available must be false when server unreachable")
	}
	if !status.ReadOnly {
		t.Fatal("read_only must be true when server unreachable")
	}
	if status.CurrentNode != nil {
		t.Fatalf("current_node must be nil when unreachable, got %#v", status.CurrentNode)
	}

	nodeList, err := pc.Nodes(ctx, model.NodeFilter{})
	if err != nil {
		t.Fatalf("Nodes should not return hard error, got %v", err)
	}
	if len(nodeList.Items) != 0 || nodeList.Total != 0 {
		t.Fatalf("expected empty nodes when unreachable, got %d", nodeList.Total)
	}

	// Mutating actions must fail with controller unavailable
	_, err = pc.Select(ctx, model.SelectRequest{NodeID: "node-1", Confirm: true})
	if !errors.Is(err, model.ErrControllerUnavailable) {
		t.Fatalf("expected ErrControllerUnavailable, got %v", err)
	}
}

func TestSkyVPNSpecialCharactersPathEscaping(t *testing.T) {
	specialNode := "JP - Tokyo (Special/Node) [Residential] 🇯🇵"
	ts, state := newMockSkyVPNServer(specialNode, []string{specialNode})
	defer ts.Close()

	state.mu.Lock()
	state.delayMap[specialNode] = 160
	state.mu.Unlock()

	pc := proxycontrol.NewSkyVPNProxyControl(proxycontrol.SkyVPNConfig{
		BaseURL:        ts.URL,
		TimeoutSeconds: 5,
	})

	ctx := context.Background()
	res, err := pc.HealthCheck(ctx, model.HealthCheckRequest{DryRun: true})
	if err != nil {
		t.Fatalf("HealthCheck error with special characters: %v", err)
	}
	if !res.Healthy || res.LatencyMS != 160 {
		t.Fatalf("HealthCheck expected healthy with 160ms, got %#v", res)
	}
}

func TestUnavailableProxyControlBehavior(t *testing.T) {
	upc := proxycontrol.NewUnavailableProxyControl()
	ctx := context.Background()

	status, _ := upc.Status(ctx)
	if status.ControllerAvailable || !status.ReadOnly || status.CurrentNode != nil || len(status.CheckStages) != 0 {
		t.Fatalf("invalid unavailable status: %#v", status)
	}

	list, _ := upc.Nodes(ctx, model.NodeFilter{})
	if len(list.Items) != 0 || list.Total != 0 {
		t.Fatalf("expected empty nodes from unavailable control, got %d", list.Total)
	}

	_, err := upc.Select(ctx, model.SelectRequest{NodeID: "node-1", Confirm: true})
	if !errors.Is(err, model.ErrControllerUnavailable) {
		t.Fatalf("expected ErrControllerUnavailable, got %v", err)
	}
}

func TestProductionDefaultDoesNotInstantiateMock(t *testing.T) {
	// When PROXY_CONTROLLER_TYPE is empty (default production behavior),
	// building proxy control must return UnavailableProxyControl, NOT MockProxyControl.
	cfg := config.Config{}
	if cfg.ProxyControl.Type != "" {
		t.Fatal("expected empty proxy control type by default")
	}
	pc := proxycontrol.NewUnavailableProxyControl()

	status, _ := pc.Status(context.Background())
	if status.ControllerAvailable {
		t.Fatal("production default must be controller_available=false")
	}
	if status.CurrentNode != nil {
		t.Fatal("production default must not contain mock current node")
	}
}
