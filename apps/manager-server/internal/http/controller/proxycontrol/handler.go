package proxycontrol

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/middleware"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	proxycontrolsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proxycontrol"
)

type Handler struct {
	App     *app.Context
	Service proxycontrolsvc.ProxyControl
}

func (h *Handler) getService() proxycontrolsvc.ProxyControl {
	if h.Service != nil {
		return h.Service
	}
	if h.App != nil && h.App.ProxyControlService != nil {
		return h.App.ProxyControlService
	}
	return proxycontrolsvc.NewUnavailableProxyControl()
}

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request) {
	if !middleware.AuthorizePanel(w, r, h.App.AdminAuthService) {
		return
	}

	path := strings.TrimRight(r.URL.Path, "/")
	subPath := path
	if strings.HasPrefix(subPath, "/v0/management/proxy") {
		subPath = strings.TrimPrefix(subPath, "/v0/management/proxy")
	} else if strings.HasPrefix(subPath, "/api/proxy") {
		subPath = strings.TrimPrefix(subPath, "/api/proxy")
	}
	subPath = strings.TrimRight(subPath, "/")

	svc := h.getService()
	ctx := r.Context()

	switch {
	case (subPath == "" || subPath == "/status") && r.Method == http.MethodGet:
		status, err := svc.Status(ctx)
		if err != nil {
			response.Error(w, http.StatusInternalServerError, err)
			return
		}
		response.JSON(w, http.StatusOK, status)

	case subPath == "/nodes" && r.Method == http.MethodGet:
		filter := parseNodeFilter(r)
		list, err := svc.Nodes(ctx, filter)
		if err != nil {
			response.Error(w, http.StatusInternalServerError, err)
			return
		}
		response.JSON(w, http.StatusOK, list)

	case subPath == "/policy" && r.Method == http.MethodGet:
		pol, err := svc.Policy(ctx)
		if err != nil {
			response.Error(w, http.StatusInternalServerError, err)
			return
		}
		response.JSON(w, http.StatusOK, pol)

	case subPath == "/policy" && r.Method == http.MethodPut:
		var pol model.ProxyPolicy
		if err := json.NewDecoder(r.Body).Decode(&pol); err != nil {
			response.Error(w, http.StatusBadRequest, err)
			return
		}
		if err := svc.UpdatePolicy(ctx, pol); err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, model.ErrControllerUnavailable) || errors.Is(err, model.ErrReadOnlyMode) {
				status = http.StatusServiceUnavailable
			}
			response.Error(w, status, err)
			return
		}
		updated, _ := svc.Policy(ctx)
		response.JSON(w, http.StatusOK, updated)

	case subPath == "/health-check" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		var req model.HealthCheckRequest
		req.DryRun = true
		if r.Method == http.MethodPost && r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		res, err := svc.HealthCheck(ctx, req)
		if err != nil {
			response.Error(w, http.StatusBadRequest, err)
			return
		}
		response.JSON(w, http.StatusOK, res)

	case subPath == "/select/dry-run" && r.Method == http.MethodPost:
		var req model.SelectRequest
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		req.DryRun = true
		res, err := svc.DryRunSelect(ctx, req)
		if err != nil {
			response.Error(w, http.StatusBadRequest, err)
			return
		}
		response.JSON(w, http.StatusOK, res)

	case subPath == "/select" && r.Method == http.MethodPost:
		var req model.SelectRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			response.Error(w, http.StatusBadRequest, err)
			return
		}
		if !req.Confirm {
			response.Error(w, http.StatusBadRequest, model.ErrConfirmationRequired)
			return
		}
		res, err := svc.Select(ctx, req)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, model.ErrReadOnlyMode) {
				status = http.StatusForbidden
			}
			response.Error(w, status, err)
			return
		}
		response.JSON(w, http.StatusOK, res)

	case subPath == "/rollback" && r.Method == http.MethodPost:
		var req model.RollbackRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			response.Error(w, http.StatusBadRequest, err)
			return
		}
		if !req.Confirm {
			response.Error(w, http.StatusBadRequest, model.ErrConfirmationRequired)
			return
		}
		if err := svc.Rollback(ctx, req.RollbackID); err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, model.ErrReadOnlyMode) {
				status = http.StatusForbidden
			}
			response.Error(w, status, err)
			return
		}
		response.JSON(w, http.StatusOK, map[string]any{"ok": true, "rolled_back": true})

	case subPath == "/history" && r.Method == http.MethodGet:
		filter := parseHistoryFilter(r)
		page, err := svc.History(ctx, filter)
		if err != nil {
			response.Error(w, http.StatusInternalServerError, err)
			return
		}
		response.JSON(w, http.StatusOK, page)

	default:
		response.MethodNotAllowed(w)
	}
}

func parseNodeFilter(r *http.Request) model.NodeFilter {
	q := r.URL.Query()
	f := model.NodeFilter{
		Country:       q.Get("country"),
		Health:        q.Get("health"),
		PreferredOnly: q.Get("preferred") == "true",
		Sort:          q.Get("sort"),
		Page:          1,
		PageSize:      50,
	}
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 0 {
		f.Page = p
	}
	if ps, err := strconv.Atoi(q.Get("page_size")); err == nil && ps > 0 {
		f.PageSize = ps
	}
	if sel := q.Get("selected"); sel != "" {
		b := (sel == "true")
		f.Selected = &b
	}
	return f
}

func parseHistoryFilter(r *http.Request) model.ProxyHistoryFilter {
	q := r.URL.Query()
	f := model.ProxyHistoryFilter{
		Operation: q.Get("operation"),
		Page:      1,
		PageSize:  50,
	}
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 0 {
		f.Page = p
	}
	if ps, err := strconv.Atoi(q.Get("page_size")); err == nil && ps > 0 {
		f.PageSize = ps
	}
	return f
}
