package proxycontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

type ProxyControl interface {
	Status(ctx context.Context) (model.ProxyStatus, error)
	Nodes(ctx context.Context, filter model.NodeFilter) (model.NodeList, error)
	Policy(ctx context.Context) (model.ProxyPolicy, error)
	UpdatePolicy(ctx context.Context, policy model.ProxyPolicy) error
	HealthCheck(ctx context.Context, req model.HealthCheckRequest) (model.HealthCheckResult, error)
	DryRunSelect(ctx context.Context, req model.SelectRequest) (model.SelectResult, error)
	Select(ctx context.Context, req model.SelectRequest) (model.SelectResult, error)
	Rollback(ctx context.Context, rollbackID string) error
	History(ctx context.Context, filter model.ProxyHistoryFilter) (model.ProxyHistoryPage, error)
}

// GeneratePublicNodeID hashes any internal identifier or name into a safe, irreversible node-xxxx id
func GeneratePublicNodeID(internalNameOrURI string) string {
	h := sha256.Sum256([]byte(internalNameOrURI))
	return fmt.Sprintf("node-%s", hex.EncodeToString(h[:2]))
}

// Default6Stages returns the standard 6 stages of egress health check
func Default6Stages(passed bool, failStage string, errCode string) []model.HealthCheckStage {
	now := time.Now().UTC().Format(time.RFC3339)
	stages := []struct {
		stage string
		name  string
	}{
		{"proxy_reachability", "代理连通性"},
		{"egress_ip_validation", "出口IP校验"},
		{"google_reachability", "Google基础连通性"},
		{"oauth_reachability", "OAuth连通性"},
		{"cloud_code_reachability", "Cloud Code连通性"},
		{"node_confirmation", "节点最终确认"},
	}

	result := make([]model.HealthCheckStage, len(stages))
	failedEncountered := false
	for i, s := range stages {
		if failedEncountered {
			result[i] = model.HealthCheckStage{
				Stage:  s.stage,
				Name:   s.name,
				Status: "skipped",
				Passed: false,
			}
			continue
		}

		if !passed && s.stage == failStage {
			result[i] = model.HealthCheckStage{
				Stage:       s.stage,
				Name:        s.name,
				Status:      "failed",
				HTTPStatus:  502,
				LatencyMS:   240,
				StartedAt:   now,
				CompletedAt: now,
				ErrorCode:   errCode,
				Passed:      false,
			}
			failedEncountered = true
			continue
		}

		result[i] = model.HealthCheckStage{
			Stage:       s.stage,
			Name:        s.name,
			Status:      "passed",
			HTTPStatus:  200,
			LatencyMS:   35 + i*15,
			StartedAt:   now,
			CompletedAt: now,
			Passed:      true,
		}
	}
	return result
}

// MockProxyControl implements ProxyControl for local testing and standalone UI operation
type MockProxyControl struct {
	mu           sync.Mutex
	status       model.ProxyStatus
	nodes        []model.ProxyNode
	policy       model.ProxyPolicy
	history      []model.ProxyHistoryItem
	rollbackSnap map[string]string // rollbackID -> nodeID
}

func NewMockProxyControl() *MockProxyControl {
	now := time.Now().UTC().Format(time.RFC3339)
	nodes := []model.ProxyNode{
		{
			ID:                   "node-7f31",
			Name:                 "JP-Tokyo-01",
			Country:              "JP",
			Region:               "Tokyo",
			Health:               "healthy",
			LatencyMS:            145,
			Selected:             false,
			PreferredLevel:       1,
			ConsecutiveSuccesses: 12,
			Switchable:           true,
			LastCheckedAt:        now,
		},
		{
			ID:                   "node-7f3a",
			Name:                 "JP-Tokyo-03",
			Country:              "JP",
			Region:               "Tokyo",
			Health:               "healthy",
			LatencyMS:            182,
			Selected:             true,
			PreferredLevel:       2,
			ConsecutiveSuccesses: 15,
			Switchable:           true,
			LastCheckedAt:        now,
			SelectedAt:           now,
		},
		{
			ID:                   "node-8a12",
			Name:                 "JP-Osaka-02",
			Country:              "JP",
			Region:               "Osaka",
			Health:               "degraded",
			LatencyMS:            320,
			Selected:             false,
			PreferredLevel:       0,
			ConsecutiveFailures:  1,
			Switchable:           true,
			LastCheckedAt:        now,
		},
		{
			ID:                   "node-9c44",
			Name:                 "JP-Tokyo-07",
			Country:              "JP",
			Region:               "Tokyo",
			Health:               "failed",
			LatencyMS:            0,
			Selected:             false,
			PreferredLevel:       0,
			ConsecutiveFailures:  4,
			Switchable:           false,
			LastCheckedAt:        now,
			Reason:               "node_probe_timeout",
		},
		{
			ID:                   "node-3e21",
			Name:                 "SG-Singapore-01",
			Country:              "SG",
			Region:               "Singapore",
			Health:               "healthy",
			LatencyMS:            195,
			Selected:             false,
			PreferredLevel:       0,
			ConsecutiveSuccesses: 8,
			Switchable:           true,
			LastCheckedAt:        now,
		},
	}

	currentNode := &nodes[1]
	policy := model.DefaultProxyPolicy()
	policy.PreferredNodes = []string{"node-7f31", "node-7f3a"}

	status := model.ProxyStatus{
		Enabled:             true,
		ReadOnly:            false,
		ControllerAvailable: true,
		CurrentNode:         currentNode,
		Egress: &model.EgressStatus{
			Health:    "healthy",
			IP:        nil,
			IPDisplay: "JP egress verified",
			Country:   "JP",
			CheckedAt: now,
		},
		Selection: &model.SelectionStatus{
			Mode:       "manual",
			DryRun:     true,
			AutoSwitch: false,
			LastReason: "current_node_healthy",
			LastResult: "kept_current",
		},
		CheckStages: Default6Stages(true, "", ""),
	}

	history := []model.ProxyHistoryItem{
		{
			ID:                "hist-001",
			Timestamp:         now,
			Operation:         "health_check",
			SourceNodeID:      "node-7f3a",
			SourceNodeName:    "JP-Tokyo-03",
			DryRun:            true,
			HealthCheckPassed: true,
			FinalResult:       "success",
			Operator:          "system",
		},
	}

	return &MockProxyControl{
		status:       status,
		nodes:        nodes,
		policy:       policy,
		history:      history,
		rollbackSnap: make(map[string]string),
	}
}

func (m *MockProxyControl) Status(ctx context.Context) (model.ProxyStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status, nil
}

func (m *MockProxyControl) Nodes(ctx context.Context, filter model.NodeFilter) (model.NodeList, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var filtered []model.ProxyNode
	for _, n := range m.nodes {
		if filter.Country != "" && !strings.EqualFold(n.Country, filter.Country) {
			continue
		}
		if filter.Health != "" && !strings.EqualFold(n.Health, filter.Health) {
			continue
		}
		if filter.Selected != nil && n.Selected != *filter.Selected {
			continue
		}
		if filter.PreferredOnly && n.PreferredLevel <= 0 {
			continue
		}
		filtered = append(filtered, n)
	}

	switch filter.Sort {
	case "latency":
		sort.Slice(filtered, func(i, j int) bool {
			if filtered[i].LatencyMS == 0 {
				return false
			}
			if filtered[j].LatencyMS == 0 {
				return true
			}
			return filtered[i].LatencyMS < filtered[j].LatencyMS
		})
	case "health":
		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].Health < filtered[j].Health
		})
	case "priority":
		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].PreferredLevel > filtered[j].PreferredLevel
		})
	case "last_checked":
		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].LastCheckedAt > filtered[j].LastCheckedAt
		})
	default:
		// Default: selected first, then priority, then latency
		sort.Slice(filtered, func(i, j int) bool {
			if filtered[i].Selected != filtered[j].Selected {
				return filtered[i].Selected
			}
			if filtered[i].PreferredLevel != filtered[j].PreferredLevel {
				return filtered[i].PreferredLevel > filtered[j].PreferredLevel
			}
			return filtered[i].LatencyMS < filtered[j].LatencyMS
		})
	}

	healthy, degraded, failed := 0, 0, 0
	for _, n := range m.nodes {
		switch n.Health {
		case "healthy":
			healthy++
		case "degraded":
			degraded++
		case "failed":
			failed++
		}
	}

	total := len(filtered)
	start := (filter.Page - 1) * filter.PageSize
	if start < 0 {
		start = 0
	}
	end := start + filter.PageSize
	if filter.PageSize <= 0 || end > total {
		end = total
	}
	if start > total {
		start = total
	}

	now := time.Now().UTC().Format(time.RFC3339)
	return model.NodeList{
		Items:         filtered[start:end],
		Total:         total,
		Healthy:       healthy,
		Degraded:      degraded,
		Failed:        failed,
		LastRefreshAt: &now,
	}, nil
}

func (m *MockProxyControl) Policy(ctx context.Context) (model.ProxyPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.policy, nil
}

func (m *MockProxyControl) UpdatePolicy(ctx context.Context, policy model.ProxyPolicy) error {
	if err := model.ValidateProxyPolicy(policy); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.policy = policy
	m.status.Selection.AutoSwitch = policy.AutoSwitch
	m.status.Selection.DryRun = policy.DryRun
	return nil
}

func (m *MockProxyControl) HealthCheck(ctx context.Context, req model.HealthCheckRequest) (model.HealthCheckResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	nodeID := req.NodeID
	if nodeID == "" && m.status.CurrentNode != nil {
		nodeID = m.status.CurrentNode.ID
	}

	var targetNode *model.ProxyNode
	for i := range m.nodes {
		if m.nodes[i].ID == nodeID {
			targetNode = &m.nodes[i]
			break
		}
	}
	if targetNode == nil {
		return model.HealthCheckResult{}, model.ErrNodeRequired
	}

	now := time.Now().UTC().Format(time.RFC3339)
	healthy := targetNode.Health != "failed"
	errCode := ""
	if !healthy {
		errCode = model.ErrCodeNodeProbeTimeout
	}

	stages := Default6Stages(healthy, "node_confirmation", errCode)
	res := model.HealthCheckResult{
		NodeID:    targetNode.ID,
		Healthy:   healthy,
		LatencyMS: targetNode.LatencyMS,
		Stages:    stages,
		CheckedAt: now,
		ErrorCode: errCode,
	}

	m.status.CheckStages = stages
	m.history = append([]model.ProxyHistoryItem{{
		ID:                fmt.Sprintf("hist-%d", time.Now().UnixNano()),
		Timestamp:         now,
		Operation:         "health_check",
		SourceNodeID:      targetNode.ID,
		SourceNodeName:    targetNode.Name,
		DryRun:            req.DryRun,
		HealthCheckPassed: healthy,
		FinalResult:       map[bool]string{true: "success", false: "failed"}[healthy],
		Operator:          "admin",
		ErrorCode:         errCode,
	}}, m.history...)

	return res, nil
}

func (m *MockProxyControl) DryRunSelect(ctx context.Context, req model.SelectRequest) (model.SelectResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var recommended *model.ProxyNode
	// Find fastest healthy node
	for i := range m.nodes {
		n := &m.nodes[i]
		if n.Health == "healthy" && n.Switchable {
			if recommended == nil || (n.LatencyMS > 0 && n.LatencyMS < recommended.LatencyMS) {
				recommended = n
			}
		}
	}

	stages := Default6Stages(true, "", "")
	wouldSwitch := recommended != nil && (m.status.CurrentNode == nil || recommended.ID != m.status.CurrentNode.ID)

	now := time.Now().UTC().Format(time.RFC3339)
	m.history = append([]model.ProxyHistoryItem{{
		ID:                fmt.Sprintf("hist-%d", time.Now().UnixNano()),
		Timestamp:         now,
		Operation:         "dry_run_select",
		SourceNodeID:      m.status.CurrentNode.ID,
		SourceNodeName:    m.status.CurrentNode.Name,
		TargetNodeID:      recommended.ID,
		TargetNodeName:    recommended.Name,
		TriggerReason:     "preferred_healthy_low_latency",
		DryRun:            true,
		HealthCheckPassed: true,
		FinalResult:       "success",
		Operator:          "admin",
	}}, m.history...)

	return model.SelectResult{
		Success:         true,
		WouldSwitch:     wouldSwitch,
		RolledBack:      false,
		CurrentNode:     m.status.CurrentNode,
		RecommendedNode: recommended,
		Candidates:      m.nodes,
		Reason:          "preferred_healthy_low_latency",
		Checks:          stages,
	}, nil
}

func (m *MockProxyControl) Select(ctx context.Context, req model.SelectRequest) (model.SelectResult, error) {
	if !req.Confirm {
		return model.SelectResult{}, model.ErrConfirmationRequired
	}
	if req.NodeID == "" {
		return model.SelectResult{}, model.ErrNodeRequired
	}
	if req.DryRun {
		return m.DryRunSelect(ctx, req)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var target *model.ProxyNode
	for i := range m.nodes {
		if m.nodes[i].ID == req.NodeID {
			target = &m.nodes[i]
			break
		}
	}
	if target == nil {
		return model.SelectResult{}, model.ErrNodeRequired
	}

	now := time.Now().UTC().Format(time.RFC3339)
	rollbackID := fmt.Sprintf("rb-%d", time.Now().Unix())
	if m.status.CurrentNode != nil {
		m.rollbackSnap[rollbackID] = m.status.CurrentNode.ID
	}

	// If target node is failed, simulate post-switch health failure and rollback
	if target.Health == "failed" {
		stages := Default6Stages(false, "node_confirmation", model.ErrCodePostSwitchHealthFailed)
		m.history = append([]model.ProxyHistoryItem{{
			ID:                fmt.Sprintf("hist-%d", time.Now().UnixNano()),
			Timestamp:         now,
			Operation:         "manual_select",
			SourceNodeID:      m.status.CurrentNode.ID,
			SourceNodeName:    m.status.CurrentNode.Name,
			TargetNodeID:      target.ID,
			TargetNodeName:    target.Name,
			TriggerReason:     req.Reason,
			DryRun:            false,
			HealthCheckPassed: false,
			FinalResult:       "rolled_back",
			RolledBack:        true,
			Operator:          "admin",
			ErrorCode:         model.ErrCodePostSwitchHealthFailed,
		}}, m.history...)

		return model.SelectResult{
			Success:     false,
			WouldSwitch: false,
			RolledBack:  true,
			CurrentNode: m.status.CurrentNode,
			Reason:      model.ErrCodePostSwitchHealthFailed,
			Checks:      stages,
			RollbackID:  rollbackID,
		}, nil
	}

	// Successful switch
	for i := range m.nodes {
		m.nodes[i].Selected = (m.nodes[i].ID == target.ID)
	}
	target.Selected = true
	target.SelectedAt = now
	m.status.CurrentNode = target
	m.status.Egress.Country = target.Country
	m.status.Egress.CheckedAt = now
	m.status.Selection.LastReason = "manual_selection_applied"
	m.status.Selection.LastResult = "switched"
	stages := Default6Stages(true, "", "")
	m.status.CheckStages = stages

	m.history = append([]model.ProxyHistoryItem{{
		ID:                fmt.Sprintf("hist-%d", time.Now().UnixNano()),
		Timestamp:         now,
		Operation:         "manual_select",
		TargetNodeID:      target.ID,
		TargetNodeName:    target.Name,
		TriggerReason:     req.Reason,
		DryRun:            false,
		HealthCheckPassed: true,
		FinalResult:       "success",
		RolledBack:        false,
		Operator:          "admin",
	}}, m.history...)

	return model.SelectResult{
		Success:     true,
		WouldSwitch: true,
		RolledBack:  false,
		CurrentNode: target,
		Reason:      "manual_switch_confirmed",
		Checks:      stages,
		RollbackID:  rollbackID,
	}, nil
}

func (m *MockProxyControl) Rollback(ctx context.Context, rollbackID string) error {
	if rollbackID == "" {
		return model.ErrRollbackIDRequired
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	prevNodeID, ok := m.rollbackSnap[rollbackID]
	if !ok {
		return fmt.Errorf("rollback point %q not found", rollbackID)
	}

	var target *model.ProxyNode
	for i := range m.nodes {
		if m.nodes[i].ID == prevNodeID {
			target = &m.nodes[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("rollback target node %q not found", prevNodeID)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	for i := range m.nodes {
		m.nodes[i].Selected = (m.nodes[i].ID == target.ID)
	}
	m.status.CurrentNode = target
	m.status.Egress.Country = target.Country
	m.status.Selection.LastReason = "rollback_to_previous_node"
	m.status.Selection.LastResult = "rolled_back"

	m.history = append([]model.ProxyHistoryItem{{
		ID:                fmt.Sprintf("hist-%d", time.Now().UnixNano()),
		Timestamp:         now,
		Operation:         "rollback",
		TargetNodeID:      target.ID,
		TargetNodeName:    target.Name,
		TriggerReason:     fmt.Sprintf("rollback_id_%s", rollbackID),
		DryRun:            false,
		HealthCheckPassed: true,
		FinalResult:       "rolled_back",
		RolledBack:        true,
		Operator:          "admin",
	}}, m.history...)

	return nil
}

func (m *MockProxyControl) History(ctx context.Context, filter model.ProxyHistoryFilter) (model.ProxyHistoryPage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var filtered []model.ProxyHistoryItem
	for _, item := range m.history {
		if filter.Operation != "" && !strings.EqualFold(item.Operation, filter.Operation) {
			continue
		}
		filtered = append(filtered, item)
	}

	total := len(filtered)
	start := (filter.Page - 1) * filter.PageSize
	if start < 0 {
		start = 0
	}
	end := start + filter.PageSize
	if filter.PageSize <= 0 || end > total {
		end = total
	}
	if start > total {
		start = total
	}

	return model.ProxyHistoryPage{
		Items: filtered[start:end],
		Total: total,
	}, nil
}

// ConfiguredProxyControl provides a safe wrapper when no external controller is wired,
// keeping the system read-only and explicitly indicating controller_available=false
type ConfiguredProxyControl struct {
	mu           sync.Mutex
	status       model.ProxyStatus
	policy       model.ProxyPolicy
	nodes        []model.ProxyNode
	history      []model.ProxyHistoryItem
}

func NewConfiguredProxyControl(available bool) *ConfiguredProxyControl {
	now := time.Now().UTC().Format(time.RFC3339)
	status := model.ProxyStatus{
		Enabled:             true,
		ReadOnly:            !available,
		ControllerAvailable: available,
		CurrentNode: &model.ProxyNode{
			ID:        "node-current",
			Name:      "Default Outbound",
			Country:   "UN",
			Region:    "Global",
			Health:    "healthy",
			LatencyMS: 0,
			Selected:  true,
		},
		Egress: &model.EgressStatus{
			Health:    "healthy",
			IP:        nil,
			IPDisplay: "Egress verified",
			Country:   "UN",
			CheckedAt: now,
		},
		Selection: &model.SelectionStatus{
			Mode:       "manual",
			DryRun:     true,
			AutoSwitch: false,
			LastReason: "default_outbound",
			LastResult: "kept_current",
		},
		CheckStages: Default6Stages(available, "node_confirmation", model.ErrCodeControllerUnavailable),
	}
	return &ConfiguredProxyControl{
		status: status,
		policy: model.DefaultProxyPolicy(),
		nodes:  []model.ProxyNode{*status.CurrentNode},
	}
}

func (c *ConfiguredProxyControl) Status(ctx context.Context) (model.ProxyStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status, nil
}

func (c *ConfiguredProxyControl) Nodes(ctx context.Context, filter model.NodeFilter) (model.NodeList, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return model.NodeList{
		Items:   c.nodes,
		Total:   len(c.nodes),
		Healthy: len(c.nodes),
	}, nil
}

func (c *ConfiguredProxyControl) Policy(ctx context.Context) (model.ProxyPolicy, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.policy, nil
}

func (c *ConfiguredProxyControl) UpdatePolicy(ctx context.Context, policy model.ProxyPolicy) error {
	if err := model.ValidateProxyPolicy(policy); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.status.ControllerAvailable {
		return model.ErrControllerUnavailable
	}
	c.policy = policy
	return nil
}

func (c *ConfiguredProxyControl) HealthCheck(ctx context.Context, req model.HealthCheckRequest) (model.HealthCheckResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.status.ControllerAvailable {
		return model.HealthCheckResult{
			Healthy:   false,
			ErrorCode: model.ErrCodeControllerUnavailable,
			Stages:    Default6Stages(false, "node_confirmation", model.ErrCodeControllerUnavailable),
		}, nil
	}
	return model.HealthCheckResult{
		Healthy: true,
		Stages:  Default6Stages(true, "", ""),
	}, nil
}

func (c *ConfiguredProxyControl) DryRunSelect(ctx context.Context, req model.SelectRequest) (model.SelectResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return model.SelectResult{
		Success:     true,
		WouldSwitch: false,
		Reason:      "controller_read_only",
		Checks:      c.status.CheckStages,
	}, nil
}

func (c *ConfiguredProxyControl) Select(ctx context.Context, req model.SelectRequest) (model.SelectResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.status.ControllerAvailable || c.status.ReadOnly {
		return model.SelectResult{}, model.ErrReadOnlyMode
	}
	return model.SelectResult{Success: true}, nil
}

func (c *ConfiguredProxyControl) Rollback(ctx context.Context, rollbackID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.status.ControllerAvailable || c.status.ReadOnly {
		return model.ErrReadOnlyMode
	}
	return nil
}

func (c *ConfiguredProxyControl) History(ctx context.Context, filter model.ProxyHistoryFilter) (model.ProxyHistoryPage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return model.ProxyHistoryPage{Items: c.history, Total: len(c.history)}, nil
}
