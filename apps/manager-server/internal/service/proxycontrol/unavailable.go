package proxycontrol

import (
	"context"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

// UnavailableProxyControl implements ProxyControl when no controller is configured or connected.
// In this mode, controller_available is false, read_only is true, and no mock data is exposed.
type UnavailableProxyControl struct{}

func NewUnavailableProxyControl() *UnavailableProxyControl {
	return &UnavailableProxyControl{}
}

func (u *UnavailableProxyControl) Status(ctx context.Context) (model.ProxyStatus, error) {
	return model.ProxyStatus{
		Enabled:             false,
		ReadOnly:            true,
		ControllerAvailable: false,
		CurrentNode:         nil,
		CheckStages:         []model.HealthCheckStage{},
	}, nil
}

func (u *UnavailableProxyControl) Nodes(ctx context.Context, filter model.NodeFilter) (model.NodeList, error) {
	return model.NodeList{
		Items:   []model.ProxyNode{},
		Total:   0,
		Healthy: 0,
	}, nil
}

func (u *UnavailableProxyControl) Policy(ctx context.Context) (model.ProxyPolicy, error) {
	return model.DefaultProxyPolicy(), nil
}

func (u *UnavailableProxyControl) UpdatePolicy(ctx context.Context, policy model.ProxyPolicy) error {
	return model.ErrControllerUnavailable
}

func (u *UnavailableProxyControl) HealthCheck(ctx context.Context, req model.HealthCheckRequest) (model.HealthCheckResult, error) {
	return model.HealthCheckResult{
		Healthy:   false,
		LatencyMS: 0,
		Stages:    []model.HealthCheckStage{},
		ErrorCode: model.ErrCodeControllerUnavailable,
	}, nil
}

func (u *UnavailableProxyControl) DryRunSelect(ctx context.Context, req model.SelectRequest) (model.SelectResult, error) {
	return model.SelectResult{
		Success:     false,
		WouldSwitch: false,
		Reason:      model.ErrCodeControllerUnavailable,
		Checks:      []model.HealthCheckStage{},
	}, nil
}

func (u *UnavailableProxyControl) Select(ctx context.Context, req model.SelectRequest) (model.SelectResult, error) {
	return model.SelectResult{
		Success:    false,
		RolledBack: false,
		Reason:     model.ErrCodeControllerUnavailable,
	}, model.ErrControllerUnavailable
}

func (u *UnavailableProxyControl) Rollback(ctx context.Context, rollbackID string) error {
	return model.ErrControllerUnavailable
}

func (u *UnavailableProxyControl) History(ctx context.Context, filter model.ProxyHistoryFilter) (model.ProxyHistoryPage, error) {
	return model.ProxyHistoryPage{
		Items: []model.ProxyHistoryItem{},
		Total: 0,
	}, nil
}
