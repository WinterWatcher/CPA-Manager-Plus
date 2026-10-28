package model

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	countryCodeRegex = regexp.MustCompile(`^[A-Z]{2}$`)

	ErrInvalidCountryCode     = errors.New("country code must be a 2-letter uppercase ISO code")
	ErrInvalidProbeConcurrency = errors.New("probe concurrency must be between 1 and 20")
	ErrInvalidProbeTimeout     = errors.New("probe timeout must be between 1 and 60 seconds")
	ErrInvalidHealthRounds     = errors.New("health check rounds must be between 1 and 10")
	ErrInvalidFailureThreshold = errors.New("failure threshold must be between 1 and 10")
	ErrInvalidSuccessThreshold = errors.New("success threshold must be between 1 and 10")
	ErrInvalidCooldownSeconds  = errors.New("cooldown seconds must be between 0 and 3600")
	ErrNodeRequired            = errors.New("node ID is required")
	ErrRollbackIDRequired      = errors.New("rollback ID is required")
	ErrConfirmationRequired    = errors.New("explicit confirmation is required for node modification")
	ErrControllerUnavailable   = errors.New("controller_unavailable")
	ErrReadOnlyMode            = errors.New("proxy management is in read-only mode")
)

// Allowed safe error codes
const (
	ErrCodeProxyUnreachable         = "proxy_unreachable"
	ErrCodeEgressIPMismatch         = "egress_ip_mismatch"
	ErrCodeGoogleCheckFailed        = "google_check_failed"
	ErrCodeOAuthCheckFailed         = "oauth_check_failed"
	ErrCodeCloudCodeCheckFailed     = "cloud_code_check_failed"
	ErrCodeNodeProbeTimeout         = "node_probe_timeout"
	ErrCodeNodeSwitchFailed         = "node_switch_failed"
	ErrCodePostSwitchHealthFailed   = "post_switch_health_failed"
	ErrCodeControllerUnavailable    = "controller_unavailable"
	ErrCodeConfigurationMissing     = "configuration_missing"
)

type ProxyStatus struct {
	Enabled             bool               `json:"enabled"`
	ReadOnly            bool               `json:"read_only"`
	ControllerAvailable bool               `json:"controller_available"`
	CurrentNode         *ProxyNode         `json:"current_node,omitempty"`
	Egress              *EgressStatus      `json:"egress,omitempty"`
	Selection           *SelectionStatus   `json:"selection,omitempty"`
	CheckStages         []HealthCheckStage `json:"check_stages"`
}

type ProxyNode struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Country               string `json:"country"`
	Region                string `json:"region"`
	Health                string `json:"health"` // healthy, degraded, checking, failed, unknown, switching, rollback
	LatencyMS             int    `json:"latency_ms"`
	Selected              bool   `json:"selected"`
	PreferredLevel        int    `json:"preferred_level,omitempty"`
	ConsecutiveFailures   int    `json:"consecutive_failures"`
	ConsecutiveSuccesses  int    `json:"consecutive_successes"`
	Switchable            bool   `json:"switchable"`
	LastCheckedAt         string `json:"last_checked_at,omitempty"`
	SelectedAt            string `json:"selected_at,omitempty"`
	Reason                string `json:"reason,omitempty"`
}

type EgressStatus struct {
	Health    string  `json:"health"`
	IP        *string `json:"ip"` // null or "[REDACTED]"
	IPDisplay string  `json:"ip_display"`
	Country   string  `json:"country"`
	CheckedAt string  `json:"checked_at"`
}

type SelectionStatus struct {
	Mode       string `json:"mode"` // manual | auto
	DryRun     bool   `json:"dry_run"`
	AutoSwitch bool   `json:"auto_switch"`
	LastReason string `json:"last_reason,omitempty"`
	LastResult string `json:"last_result,omitempty"`
}

type HealthCheckStage struct {
	Stage       string `json:"stage"`
	Name        string `json:"name"`
	Status      string `json:"status"` // passed, failed, running, pending, skipped
	HTTPStatus  int    `json:"http_status,omitempty"`
	LatencyMS   int    `json:"latency_ms,omitempty"`
	StartedAt   string `json:"started_at,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
	ErrorCode   string `json:"error_code,omitempty"`
	Passed      bool   `json:"passed"`
}

type NodeFilter struct {
	Country       string
	Health        string
	Selected      *bool
	PreferredOnly bool
	Sort          string // priority, health, latency, last_checked
	Page          int
	PageSize      int
}

type NodeList struct {
	Items         []ProxyNode `json:"items"`
	Total         int         `json:"total"`
	Healthy       int         `json:"healthy"`
	Degraded      int         `json:"degraded"`
	Failed        int         `json:"failed"`
	LastRefreshAt *string     `json:"last_refresh_at"`
}

type ProxyPolicy struct {
	AutoSwitch          bool     `json:"auto_switch"`
	DryRun              bool     `json:"dry_run"`
	AllowedCountries    []string `json:"allowed_countries"`
	PreferredNodes      []string `json:"preferred_nodes"`
	ProbeConcurrency    int      `json:"probe_concurrency"`
	ProbeTimeoutSeconds int      `json:"probe_timeout_seconds"`
	HealthCheckRounds   int      `json:"health_check_rounds"`
	FailureThreshold    int      `json:"failure_threshold"`
	SuccessThreshold    int      `json:"success_threshold"`
	CooldownSeconds     int      `json:"cooldown_seconds"`
	RollbackOnFailure   bool     `json:"rollback_on_failure"`
	AllowAutoRollback   bool     `json:"allow_auto_rollback"`
}

func DefaultProxyPolicy() ProxyPolicy {
	return ProxyPolicy{
		AutoSwitch:          false,
		DryRun:              true,
		AllowedCountries:    []string{"JP", "SG", "US"},
		PreferredNodes:      []string{},
		ProbeConcurrency:    5,
		ProbeTimeoutSeconds: 10,
		HealthCheckRounds:   3,
		FailureThreshold:    3,
		SuccessThreshold:    2,
		CooldownSeconds:     300,
		RollbackOnFailure:   true,
		AllowAutoRollback:   true,
	}
}

func ValidateProxyPolicy(p ProxyPolicy) error {
	for _, c := range p.AllowedCountries {
		trimmed := strings.ToUpper(strings.TrimSpace(c))
		if !countryCodeRegex.MatchString(trimmed) {
			return fmt.Errorf("%w: %q", ErrInvalidCountryCode, c)
		}
	}
	if p.ProbeConcurrency < 1 || p.ProbeConcurrency > 20 {
		return ErrInvalidProbeConcurrency
	}
	if p.ProbeTimeoutSeconds < 1 || p.ProbeTimeoutSeconds > 60 {
		return ErrInvalidProbeTimeout
	}
	if p.HealthCheckRounds < 1 || p.HealthCheckRounds > 10 {
		return ErrInvalidHealthRounds
	}
	if p.FailureThreshold < 1 || p.FailureThreshold > 10 {
		return ErrInvalidFailureThreshold
	}
	if p.SuccessThreshold < 1 || p.SuccessThreshold > 10 {
		return ErrInvalidSuccessThreshold
	}
	if p.CooldownSeconds < 0 || p.CooldownSeconds > 3600 {
		return ErrInvalidCooldownSeconds
	}
	return nil
}

type HealthCheckRequest struct {
	DryRun bool   `json:"dry_run"`
	NodeID string `json:"node_id,omitempty"`
}

type HealthCheckResult struct {
	NodeID    string             `json:"node_id"`
	Healthy   bool               `json:"healthy"`
	LatencyMS int                `json:"latency_ms"`
	Stages    []HealthCheckStage `json:"stages"`
	CheckedAt string             `json:"checked_at"`
	ErrorCode string             `json:"error_code,omitempty"`
}

type SelectRequest struct {
	NodeID  string `json:"node_id"`
	Reason  string `json:"reason,omitempty"`
	Confirm bool   `json:"confirm"`
	DryRun  bool   `json:"dry_run"`
}

type SelectResult struct {
	Success         bool               `json:"success"`
	WouldSwitch     bool               `json:"would_switch"`
	RolledBack      bool               `json:"rolled_back"`
	CurrentNode     *ProxyNode         `json:"current_node,omitempty"`
	RecommendedNode *ProxyNode         `json:"recommended_node,omitempty"`
	Candidates      []ProxyNode        `json:"candidates,omitempty"`
	Reason          string             `json:"reason,omitempty"`
	Checks          []HealthCheckStage `json:"checks,omitempty"`
	RollbackID      string             `json:"rollback_id,omitempty"`
}

type RollbackRequest struct {
	RollbackID string `json:"rollback_id"`
	Confirm    bool   `json:"confirm"`
}

type ProxyHistoryItem struct {
	ID                string `json:"id"`
	Timestamp         string `json:"timestamp"`
	Operation         string `json:"operation"` // health_check, dry_run_select, manual_select, automatic_select, rollback, refresh_nodes
	SourceNodeID      string `json:"source_node_id,omitempty"`
	SourceNodeName    string `json:"source_node_name,omitempty"`
	TargetNodeID      string `json:"target_node_id,omitempty"`
	TargetNodeName    string `json:"target_node_name,omitempty"`
	TriggerReason     string `json:"trigger_reason,omitempty"`
	DryRun            bool   `json:"dry_run"`
	HealthCheckPassed bool   `json:"health_check_passed"`
	FinalResult       string `json:"final_result"` // success, failed, rolled_back, kept_current
	RolledBack        bool   `json:"rolled_back"`
	Operator          string `json:"operator"`
	ErrorCode         string `json:"error_code,omitempty"`
}

type ProxyHistoryFilter struct {
	Operation string
	Page      int
	PageSize  int
}

type ProxyHistoryPage struct {
	Items []ProxyHistoryItem `json:"items"`
	Total int                `json:"total"`
}
