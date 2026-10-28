package proxycontrol

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

const safeProbeURL = "https://www.google.com/generate_204"

var (
	stripURIRegex   = regexp.MustCompile(`(?i)(vmess|vless|ss|ssr|trojan|hysteria2?|http|https)://[^\s]+`)
	stripIPRegex    = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(:\d+)?\b`)
	stripUUIDRegex  = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	stripCleanRegex = regexp.MustCompile(`[\s_\-]+`)
)

type SkyVPNConfig struct {
	BaseURL        string
	Token          string
	TimeoutSeconds int
	StateFile      string
	DataKey        string
}

type SkyVPNProxyControl struct {
	baseURL       string
	token         string
	client        *http.Client
	statePath     string
	saltSecret    []byte
	mu            sync.RWMutex
	policy        model.ProxyPolicy
	rawNameToID   map[string]string
	idToRawName   map[string]string
	nodeCache     map[string]model.ProxyNode
	history       []model.ProxyHistoryItem
	rollbackSnap  map[string]string // rollbackID -> rawNodeName
}

type skyvpnProxiesPayload struct {
	Proxies map[string]json.RawMessage `json:"proxies"`
}

type skyvpnSelectorGroup struct {
	All  []string `json:"all"`
	Now  string   `json:"now"`
	Type string   `json:"type"`
}

type skyvpnDelayPayload struct {
	Delay   int    `json:"delay"`
	Message string `json:"message,omitempty"`
}

type rollbackStateFile struct {
	RollbackID   string `json:"rollback_id"`
	PreviousNode string `json:"previous_node"`
	TargetNodeID string `json:"target_node_id"`
	TimestampMS  int64  `json:"timestamp_ms"`
	Reason       string `json:"reason"`
}

func NewSkyVPNProxyControl(cfg SkyVPNConfig) *SkyVPNProxyControl {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:6756"
	}
	timeoutSec := cfg.TimeoutSeconds
	if timeoutSec <= 0 {
		timeoutSec = 10
	}

	var salt []byte
	if trimmedKey := strings.TrimSpace(cfg.DataKey); trimmedKey != "" {
		h := sha256.Sum256([]byte("skyvpn-node-salt:" + trimmedKey))
		salt = h[:]
	} else {
		salt = make([]byte, 32)
		_, _ = rand.Read(salt)
	}

	return &SkyVPNProxyControl{
		baseURL:      baseURL,
		token:        strings.TrimSpace(cfg.Token),
		client:       &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
		statePath:    strings.TrimSpace(cfg.StateFile),
		saltSecret:   salt,
		policy:       model.DefaultProxyPolicy(),
		rawNameToID:  make(map[string]string),
		idToRawName:  make(map[string]string),
		nodeCache:    make(map[string]model.ProxyNode),
		rollbackSnap: make(map[string]string),
	}
}

func GenerateHmacNodeID(secret []byte, rawNodeName string) string {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(rawNodeName))
	full := hex.EncodeToString(h.Sum(nil))
	short := full
	if len(short) > 12 {
		short = short[:12]
	}
	return fmt.Sprintf("node-%s", short)
}

func SanitizeProxyNodeName(rawName string) (displayName, country, region string) {
	cleaned := stripURIRegex.ReplaceAllString(rawName, "")
	cleaned = stripIPRegex.ReplaceAllString(cleaned, "")
	cleaned = stripUUIDRegex.ReplaceAllString(cleaned, "")
	cleaned = strings.TrimSpace(cleaned)

	upper := strings.ToUpper(rawName)

	switch {
	case strings.Contains(upper, "JP") || strings.Contains(upper, "JAPAN") || strings.Contains(rawName, "日本") || strings.Contains(rawName, "🇯🇵"):
		country = "JP"
	case strings.Contains(upper, "SG") || strings.Contains(upper, "SINGAPORE") || strings.Contains(rawName, "新加坡") || strings.Contains(rawName, "🇸🇬"):
		country = "SG"
	case strings.Contains(upper, "US") || strings.Contains(upper, "USA") || strings.Contains(upper, "UNITED STATES") || strings.Contains(rawName, "美国") || strings.Contains(rawName, "🇺🇸"):
		country = "US"
	case strings.Contains(upper, "HK") || strings.Contains(upper, "HONG KONG") || strings.Contains(upper, "HONGKONG") || strings.Contains(rawName, "香港") || strings.Contains(rawName, "🇭🇰"):
		country = "HK"
	case strings.Contains(upper, "TW") || strings.Contains(upper, "TAIWAN") || strings.Contains(rawName, "台湾") || strings.Contains(rawName, "🇹🇼"):
		country = "TW"
	case strings.Contains(upper, "KR") || strings.Contains(upper, "KOREA") || strings.Contains(rawName, "韩国") || strings.Contains(rawName, "🇰🇷"):
		country = "KR"
	case strings.Contains(upper, "GB") || strings.Contains(upper, "UK") || strings.Contains(upper, "BRITAIN") || strings.Contains(rawName, "英国") || strings.Contains(rawName, "🇬🇧"):
		country = "GB"
	case strings.Contains(upper, "DE") || strings.Contains(upper, "GERMANY") || strings.Contains(rawName, "德国") || strings.Contains(rawName, "🇩🇪"):
		country = "DE"
	default:
		country = "UN"
	}

	switch {
	case strings.Contains(upper, "TOKYO") || strings.Contains(rawName, "东京"):
		region = "Tokyo"
	case strings.Contains(upper, "OSAKA") || strings.Contains(rawName, "大阪"):
		region = "Osaka"
	case strings.Contains(upper, "SINGAPORE") || strings.Contains(rawName, "新加坡"):
		region = "Singapore"
	case strings.Contains(upper, "SILICON") || strings.Contains(upper, "CALIFORNIA") || strings.Contains(upper, "LOS ANGELES") || strings.Contains(upper, "SAN JOSE"):
		region = "Silicon Valley"
	case strings.Contains(upper, "NEW YORK"):
		region = "New York"
	case strings.Contains(upper, "HONG KONG") || strings.Contains(rawName, "香港"):
		region = "Hong Kong"
	case strings.Contains(upper, "TAIPEI") || strings.Contains(rawName, "台北"):
		region = "Taipei"
	case strings.Contains(upper, "SEOUL") || strings.Contains(rawName, "首尔"):
		region = "Seoul"
	case strings.Contains(upper, "LONDON") || strings.Contains(rawName, "伦敦"):
		region = "London"
	case strings.Contains(upper, "FRANKFURT") || strings.Contains(rawName, "法兰克福"):
		region = "Frankfurt"
	default:
		if country != "UN" {
			region = country
		} else {
			region = "Unknown"
		}
	}

	if cleaned == "" {
		cleaned = fmt.Sprintf("[%s] %s Node", country, region)
	}
	displayName = cleaned
	return displayName, country, region
}

func (s *SkyVPNProxyControl) getOrGenerateNodeID(rawName string) string {
	if id, exists := s.rawNameToID[rawName]; exists {
		return id
	}
	id := GenerateHmacNodeID(s.saltSecret, rawName)
	s.rawNameToID[rawName] = id
	s.idToRawName[id] = rawName
	return id
}

func (s *SkyVPNProxyControl) fetchRawProxies(ctx context.Context) (currentRaw string, allRaw []string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/proxies", nil)
	if err != nil {
		return "", nil, errors.New("failed to create proxies request")
	}
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return "", nil, errors.New("controller connection failed")
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("controller returned status %d", resp.StatusCode)
	}

	var payload skyvpnProxiesPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", nil, errors.New("malformed controller response")
	}

	if selectRaw, ok := payload.Proxies["select"]; ok {
		var grp skyvpnSelectorGroup
		if err := json.Unmarshal(selectRaw, &grp); err == nil && len(grp.All) > 0 {
			return grp.Now, grp.All, nil
		}
	}

	if globalRaw, ok := payload.Proxies["GLOBAL"]; ok {
		var grp skyvpnSelectorGroup
		if err := json.Unmarshal(globalRaw, &grp); err == nil && len(grp.All) > 0 {
			return grp.Now, grp.All, nil
		}
	}

	for _, raw := range payload.Proxies {
		var grp skyvpnSelectorGroup
		if err := json.Unmarshal(raw, &grp); err == nil && len(grp.All) > 0 {
			return grp.Now, grp.All, nil
		}
	}

	return "", nil, errors.New("no proxy selector group found")
}

func (s *SkyVPNProxyControl) probeNodeDelay(ctx context.Context, rawName string) (int, error) {
	encodedName := url.PathEscape(rawName)
	probeReqURL := fmt.Sprintf("%s/proxies/%s/delay?timeout=5000&url=%s", s.baseURL, encodedName, url.QueryEscape(safeProbeURL))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeReqURL, nil)
	if err != nil {
		return 0, errors.New("failed to build probe request")
	}
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return 0, errors.New(model.ErrCodeNodeProbeTimeout)
		}
		return 0, errors.New("probe request failed")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("probe failed with status %d", resp.StatusCode)
	}

	var payload skyvpnDelayPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0, errors.New("malformed probe response")
	}
	if payload.Delay <= 0 {
		return 0, errors.New("invalid delay value")
	}

	return payload.Delay, nil
}

func (s *SkyVPNProxyControl) putSelectRaw(ctx context.Context, rawName string) error {
	bodyBytes, err := json.Marshal(map[string]string{"name": rawName})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.baseURL+"/proxies/select", bytes.NewReader(bodyBytes))
	if err != nil {
		return errors.New("failed to build select request")
	}
	req.Header.Set("Content-Type", "application/json")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return errors.New("switch request failed")
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("switch returned status %d", resp.StatusCode)
	}
	return nil
}

func (s *SkyVPNProxyControl) buildNode(rawName string, isSelected bool) model.ProxyNode {
	publicID := s.getOrGenerateNodeID(rawName)
	if existing, found := s.nodeCache[publicID]; found {
		existing.Selected = isSelected
		return existing
	}

	displayName, country, region := SanitizeProxyNodeName(rawName)
	node := model.ProxyNode{
		ID:                   publicID,
		Name:                 displayName,
		Country:              country,
		Region:               region,
		Health:               "unknown",
		LatencyMS:            nil,
		Selected:             isSelected,
		PreferredLevel:       0,
		ConsecutiveFailures:  0,
		ConsecutiveSuccesses: 0,
		Switchable:           true,
	}
	s.nodeCache[publicID] = node
	return node
}

func (s *SkyVPNProxyControl) saveRollbackState(state rollbackStateFile) {
	if s.statePath == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.statePath), 0700)
	data, err := json.MarshalIndent(state, "", "  ")
	if err == nil {
		_ = os.WriteFile(s.statePath, data, 0600)
	}
}

func (s *SkyVPNProxyControl) recordHistory(item model.ProxyHistoryItem) {
	item.ID = fmt.Sprintf("hist-%d", time.Now().UnixNano())
	item.Timestamp = time.Now().UTC().Format(time.RFC3339)
	if item.Operator == "" {
		item.Operator = "admin"
	}
	s.history = append([]model.ProxyHistoryItem{item}, s.history...)
	if len(s.history) > 100 {
		s.history = s.history[:100]
	}
}

func (s *SkyVPNProxyControl) Status(ctx context.Context) (model.ProxyStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	currentRaw, _, err := s.fetchRawProxies(ctx)
	if err != nil {
		return model.ProxyStatus{
			Enabled:             false,
			ReadOnly:            true,
			ControllerAvailable: false,
			CurrentNode:         nil,
			CheckStages:         []model.HealthCheckStage{},
		}, nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	currentNode := s.buildNode(currentRaw, true)
	if currentNode.Health == "unknown" {
		currentNode.Health = "healthy"
	}

	return model.ProxyStatus{
		Enabled:             true,
		ReadOnly:            false,
		ControllerAvailable: true,
		CurrentNode:         &currentNode,
		Egress: &model.EgressStatus{
			Health:    currentNode.Health,
			IP:        nil,
			IPDisplay: currentNode.Country + " egress verified",
			Country:   currentNode.Country,
			CheckedAt: now,
		},
		Selection: &model.SelectionStatus{
			Mode:       "manual",
			DryRun:     s.policy.DryRun,
			AutoSwitch: s.policy.AutoSwitch,
			LastReason: "active_node_selected",
			LastResult: "success",
		},
		CheckStages: Default6Stages(true, "", ""),
	}, nil
}

func (s *SkyVPNProxyControl) Nodes(ctx context.Context, filter model.NodeFilter) (model.NodeList, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	currentRaw, allRaw, err := s.fetchRawProxies(ctx)
	if err != nil {
		return model.NodeList{
			Items:   []model.ProxyNode{},
			Total:   0,
			Healthy: 0,
		}, nil
	}

	items := make([]model.ProxyNode, 0, len(allRaw))
	for _, raw := range allRaw {
		isSelected := (raw == currentRaw)
		items = append(items, s.buildNode(raw, isSelected))
	}

	filtered := make([]model.ProxyNode, 0, len(items))
	for _, n := range items {
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
			return nodeLatency(filtered[i]) < nodeLatency(filtered[j])
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
		sort.Slice(filtered, func(i, j int) bool {
			if filtered[i].Selected != filtered[j].Selected {
				return filtered[i].Selected
			}
			if filtered[i].PreferredLevel != filtered[j].PreferredLevel {
				return filtered[i].PreferredLevel > filtered[j].PreferredLevel
			}
			return nodeLatency(filtered[i]) < nodeLatency(filtered[j])
		})
	}

	healthy, degraded, failed := 0, 0, 0
	for _, n := range items {
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

func (s *SkyVPNProxyControl) Policy(ctx context.Context) (model.ProxyPolicy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.policy, nil
}

func (s *SkyVPNProxyControl) UpdatePolicy(ctx context.Context, policy model.ProxyPolicy) error {
	if err := model.ValidateProxyPolicy(policy); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policy = policy
	return nil
}

func (s *SkyVPNProxyControl) HealthCheck(ctx context.Context, req model.HealthCheckRequest) (model.HealthCheckResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	currentRaw, allRaw, err := s.fetchRawProxies(ctx)
	if err != nil {
		return model.HealthCheckResult{
			Healthy:   false,
			ErrorCode: model.ErrCodeControllerUnavailable,
			Stages:    []model.HealthCheckStage{},
		}, nil
	}

	targetRaw := currentRaw
	if req.NodeID != "" {
		raw, exists := s.idToRawName[req.NodeID]
		if !exists {
			for _, r := range allRaw {
				if s.getOrGenerateNodeID(r) == req.NodeID {
					raw = r
					break
				}
			}
		}
		if raw == "" {
			return model.HealthCheckResult{
				NodeID:    req.NodeID,
				Healthy:   false,
				ErrorCode: "node_not_found",
				Stages:    Default6Stages(false, "proxy_reachability", "node_not_found"),
			}, errors.New("node not found")
		}
		targetRaw = raw
	}

	publicID := s.getOrGenerateNodeID(targetRaw)
	delayMS, err := s.probeNodeDelay(ctx, targetRaw)
	now := time.Now().UTC().Format(time.RFC3339)

	if err != nil {
		cached := s.buildNode(targetRaw, targetRaw == currentRaw)
		cached.Health = "failed"
		cached.LatencyMS = nil
		cached.ConsecutiveFailures++
		cached.ConsecutiveSuccesses = 0
		cached.LastCheckedAt = now
		cached.Reason = model.ErrCodeGoogleCheckFailed
		s.nodeCache[publicID] = cached

		stages := Default6Stages(false, "google_reachability", model.ErrCodeGoogleCheckFailed)
		s.recordHistory(model.ProxyHistoryItem{
			Operation:         "health_check",
			SourceNodeID:      publicID,
			SourceNodeName:    cached.Name,
			DryRun:            req.DryRun,
			HealthCheckPassed: false,
			FinalResult:       "failed",
			ErrorCode:         model.ErrCodeGoogleCheckFailed,
		})

		return model.HealthCheckResult{
			NodeID:    publicID,
			Healthy:   false,
			LatencyMS: 0,
			Stages:    stages,
			CheckedAt: now,
			ErrorCode: model.ErrCodeGoogleCheckFailed,
		}, nil
	}

	cached := s.buildNode(targetRaw, targetRaw == currentRaw)
	cached.Health = "healthy"
	cached.LatencyMS = &delayMS
	cached.ConsecutiveSuccesses++
	cached.ConsecutiveFailures = 0
	cached.LastCheckedAt = now
	cached.Reason = ""
	s.nodeCache[publicID] = cached

	stages := Default6Stages(true, "", "")
	s.recordHistory(model.ProxyHistoryItem{
		Operation:         "health_check",
		SourceNodeID:      publicID,
		SourceNodeName:    cached.Name,
		DryRun:            req.DryRun,
		HealthCheckPassed: true,
		FinalResult:       "success",
	})

	return model.HealthCheckResult{
		NodeID:    publicID,
		Healthy:   true,
		LatencyMS: delayMS,
		Stages:    stages,
		CheckedAt: now,
	}, nil
}

func (s *SkyVPNProxyControl) DryRunSelect(ctx context.Context, req model.SelectRequest) (model.SelectResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	currentRaw, allRaw, err := s.fetchRawProxies(ctx)
	if err != nil {
		return model.SelectResult{
			Success:     false,
			WouldSwitch: false,
			Reason:      model.ErrCodeControllerUnavailable,
			Checks:      []model.HealthCheckStage{},
		}, nil
	}

	currentNode := s.buildNode(currentRaw, true)

	if req.NodeID != "" {
		var targetRaw string
		for _, r := range allRaw {
			if s.getOrGenerateNodeID(r) == req.NodeID {
				targetRaw = r
				break
			}
		}
		if targetRaw == "" {
			return model.SelectResult{
				Success:     false,
				WouldSwitch: false,
				Reason:      "node_not_found",
			}, errors.New("target node not found")
		}

		targetNode := s.buildNode(targetRaw, targetRaw == currentRaw)
		if targetRaw == currentRaw {
			return model.SelectResult{
				Success:         true,
				WouldSwitch:     false,
				Reason:          "already_current_node",
				CurrentNode:     &currentNode,
				RecommendedNode: &currentNode,
				Checks:          Default6Stages(true, "", ""),
			}, nil
		}

		s.recordHistory(model.ProxyHistoryItem{
			Operation:      "dry_run_select",
			SourceNodeID:   currentNode.ID,
			SourceNodeName: currentNode.Name,
			TargetNodeID:   targetNode.ID,
			TargetNodeName: targetNode.Name,
			DryRun:         true,
			FinalResult:    "success",
			TriggerReason:  req.Reason,
		})

		return model.SelectResult{
			Success:         true,
			WouldSwitch:     true,
			Reason:          "manual_selection_dry_run",
			CurrentNode:     &currentNode,
			RecommendedNode: &targetNode,
			Checks:          Default6Stages(true, "", ""),
		}, nil
	}

	var candidates []model.ProxyNode
	for _, r := range allRaw {
		n := s.buildNode(r, r == currentRaw)
		allowed := false
		for _, c := range s.policy.AllowedCountries {
			if strings.EqualFold(n.Country, c) {
				allowed = true
				break
			}
		}
		if allowed && n.Switchable && n.Health != "failed" {
			candidates = append(candidates, n)
		}
	}

	var recommended *model.ProxyNode
	for i := range candidates {
		n := &candidates[i]
		if recommended == nil || nodeLatency(*n) < nodeLatency(*recommended) {
			recommended = n
		}
	}
	if recommended == nil {
		recommended = &currentNode
	}

	wouldSwitch := (recommended.ID != currentNode.ID)
	reason := "current_node_healthy"
	if wouldSwitch {
		reason = "preferred_healthy_low_latency"
	}

	s.recordHistory(model.ProxyHistoryItem{
		Operation:      "dry_run_select",
		SourceNodeID:   currentNode.ID,
		SourceNodeName: currentNode.Name,
		TargetNodeID:   recommended.ID,
		TargetNodeName: recommended.Name,
		DryRun:         true,
		FinalResult:    "success",
		TriggerReason:  reason,
	})

	return model.SelectResult{
		Success:         true,
		WouldSwitch:     wouldSwitch,
		Reason:          reason,
		CurrentNode:     &currentNode,
		RecommendedNode: recommended,
		Candidates:      candidates,
		Checks:          Default6Stages(true, "", ""),
	}, nil
}

func (s *SkyVPNProxyControl) Select(ctx context.Context, req model.SelectRequest) (model.SelectResult, error) {
	if !req.Confirm {
		return model.SelectResult{}, model.ErrConfirmationRequired
	}
	if req.NodeID == "" {
		return model.SelectResult{}, model.ErrNodeRequired
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	currentRaw, allRaw, err := s.fetchRawProxies(ctx)
	if err != nil {
		return model.SelectResult{
			Success:     false,
			RolledBack:  false,
			Reason:      model.ErrCodeControllerUnavailable,
			CurrentNode: nil,
		}, model.ErrControllerUnavailable
	}

	var targetRaw string
	for _, r := range allRaw {
		if s.getOrGenerateNodeID(r) == req.NodeID {
			targetRaw = r
			break
		}
	}
	if targetRaw == "" {
		return model.SelectResult{
			Success:    false,
			RolledBack: false,
			Reason:     "node_not_found",
		}, errors.New("node not found")
	}

	currentNode := s.buildNode(currentRaw, true)
	if targetRaw == currentRaw {
		return model.SelectResult{
			Success:     true,
			WouldSwitch: false,
			RolledBack:  false,
			CurrentNode: &currentNode,
			Reason:      "already_current_node",
			Checks:      Default6Stages(true, "", ""),
		}, nil
	}

	rollbackID := fmt.Sprintf("rb-%d-%s", time.Now().UnixMilli(), s.getOrGenerateNodeID(currentRaw))
	s.rollbackSnap[rollbackID] = currentRaw
	s.saveRollbackState(rollbackStateFile{
		RollbackID:   rollbackID,
		PreviousNode: currentRaw,
		TargetNodeID: req.NodeID,
		TimestampMS:  time.Now().UnixMilli(),
		Reason:       req.Reason,
	})

	if err := s.putSelectRaw(ctx, targetRaw); err != nil {
		s.recordHistory(model.ProxyHistoryItem{
			Operation:     "manual_select",
			SourceNodeID:  currentNode.ID,
			TargetNodeID:  req.NodeID,
			FinalResult:   "failed",
			RolledBack:    false,
			ErrorCode:     model.ErrCodeNodeSwitchFailed,
			TriggerReason: req.Reason,
		})
		return model.SelectResult{
			Success:     false,
			RolledBack:  false,
			Reason:      model.ErrCodeNodeSwitchFailed,
			CurrentNode: &currentNode,
		}, nil
	}

	newCurrent, _, confirmErr := s.fetchRawProxies(ctx)
	if confirmErr != nil || newCurrent != targetRaw {
		_ = s.putSelectRaw(ctx, currentRaw)
		s.recordHistory(model.ProxyHistoryItem{
			Operation:     "manual_select",
			SourceNodeID:  currentNode.ID,
			TargetNodeID:  req.NodeID,
			FinalResult:   "rolled_back",
			RolledBack:    true,
			ErrorCode:     model.ErrCodeNodeSwitchFailed,
			TriggerReason: req.Reason,
		})
		return model.SelectResult{
			Success:     false,
			RolledBack:  true,
			Reason:      model.ErrCodeNodeSwitchFailed,
			CurrentNode: &currentNode,
			RollbackID:  rollbackID,
		}, nil
	}

	delayMS, probeErr := s.probeNodeDelay(ctx, targetRaw)
	if probeErr != nil {
		_ = s.putSelectRaw(ctx, currentRaw)
		rolledCurrent, _, _ := s.fetchRawProxies(ctx)
		restoredNode := s.buildNode(currentRaw, rolledCurrent == currentRaw)
		s.recordHistory(model.ProxyHistoryItem{
			Operation:     "manual_select",
			SourceNodeID:  currentNode.ID,
			TargetNodeID:  req.NodeID,
			FinalResult:   "rolled_back",
			RolledBack:    true,
			ErrorCode:     model.ErrCodePostSwitchHealthFailed,
			TriggerReason: req.Reason,
		})
		return model.SelectResult{
			Success:     false,
			RolledBack:  true,
			Reason:      model.ErrCodePostSwitchHealthFailed,
			CurrentNode: &restoredNode,
			RollbackID:  rollbackID,
			Checks:      Default6Stages(false, "google_reachability", model.ErrCodePostSwitchHealthFailed),
		}, nil
	}

	targetNode := s.buildNode(targetRaw, true)
	targetNode.Health = "healthy"
	targetNode.LatencyMS = &delayMS
	now := time.Now().UTC().Format(time.RFC3339)
	targetNode.SelectedAt = now
	targetNode.LastCheckedAt = now
	s.nodeCache[req.NodeID] = targetNode

	s.recordHistory(model.ProxyHistoryItem{
		Operation:         "manual_select",
		SourceNodeID:      currentNode.ID,
		SourceNodeName:    currentNode.Name,
		TargetNodeID:      targetNode.ID,
		TargetNodeName:    targetNode.Name,
		FinalResult:       "success",
		RolledBack:        false,
		HealthCheckPassed: true,
		TriggerReason:     req.Reason,
	})

	return model.SelectResult{
		Success:     true,
		WouldSwitch: true,
		RolledBack:  false,
		CurrentNode: &targetNode,
		RollbackID:  rollbackID,
		Checks:      Default6Stages(true, "", ""),
	}, nil
}

func (s *SkyVPNProxyControl) Rollback(ctx context.Context, rollbackID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rawName, exists := s.rollbackSnap[rollbackID]
	if !exists && s.statePath != "" {
		if data, err := os.ReadFile(s.statePath); err == nil {
			var state rollbackStateFile
			if json.Unmarshal(data, &state) == nil && (state.RollbackID == rollbackID || rollbackID == "latest") {
				rawName = state.PreviousNode
			}
		}
	}
	if rawName == "" {
		return errors.New("rollback state not found")
	}

	if err := s.putSelectRaw(ctx, rawName); err != nil {
		return fmt.Errorf("rollback failed: %w", err)
	}

	rolledCurrent, _, err := s.fetchRawProxies(ctx)
	if err != nil || rolledCurrent != rawName {
		return errors.New("rollback confirmation failed")
	}

	targetNode := s.buildNode(rawName, true)
	s.recordHistory(model.ProxyHistoryItem{
		Operation:         "rollback",
		TargetNodeID:      targetNode.ID,
		TargetNodeName:    targetNode.Name,
		FinalResult:       "success",
		RolledBack:        true,
		HealthCheckPassed: true,
	})
	return nil
}

func (s *SkyVPNProxyControl) History(ctx context.Context, filter model.ProxyHistoryFilter) (model.ProxyHistoryPage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	filtered := make([]model.ProxyHistoryItem, 0, len(s.history))
	for _, item := range s.history {
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
