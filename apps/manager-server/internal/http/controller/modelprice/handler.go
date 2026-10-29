package modelprice

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/middleware"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	modelpricerepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/modelprice"
	modelpricesvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/modelprice"
)

type Handler struct {
	App *app.Context
}

type reviewActionRequest struct {
	Reason string `json:"reason"`
}

type freezeActionRequest struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Reason   string `json:"reason"`
}

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request) {
	if !middleware.AuthorizePanel(w, r, h.App.AdminAuthService) {
		return
	}

	path := strings.TrimRight(r.URL.Path, "/")
	switch {
	case path == "/v0/management/model-prices/runtime-models" && r.Method == http.MethodGet:
		status, err := h.App.ModelPriceService.RuntimeModelPricingStatus(r.Context())
		if err != nil {
			response.Error(w, http.StatusInternalServerError, err)
			return
		}
		response.JSON(w, http.StatusOK, status)

	case path == "/v0/management/model-prices/usage-summary" && r.Method == http.MethodGet:
		summary, err := h.App.ModelPriceService.UsageSummary(r.Context(), h.App.Config.QueryLimit)
		if err != nil {
			response.Error(w, http.StatusInternalServerError, err)
			return
		}
		response.JSON(w, http.StatusOK, summary)

	case path == "/v0/management/model-prices" && r.Method == http.MethodGet:
		prices, err := h.App.ModelPriceService.List(r.Context())
		if err != nil {
			response.Error(w, http.StatusInternalServerError, err)
			return
		}
		response.JSON(w, http.StatusOK, map[string]any{"prices": prices})

	case path == "/v0/management/model-prices" && r.Method == http.MethodPut:
		var req modelpricesvc.UpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			response.Error(w, http.StatusBadRequest, err)
			return
		}
		prices, err := h.App.ModelPriceService.Replace(r.Context(), req.Prices)
		if err != nil {
			response.Error(w, modelPriceMutationStatus(err, http.StatusBadRequest), err)
			return
		}
		response.JSON(w, http.StatusOK, map[string]any{"prices": prices})

	case path == "/v0/management/model-prices/sync" && r.Method == http.MethodPost:
		var req modelpricesvc.SyncRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			response.Error(w, http.StatusBadRequest, err)
			return
		}
		result, err := h.App.ModelPriceService.Sync(r.Context(), req)
		if err != nil {
			response.Error(w, modelPriceMutationStatus(err, response.ModelPriceErrorStatus(err)), err)
			return
		}
		response.JSON(w, http.StatusOK, result)

	case path == "/v0/management/model-prices/candidates" && r.Method == http.MethodGet:
		h.handleListCandidates(w, r)

	case strings.HasPrefix(path, "/v0/management/model-prices/candidates/"):
		h.handleCandidateAction(w, r, path)

	case path == "/v0/management/model-prices/freeze" && r.Method == http.MethodPost:
		var req freezeActionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			response.Error(w, http.StatusBadRequest, err)
			return
		}
		if req.Model == "" {
			response.Error(w, http.StatusBadRequest, errors.New("model name is required"))
			return
		}
		operator := "admin"
		if err := h.App.ModelPriceService.FreezeModel(r.Context(), req.Provider, req.Model, operator, req.Reason); err != nil {
			response.Error(w, http.StatusInternalServerError, err)
			return
		}
		response.JSON(w, http.StatusOK, map[string]any{"success": true})

	case path == "/v0/management/model-prices/unfreeze" && r.Method == http.MethodPost:
		var req freezeActionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			response.Error(w, http.StatusBadRequest, err)
			return
		}
		if req.Model == "" {
			response.Error(w, http.StatusBadRequest, errors.New("model name is required"))
			return
		}
		operator := "admin"
		if err := h.App.ModelPriceService.UnfreezeModel(r.Context(), req.Provider, req.Model, operator, req.Reason); err != nil {
			response.Error(w, http.StatusBadRequest, err)
			return
		}
		response.JSON(w, http.StatusOK, map[string]any{"success": true})

	case path == "/v0/management/model-prices/frozen" && r.Method == http.MethodGet:
		list, err := h.App.ModelPriceService.ListFrozenModels(r.Context())
		if err != nil {
			response.Error(w, http.StatusInternalServerError, err)
			return
		}
		response.JSON(w, http.StatusOK, map[string]any{"frozen": list})

	case path == "/v0/management/model-prices/history" && r.Method == http.MethodGet:
		q := r.URL.Query()
		provider := q.Get("provider")
		modelName := q.Get("model")
		limit, _ := strconv.Atoi(q.Get("limit"))
		offset, _ := strconv.Atoi(q.Get("offset"))
		snapshots, total, err := h.App.ModelPriceService.ListSnapshots(r.Context(), provider, modelName, limit, offset)
		if err != nil {
			response.Error(w, http.StatusInternalServerError, err)
			return
		}
		response.JSON(w, http.StatusOK, map[string]any{
			"snapshots": snapshots,
			"total":     total,
		})

	case path == "/v0/management/model-prices/audit" && r.Method == http.MethodGet:
		q := r.URL.Query()
		provider := q.Get("provider")
		modelName := q.Get("model")
		limit, _ := strconv.Atoi(q.Get("limit"))
		offset, _ := strconv.Atoi(q.Get("offset"))
		events, total, err := h.App.ModelPriceService.ListAuditEvents(r.Context(), provider, modelName, limit, offset)
		if err != nil {
			response.Error(w, http.StatusInternalServerError, err)
			return
		}
		response.JSON(w, http.StatusOK, map[string]any{
			"events": events,
			"total":  total,
		})

	default:
		response.MethodNotAllowed(w)
	}
}

func (h *Handler) handleListCandidates(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	filter := modelpricerepo.CandidateFilter{
		Status:     q.Get("status"),
		ChangeKind: q.Get("changeKind"),
		Provider:   q.Get("provider"),
		Model:      q.Get("model"),
		Limit:      limit,
		Offset:     offset,
	}

	candidates, total, err := h.App.ModelPriceService.ListCandidates(r.Context(), filter)
	if err != nil {
		response.Error(w, http.StatusInternalServerError, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{
		"candidates": candidates,
		"total":      total,
	})
}

func (h *Handler) handleCandidateAction(w http.ResponseWriter, r *http.Request, path string) {
	subPath := strings.TrimPrefix(path, "/v0/management/model-prices/candidates/")
	parts := strings.Split(subPath, "/")

	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		response.Error(w, http.StatusBadRequest, errors.New("valid candidate id is required"))
		return
	}

	if len(parts) == 1 && r.Method == http.MethodGet {
		c, err := h.App.ModelPriceService.GetCandidate(r.Context(), id)
		if err != nil {
			response.Error(w, http.StatusInternalServerError, err)
			return
		}
		if c == nil {
			response.Error(w, http.StatusNotFound, errors.New("candidate not found"))
			return
		}
		response.JSON(w, http.StatusOK, c)
		return
	}

	if len(parts) == 2 && r.Method == http.MethodPost {
		var req reviewActionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			response.Error(w, http.StatusBadRequest, err)
			return
		}
		reviewer := "admin"

		switch parts[1] {
		case "approve":
			approved, err := h.App.ModelPriceService.ApproveCandidate(r.Context(), id, reviewer, req.Reason)
			if err != nil {
				response.Error(w, http.StatusBadRequest, err)
				return
			}
			response.JSON(w, http.StatusOK, approved)
			return

		case "reject":
			rejected, err := h.App.ModelPriceService.RejectCandidate(r.Context(), id, reviewer, req.Reason)
			if err != nil {
				response.Error(w, http.StatusBadRequest, err)
				return
			}
			response.JSON(w, http.StatusOK, rejected)
			return
		}
	}

	response.MethodNotAllowed(w)
}

func modelPriceMutationStatus(err error, fallback int) int {
	if errors.Is(err, modelpricesvc.ErrStructureChangeAfterRawDeletion) {
		return http.StatusConflict
	}
	return fallback
}
