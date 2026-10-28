package proxycontrol

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

func TestMockProxyControlStatusAndNodes(t *testing.T) {
	ctx := context.Background()
	pc := NewMockProxyControl()

	status, err := pc.Status(ctx)
	if err != nil {
		t.Fatalf("Status error: %v", err)
	}
	if !status.Enabled || !status.ControllerAvailable {
		t.Fatalf("unexpected status: %#v", status)
	}
	if status.CurrentNode == nil || status.CurrentNode.ID != "node-7f3a" {
		t.Fatalf("unexpected current node: %#v", status.CurrentNode)
	}
	if len(status.CheckStages) != 6 {
		t.Fatalf("expected 6 check stages, got %d", len(status.CheckStages))
	}

	// Filter by country JP
	list, err := pc.Nodes(ctx, model.NodeFilter{Country: "JP", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("Nodes error: %v", err)
	}
	if list.Total != 4 {
		t.Fatalf("expected 4 JP nodes, got %d", list.Total)
	}

	// Filter by health healthy
	healthyList, err := pc.Nodes(ctx, model.NodeFilter{Health: "healthy", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("Nodes error: %v", err)
	}
	if healthyList.Total != 3 {
		t.Fatalf("expected 3 healthy nodes, got %d", healthyList.Total)
	}

	// Sort by latency
	sortedList, err := pc.Nodes(ctx, model.NodeFilter{Sort: "latency", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("Nodes sort error: %v", err)
	}
	if sortedList.Items[0].LatencyMS == nil || sortedList.Items[1].LatencyMS == nil || *sortedList.Items[0].LatencyMS > *sortedList.Items[1].LatencyMS {
		t.Fatalf("nodes not sorted by latency ascending: %#v", sortedList.Items)
	}
}

func TestMockProxyControlPolicyUpdate(t *testing.T) {
	ctx := context.Background()
	pc := NewMockProxyControl()

	policy, err := pc.Policy(ctx)
	if err != nil {
		t.Fatalf("Policy error: %v", err)
	}
	if policy.AutoSwitch {
		t.Fatal("auto_switch must be false by default")
	}
	if !policy.DryRun {
		t.Fatal("dry_run must be true by default")
	}

	// Valid policy update
	policy.CooldownSeconds = 600
	policy.ProbeConcurrency = 8
	if err := pc.UpdatePolicy(ctx, policy); err != nil {
		t.Fatalf("UpdatePolicy valid failed: %v", err)
	}

	updated, _ := pc.Policy(ctx)
	if updated.CooldownSeconds != 600 || updated.ProbeConcurrency != 8 {
		t.Fatalf("policy was not updated: %#v", updated)
	}

	// Invalid policy update
	invalidPolicy := policy
	invalidPolicy.AllowedCountries = []string{"INVALID"}
	if err := pc.UpdatePolicy(ctx, invalidPolicy); !errors.Is(err, model.ErrInvalidCountryCode) {
		t.Fatalf("expected ErrInvalidCountryCode, got %v", err)
	}
}

func TestMockProxyControlDryRunSelect(t *testing.T) {
	ctx := context.Background()
	pc := NewMockProxyControl()

	// Current node is node-7f3a (182ms)
	res, err := pc.DryRunSelect(ctx, model.SelectRequest{
		Reason: "scheduled_check",
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("DryRunSelect error: %v", err)
	}
	if !res.Success {
		t.Fatalf("DryRunSelect should succeed: %#v", res)
	}
	if res.RecommendedNode == nil || res.RecommendedNode.ID != "node-7f31" {
		t.Fatalf("expected recommended node-7f31 (145ms), got %#v", res.RecommendedNode)
	}
	if !res.WouldSwitch {
		t.Fatal("would_switch should be true")
	}

	// Verify current node did not change
	status, _ := pc.Status(ctx)
	if status.CurrentNode.ID != "node-7f3a" {
		t.Fatalf("DryRun altered current node to %s", status.CurrentNode.ID)
	}
}

func TestMockProxyControlSelectAndRollback(t *testing.T) {
	ctx := context.Background()
	pc := NewMockProxyControl()

	// Select without confirmation must fail
	_, err := pc.Select(ctx, model.SelectRequest{
		NodeID:  "node-7f31",
		Confirm: false,
	})
	if !errors.Is(err, model.ErrConfirmationRequired) {
		t.Fatalf("expected ErrConfirmationRequired, got %v", err)
	}

	// Select healthy node with confirmation
	selRes, err := pc.Select(ctx, model.SelectRequest{
		NodeID:  "node-7f31",
		Confirm: true,
	})
	if err != nil {
		t.Fatalf("Select error: %v", err)
	}
	if !selRes.Success || selRes.RolledBack {
		t.Fatalf("Select should succeed: %#v", selRes)
	}
	if selRes.CurrentNode.ID != "node-7f31" {
		t.Fatalf("current node should be node-7f31, got %s", selRes.CurrentNode.ID)
	}
	if selRes.RollbackID == "" {
		t.Fatal("expected rollback ID returned")
	}

	// Rollback to original node
	if err := pc.Rollback(ctx, selRes.RollbackID); err != nil {
		t.Fatalf("Rollback error: %v", err)
	}
	status, _ := pc.Status(ctx)
	if status.CurrentNode.ID != "node-7f3a" {
		t.Fatalf("expected current node restored to node-7f3a, got %s", status.CurrentNode.ID)
	}

	// Attempting switch to a failed node triggers automatic rollback
	failRes, err := pc.Select(ctx, model.SelectRequest{
		NodeID:  "node-9c44", // failed node
		Confirm: true,
	})
	if err != nil {
		t.Fatalf("Select failed node unexpected error: %v", err)
	}
	if failRes.Success {
		t.Fatal("switching to failed node should not report success")
	}
	if !failRes.RolledBack {
		t.Fatal("switching to failed node should trigger automatic rollback")
	}
	if failRes.Reason != model.ErrCodePostSwitchHealthFailed {
		t.Fatalf("expected reason post_switch_health_failed, got %s", failRes.Reason)
	}
}

func TestConfiguredProxyControlReadOnlyMode(t *testing.T) {
	ctx := context.Background()
	cpc := NewConfiguredProxyControl(false) // controller unavailable

	status, err := cpc.Status(ctx)
	if err != nil {
		t.Fatalf("Status error: %v", err)
	}
	if status.ControllerAvailable {
		t.Fatal("expected controller_available=false")
	}
	if !status.ReadOnly {
		t.Fatal("expected read_only=true")
	}

	// Write operations must be rejected
	_, err = cpc.Select(ctx, model.SelectRequest{NodeID: "node-1", Confirm: true})
	if !errors.Is(err, model.ErrReadOnlyMode) {
		t.Fatalf("expected ErrReadOnlyMode, got %v", err)
	}

	err = cpc.UpdatePolicy(ctx, model.DefaultProxyPolicy())
	if !errors.Is(err, model.ErrControllerUnavailable) {
		t.Fatalf("expected ErrControllerUnavailable, got %v", err)
	}
}

func TestGeneratePublicNodeIDIsSafeAndConsistent(t *testing.T) {
	rawURI1 := "vmess://uuid-1@10.0.1.5:8080?alterId=0"
	rawURI2 := "vmess://uuid-2@192.168.1.1:1080?alterId=0"

	id1 := GeneratePublicNodeID(rawURI1)
	id2 := GeneratePublicNodeID(rawURI2)

	if id1 == "" || id2 == "" {
		t.Fatal("generated public node ID must not be empty")
	}
	if id1 == id2 {
		t.Fatal("different nodes must yield different public IDs")
	}
	if strings.Contains(id1, "10.0.1.5") || strings.Contains(id1, "uuid") {
		t.Fatalf("node ID leaked internal info: %s", id1)
	}

	// Consistency
	if GeneratePublicNodeID(rawURI1) != id1 {
		t.Fatal("same node must yield identical public ID across calls")
	}
}
