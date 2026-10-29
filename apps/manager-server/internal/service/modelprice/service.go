package modelprice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	modelpricerepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/modelprice"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

var ErrStructureChangeAfterRawDeletion = modelpricerepo.ErrStructureChangeAfterRawDeletion

const (
	SyncSourceModelsDev  = "models.dev"
	SyncSourceLiteLLM    = "litellm"
	SyncSourceOpenRouter = "openrouter"
	SyncSourceMulti      = "multi"

	// SyncSource is kept for existing tests and callers that still refer to the
	// original single-source LiteLLM sync constant.
	SyncSource = SyncSourceLiteLLM
)

const maxSyncCandidates = 8
const minCandidateScore = 0.55
const minWeakCandidateScore = 0.34
const defaultSyncSourceTimeout = 10 * time.Second
const defaultSyncProxyResolutionTimeout = 5 * time.Second
const defaultRuntimeModelDiscoveryTimeout = 5 * time.Second

type UpdateRequest struct {
	Prices map[string]store.ModelPrice `json:"prices"`
}

type SyncRequest struct {
	Models               []string `json:"models"`
	IncludeRuntimeModels bool     `json:"includeRuntimeModels,omitempty"`
	AutoApprove          bool     `json:"autoApprove,omitempty"`
}

type SyncResult struct {
	Source                     string                      `json:"source"`
	Sources                    []string                    `json:"sources,omitempty"`
	Imported                   int                         `json:"imported"`
	Skipped                    int                         `json:"skipped"`
	Matched                    map[string]store.ModelPrice `json:"matched,omitempty"`
	Candidates                 []SyncCandidateSet          `json:"candidates,omitempty"`
	Unmatched                  []string                    `json:"unmatched,omitempty"`
	Preserved                  []string                    `json:"preserved,omitempty"`
	ProxyUsed                  bool                        `json:"proxyUsed,omitempty"`
	SourceResults              []SyncSourceResult          `json:"sourceResults,omitempty"`
	Prices                     map[string]store.ModelPrice `json:"prices"`
	RuntimeModelCount          int                         `json:"runtimeModelCount,omitempty"`
	RuntimeModelDiscoveryError string                      `json:"runtimeModelDiscoveryError,omitempty"`
}

type SyncSourceResult struct {
	Source  string `json:"source"`
	Models  int    `json:"models"`
	Skipped int    `json:"skipped"`
	Error   string `json:"error,omitempty"`
}

type SyncCandidateSet struct {
	Model      string          `json:"model"`
	Candidates []SyncCandidate `json:"candidates"`
}

type SyncCandidate struct {
	SourceModelID string           `json:"sourceModelId"`
	Score         float64          `json:"score"`
	Reason        string           `json:"reason"`
	Price         store.ModelPrice `json:"price"`
}

type SetupResolver interface {
	ResolveSetup(ctx context.Context) (store.Setup, bool, error)
}

type Service struct {
	store                        *store.Store
	syncSources                  []priceSyncSource
	syncSourceTimeout            time.Duration
	syncProxyTimeout             time.Duration
	runtimeModelDiscoveryTimeout time.Duration
	setupResolver                SetupResolver
	notifierMu                   sync.RWMutex
	pricesChangedNotifier        func()
}

type modelPriceMatchMetadata struct {
	modelsDevCanonicalByIdentity    map[string]string
	modelsDevOfficialSourceModelIDs map[string]struct{}
}

type fetchedModelPriceSource struct {
	Prices   map[string]store.ModelPrice
	Metadata modelPriceMatchMetadata
}

type modelPriceSourceEntry struct {
	Key   string
	Price store.ModelPrice
}

type modelPriceCollection struct {
	Entries  []modelPriceSourceEntry
	Metadata modelPriceMatchMetadata
}

type fetchModelPricesFunc func(context.Context, string, *http.Client) (fetchedModelPriceSource, int, error)
type fetchModelPriceMapFunc func(context.Context, string, *http.Client) (map[string]store.ModelPrice, int, error)

type priceSyncSource struct {
	Source string
	URL    *string
	Fetch  fetchModelPricesFunc
}

func wrapModelPriceMapFetcher(fetch fetchModelPriceMapFunc) fetchModelPricesFunc {
	return func(ctx context.Context, syncURL string, client *http.Client) (fetchedModelPriceSource, int, error) {
		prices, skipped, err := fetch(ctx, syncURL, client)
		return fetchedModelPriceSource{Prices: prices}, skipped, err
	}
}

func normalizeModelPriceIdentity(identity string) string {
	return strings.ToLower(strings.TrimSpace(identity))
}

func (metadata *modelPriceMatchMetadata) merge(other modelPriceMatchMetadata) {
	if len(other.modelsDevCanonicalByIdentity) > 0 {
		if metadata.modelsDevCanonicalByIdentity == nil {
			metadata.modelsDevCanonicalByIdentity = make(map[string]string, len(other.modelsDevCanonicalByIdentity))
		}
		for identity, canonicalID := range other.modelsDevCanonicalByIdentity {
			if existing, ok := metadata.modelsDevCanonicalByIdentity[identity]; ok && !strings.EqualFold(existing, canonicalID) {
				delete(metadata.modelsDevCanonicalByIdentity, identity)
				continue
			}
			metadata.modelsDevCanonicalByIdentity[identity] = canonicalID
		}
	}
	if len(other.modelsDevOfficialSourceModelIDs) > 0 {
		if metadata.modelsDevOfficialSourceModelIDs == nil {
			metadata.modelsDevOfficialSourceModelIDs = make(map[string]struct{}, len(other.modelsDevOfficialSourceModelIDs))
		}
		for sourceModelID := range other.modelsDevOfficialSourceModelIDs {
			metadata.modelsDevOfficialSourceModelIDs[sourceModelID] = struct{}{}
		}
	}
}

func newModelsDevMatchMetadata(models map[string]json.RawMessage) modelPriceMatchMetadata {
	if len(models) == 0 {
		return modelPriceMatchMetadata{}
	}
	metadata := modelPriceMatchMetadata{
		modelsDevCanonicalByIdentity: make(map[string]string, len(models)*2),
	}
	tailMatches := make(map[string]string, len(models))
	for rawModelID := range models {
		modelID := strings.TrimSpace(rawModelID)
		if modelID == "" {
			continue
		}
		normalizedModelID := normalizeModelPriceIdentity(modelID)
		metadata.modelsDevCanonicalByIdentity[normalizedModelID] = modelID
		_, tail, ok := strings.Cut(modelID, "/")
		tail = strings.TrimSpace(tail)
		if !ok || tail == "" {
			continue
		}
		normalizedTail := normalizeModelPriceIdentity(tail)
		if existing, exists := tailMatches[normalizedTail]; exists && !strings.EqualFold(existing, modelID) {
			tailMatches[normalizedTail] = ""
			continue
		}
		tailMatches[normalizedTail] = modelID
	}
	for tail, modelID := range tailMatches {
		if modelID != "" {
			metadata.modelsDevCanonicalByIdentity[tail] = modelID
		}
	}
	return metadata
}

func (metadata modelPriceMatchMetadata) modelsDevCanonicalModelID(modelID string) (string, bool) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" || len(metadata.modelsDevCanonicalByIdentity) == 0 {
		return "", false
	}
	canonicalID, ok := metadata.modelsDevCanonicalByIdentity[normalizeModelPriceIdentity(modelID)]
	return canonicalID, ok
}

func (metadata modelPriceMatchMetadata) isModelsDevOfficialSourceModelID(sourceModelID string) bool {
	_, ok := metadata.modelsDevOfficialSourceModelIDs[normalizeModelPriceIdentity(sourceModelID)]
	return ok
}

func New(store *store.Store, syncURL *string, setupResolver ...SetupResolver) *Service {
	return NewMultiSource(store, syncURL, nil, setupResolver...)
}

func NewMultiSource(store *store.Store, liteLLMSyncURL *string, openRouterSyncURL *string, setupResolver ...SetupResolver) *Service {
	return newMultiSource(store, nil, liteLLMSyncURL, openRouterSyncURL, setupResolver...)
}

// NewMultiSourceWithModelsDev creates the production source chain. models.dev
// is deliberately first so its provider-scoped records win over the existing
// LiteLLM and OpenRouter fallbacks when both sources describe the same model.
func NewMultiSourceWithModelsDev(
	store *store.Store,
	modelsDevSyncURL *string,
	liteLLMSyncURL *string,
	openRouterSyncURL *string,
	setupResolver ...SetupResolver,
) *Service {
	return newMultiSource(store, modelsDevSyncURL, liteLLMSyncURL, openRouterSyncURL, setupResolver...)
}

func newMultiSource(
	store *store.Store,
	modelsDevSyncURL *string,
	liteLLMSyncURL *string,
	openRouterSyncURL *string,
	setupResolver ...SetupResolver,
) *Service {
	var resolver SetupResolver
	if len(setupResolver) > 0 {
		resolver = setupResolver[0]
	}
	sources := make([]priceSyncSource, 0, 3)
	if modelsDevSyncURL != nil && strings.TrimSpace(*modelsDevSyncURL) != "" {
		modelsDevCache := &modelsDevPriceCache{}
		sources = append(sources, priceSyncSource{
			Source: SyncSourceModelsDev,
			URL:    modelsDevSyncURL,
			Fetch:  modelsDevCache.fetchSource,
		})
	}
	sources = append(sources, priceSyncSource{
		Source: SyncSourceLiteLLM,
		URL:    liteLLMSyncURL,
		Fetch:  wrapModelPriceMapFetcher(fetchLiteLLMModelPrices),
	})
	if openRouterSyncURL != nil {
		sources = append(sources, priceSyncSource{
			Source: SyncSourceOpenRouter,
			URL:    openRouterSyncURL,
			Fetch:  wrapModelPriceMapFetcher(fetchOpenRouterModelPrices),
		})
	}
	return &Service{
		store:                        store,
		syncSources:                  sources,
		syncSourceTimeout:            defaultSyncSourceTimeout,
		syncProxyTimeout:             defaultSyncProxyResolutionTimeout,
		runtimeModelDiscoveryTimeout: defaultRuntimeModelDiscoveryTimeout,
		setupResolver:                resolver,
	}
}

func (s *Service) List(ctx context.Context) (map[string]store.ModelPrice, error) {
	return s.store.LoadModelPrices(ctx)
}

func (s *Service) UsageSummary(ctx context.Context, limit int) (store.ModelUsageSummary, error) {
	return s.store.ModelUsageSummary(ctx, limit)
}

func (s *Service) SetPricesChangedNotifier(notifier func()) {
	s.notifierMu.Lock()
	s.pricesChangedNotifier = notifier
	s.notifierMu.Unlock()
}

func (s *Service) notifyPricesChanged() {
	s.notifierMu.RLock()
	notifier := s.pricesChangedNotifier
	s.notifierMu.RUnlock()
	if notifier != nil {
		notifier()
	}
}

func (s *Service) Replace(ctx context.Context, prices map[string]store.ModelPrice) (map[string]store.ModelPrice, error) {
	if prices == nil {
		return nil, errors.New("prices are required")
	}
	if err := s.store.SaveModelPrices(ctx, prices); err != nil {
		return nil, err
	}
	s.notifyPricesChanged()
	return s.store.LoadModelPrices(ctx)
}

func (s *Service) Sync(ctx context.Context, req SyncRequest) (SyncResult, error) {
	effectiveModels := req.Models
	runtimeModelCount := 0
	var runtimeDiscoveryErr error

	if req.IncludeRuntimeModels {
		knownModels := normalizedRequestedModels(req.Models)
		discoveryTimeout := s.runtimeModelDiscoveryTimeout
		if discoveryTimeout <= 0 {
			discoveryTimeout = defaultRuntimeModelDiscoveryTimeout
		}
		discoveryCtx, cancel := context.WithTimeout(ctx, discoveryTimeout)
		runtimeModels, err := s.DiscoverRuntimeModels(discoveryCtx)
		cancel()

		if err != nil {
			runtimeDiscoveryErr = err
			effectiveModels = knownModels
		} else {
			runtimeModelCount = len(runtimeModels)
			allModels := make([]string, 0, len(knownModels)+len(runtimeModels))
			allModels = append(allModels, knownModels...)
			allModels = append(allModels, runtimeModels...)
			effectiveModels = normalizedRequestedModels(allModels)
		}

		if len(effectiveModels) == 0 {
			prices, err := s.store.LoadModelPrices(ctx)
			if err != nil {
				return SyncResult{}, err
			}
			discoveryErrStr := ""
			if runtimeDiscoveryErr != nil {
				discoveryErrStr = runtimeDiscoveryErr.Error()
			}
			return SyncResult{
				Prices:                     prices,
				RuntimeModelCount:          runtimeModelCount,
				RuntimeModelDiscoveryError: discoveryErrStr,
			}, nil
		}
	}

	client, proxyUsed, err := s.syncHTTPClient(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	remotePrices, skipped, sources, sourceResults, err := s.fetchAllModelPrices(ctx, client, effectiveModels)
	if err != nil {
		return SyncResult{}, err
	}
	selection := selectModelPriceCollection(remotePrices, effectiveModels)
	preserved := []string(nil)
	existingPrices, err := s.store.LoadModelPrices(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	if hasFailedSyncSource(sourceResults) {
		selection, preserved = preserveFailedSourcePrices(selection, existingPrices, sourceResults, effectiveModels)
	}

	// B1 & B2: Save detected candidate prices into model_price_candidates without directly overwriting active model_prices
	nowMS := time.Now().UnixMilli()
	var candidatesToSave []model.ModelPriceCandidate
	importedCount := 0

	for modelID, candidatePrice := range selection.Prices {
		provider := candidatePrice.Provider
		if provider == "" {
			provider = "default"
		}
		candHash := computePriceHash(provider, modelID, candidatePrice)

		var activePrice *store.ModelPrice
		activeHash := ""
		if act, ok := existingPrices[modelID]; ok {
			activePrice = &act
			actProv := act.Provider
			if actProv == "" {
				actProv = provider
			}
			activeHash = computePriceHash(actProv, modelID, act)
		}

		changeKind, status := detectPriceChange(activePrice, candidatePrice, activeHash != "" && activeHash == candHash)

		// Protect manual prices
		if activePrice != nil && strings.ToLower(strings.TrimSpace(activePrice.Source)) == "manual" {
			delete(selection.Matched, modelID)
			continue
		}

		candRecord := model.ModelPriceCandidate{
			Provider:           provider,
			Model:              modelID,
			Capability:         candidatePrice.Capability,
			BillingUnit:        candidatePrice.BillingUnit,
			PromptPer1M:        candidatePrice.Prompt,
			CompletionPer1M:    candidatePrice.Completion,
			CachePer1M:         candidatePrice.Cache,
			CacheReadPer1M:     candidatePrice.CacheRead,
			CacheCreationPer1M: candidatePrice.CacheCreation,
			UnitPrice:          candidatePrice.UnitPrice,
			Source:             candidatePrice.Source,
			SourceModelID:      candidatePrice.SourceModelID,
			RawJSON:            candidatePrice.RawJSON,
			ActivePriceHash:    activeHash,
			CandidatePriceHash: candHash,
			ChangeKind:         changeKind,
			Status:             status,
			DetectedAtMS:       nowMS,
		}
		candidatesToSave = append(candidatesToSave, candRecord)
		importedCount++

		if status == model.CandidateStatusBlocked {
			_ = s.FreezeModel(ctx, provider, modelID, "system_sync", "critical price change detected: "+changeKind)
		}
	}

	if err := s.store.SavePriceCandidates(ctx, candidatesToSave); err != nil {
		return SyncResult{}, err
	}

	if req.AutoApprove {
		candidates, _, _ := s.store.ListPriceCandidates(ctx, modelpricerepo.CandidateFilter{
			Status: model.CandidateStatusPending,
		})
		for _, cand := range candidates {
			if cand.ChangeKind != model.ChangeKindPriceIncrease && cand.ChangeKind != model.ChangeKindFreeToPaid && cand.ChangeKind != model.ChangeKindUnitChanged && cand.ChangeKind != model.ChangeKindModelRemapped {
				_, _ = s.ApproveCandidate(ctx, cand.ID, "auto_approve", "safe change auto approved")
			}
		}
	}

	if importedCount > 0 {
		s.notifyPricesChanged()
	}
	prices, err := s.store.LoadModelPrices(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	responsePrices := make(map[string]store.ModelPrice, len(prices)+len(selection.Prices))
	for k, v := range prices {
		responsePrices[k] = v
	}
	for k, v := range selection.Prices {
		if act, ok := prices[k]; ok && strings.ToLower(strings.TrimSpace(act.Source)) == "manual" {
			continue
		}
		responsePrices[k] = v
	}
	selection = filterSelectionFeedback(selection, responsePrices)
	discoveryErrStr := ""
	if runtimeDiscoveryErr != nil {
		discoveryErrStr = runtimeDiscoveryErr.Error()
	}
	return SyncResult{
		Source:                     syncResultSource(sources),
		Sources:                    sources,
		Imported:                   importedCount,
		Skipped:                    skipped,
		Matched:                    selection.Matched,
		Candidates:                 selection.Candidates,
		Unmatched:                  selection.Unmatched,
		Preserved:                  preserved,
		ProxyUsed:                  proxyUsed,
		SourceResults:              sourceResults,
		Prices:                     responsePrices,
		RuntimeModelCount:          runtimeModelCount,
		RuntimeModelDiscoveryError: discoveryErrStr,
	}, nil
}

type RuntimeModelPricingStatus struct {
	Models         []string `json:"models"`
	UnpricedModels []string `json:"unpricedModels"`
	Count          int      `json:"count"`
	UnpricedCount  int      `json:"unpricedCount"`
}

func (s *Service) RuntimeModelPricingStatus(ctx context.Context) (RuntimeModelPricingStatus, error) {
	discoveryTimeout := s.runtimeModelDiscoveryTimeout
	if discoveryTimeout <= 0 {
		discoveryTimeout = defaultRuntimeModelDiscoveryTimeout
	}
	discoveryCtx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	models, err := s.DiscoverRuntimeModels(discoveryCtx)
	cancel()
	if err != nil {
		return RuntimeModelPricingStatus{}, err
	}

	prices, err := s.store.LoadModelPrices(ctx)
	if err != nil {
		return RuntimeModelPricingStatus{}, err
	}

	normalizedModels := normalizedRequestedModels(models)
	sort.Strings(normalizedModels)

	unpricedModels := make([]string, 0, len(normalizedModels))
	for _, m := range normalizedModels {
		if _, exists := prices[m]; !exists {
			unpricedModels = append(unpricedModels, m)
		}
	}

	if normalizedModels == nil {
		normalizedModels = []string{}
	}
	if unpricedModels == nil {
		unpricedModels = []string{}
	}

	return RuntimeModelPricingStatus{
		Models:         normalizedModels,
		UnpricedModels: unpricedModels,
		Count:          len(normalizedModels),
		UnpricedCount:  len(unpricedModels),
	}, nil
}

func computePriceHash(provider, modelName string, p store.ModelPrice) string {
	raw := fmt.Sprintf("%s|%s|%s|%s|%.6f|%.6f|%.6f|%.6f|%.6f|%.6f",
		strings.ToLower(strings.TrimSpace(provider)),
		strings.ToLower(strings.TrimSpace(modelName)),
		strings.ToLower(strings.TrimSpace(p.Capability)),
		strings.ToLower(strings.TrimSpace(p.BillingUnit)),
		round6(p.Prompt),
		round6(p.Completion),
		round6(p.Cache),
		round6(p.CacheRead),
		round6(p.CacheCreation),
		round6(p.UnitPrice),
	)
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

func round6(val float64) float64 {
	return math.Round(val*1e6) / 1e6
}

func detectPriceChange(active *store.ModelPrice, cand store.ModelPrice, hashesMatch bool) (string, string) {
	if active == nil {
		return model.ChangeKindUnknownToKnown, model.CandidateStatusPending
	}

	if hashesMatch {
		return model.ChangeKindUnchanged, model.CandidateStatusUnchanged
	}

	// 1. Billing unit change: CRITICAL -> BLOCKED
	if strings.ToLower(strings.TrimSpace(active.BillingUnit)) != strings.ToLower(strings.TrimSpace(cand.BillingUnit)) &&
		active.BillingUnit != "" && cand.BillingUnit != "" {
		return model.ChangeKindUnitChanged, model.CandidateStatusBlocked
	}

	// 2. Free to Paid: 0 -> non-0: CRITICAL -> BLOCKED
	wasFree := active.Prompt == 0 && active.Completion == 0 && active.UnitPrice == 0
	isPaid := cand.Prompt > 0 || cand.Completion > 0 || cand.UnitPrice > 0
	if wasFree && isPaid {
		return model.ChangeKindFreeToPaid, model.CandidateStatusBlocked
	}

	// 3. Upstream model remapping: CRITICAL -> BLOCKED
	if active.SourceModelID != "" && cand.SourceModelID != "" &&
		strings.ToLower(strings.TrimSpace(active.SourceModelID)) != strings.ToLower(strings.TrimSpace(cand.SourceModelID)) {
		return model.ChangeKindModelRemapped, model.CandidateStatusBlocked
	}

	// 4. Paid to Free: non-0 -> 0: PENDING
	if !wasFree && !isPaid {
		return model.ChangeKindPaidToFree, model.CandidateStatusPending
	}

	// 5. Price increase: PENDING with warning
	if cand.Prompt > active.Prompt || cand.Completion > active.Completion || cand.UnitPrice > active.UnitPrice {
		return model.ChangeKindPriceIncrease, model.CandidateStatusPending
	}

	// 6. Price decrease: PENDING
	if cand.Prompt < active.Prompt || cand.Completion < active.Completion || cand.UnitPrice < active.UnitPrice {
		return model.ChangeKindPriceDecrease, model.CandidateStatusPending
	}

	return model.ChangeKindUnchanged, model.CandidateStatusUnchanged
}

func (s *Service) ApproveCandidate(ctx context.Context, id int64, reviewer, reason string) (*model.ModelPriceCandidate, error) {
	candidate, err := s.store.GetPriceCandidateByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if candidate == nil {
		return nil, errors.New("candidate not found")
	}
	if candidate.Status == model.CandidateStatusApproved {
		return candidate, nil
	}

	snapshot, err := s.store.ApplyApprovedPriceCandidate(ctx, *candidate, reviewer, reason)
	if err != nil {
		return nil, err
	}
	s.notifyPricesChanged()

	candidate.Status = model.CandidateStatusApproved
	nowMS := snapshot.CreatedAtMS
	candidate.ReviewedAtMS = &nowMS
	candidate.ReviewedBy = reviewer
	candidate.ReviewReason = reason

	return candidate, nil
}

func (s *Service) RejectCandidate(ctx context.Context, id int64, reviewer, reason string) (*model.ModelPriceCandidate, error) {
	candidate, err := s.store.GetPriceCandidateByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if candidate == nil {
		return nil, errors.New("candidate not found")
	}
	if candidate.Status == model.CandidateStatusRejected {
		return candidate, nil
	}

	if err := s.store.UpdatePriceCandidateReview(ctx, id, model.CandidateStatusRejected, reviewer, reason); err != nil {
		return nil, err
	}

	_ = s.store.RecordPriceAuditEvent(ctx, model.ModelPriceAuditEvent{
		Action:      "reject",
		Provider:    candidate.Provider,
		Model:       candidate.Model,
		CandidateID: &candidate.ID,
		Operator:    reviewer,
		Reason:      reason,
		CreatedAtMS: time.Now().UnixMilli(),
	})

	candidate.Status = model.CandidateStatusRejected
	nowMS := time.Now().UnixMilli()
	candidate.ReviewedAtMS = &nowMS
	candidate.ReviewedBy = reviewer
	candidate.ReviewReason = reason

	return candidate, nil
}

func (s *Service) FreezeModel(ctx context.Context, provider, modelName, operator, reason string) error {
	nowMS := time.Now().UnixMilli()
	err := s.store.SetPriceFreezeStatus(ctx, model.ModelPriceFreezeStatus{
		Provider:     provider,
		Model:        modelName,
		IsFrozen:     true,
		FrozenReason: reason,
		FrozenAtMS:   &nowMS,
		FrozenBy:     operator,
	})
	if err != nil {
		return err
	}
	_ = s.store.RecordPriceAuditEvent(ctx, model.ModelPriceAuditEvent{
		Action:      "freeze",
		Provider:    provider,
		Model:       modelName,
		Operator:    operator,
		Reason:      reason,
		CreatedAtMS: nowMS,
	})
	s.notifyPricesChanged()
	return nil
}

func (s *Service) UnfreezeModel(ctx context.Context, provider, modelName, operator, reason string) error {
	if operator == "" {
		return errors.New("operator required for unfreezing")
	}
	prices, err := s.store.LoadModelPrices(ctx)
	if err != nil {
		return err
	}
	if _, exists := prices[modelName]; !exists {
		return fmt.Errorf("cannot unfreeze: model %s has no active price", modelName)
	}

	candidates, _, err := s.store.ListPriceCandidates(ctx, modelpricerepo.CandidateFilter{
		Provider: provider,
		Model:    modelName,
		Status:   model.CandidateStatusBlocked,
	})
	if err != nil {
		return err
	}
	if len(candidates) > 0 {
		return fmt.Errorf("cannot unfreeze: model %s has %d unreviewed blocked candidates; review candidates first", modelName, len(candidates))
	}

	nowMS := time.Now().UnixMilli()
	err = s.store.SetPriceFreezeStatus(ctx, model.ModelPriceFreezeStatus{
		Provider:     provider,
		Model:        modelName,
		IsFrozen:     false,
		FrozenReason: "",
		FrozenAtMS:   nil,
		FrozenBy:     "",
	})
	if err != nil {
		return err
	}
	_ = s.store.RecordPriceAuditEvent(ctx, model.ModelPriceAuditEvent{
		Action:      "unfreeze",
		Provider:    provider,
		Model:       modelName,
		Operator:    operator,
		Reason:      reason,
		CreatedAtMS: nowMS,
	})
	s.notifyPricesChanged()
	return nil
}

func (s *Service) ListCandidates(ctx context.Context, filter modelpricerepo.CandidateFilter) ([]model.ModelPriceCandidate, int64, error) {
	return s.store.ListPriceCandidates(ctx, filter)
}

func (s *Service) GetCandidate(ctx context.Context, id int64) (*model.ModelPriceCandidate, error) {
	return s.store.GetPriceCandidateByID(ctx, id)
}

func (s *Service) ListSnapshots(ctx context.Context, provider, modelName string, limit, offset int) ([]model.ModelPriceSnapshot, int64, error) {
	return s.store.ListPriceSnapshots(ctx, provider, modelName, limit, offset)
}

func (s *Service) ListAuditEvents(ctx context.Context, provider, modelName string, limit, offset int) ([]model.ModelPriceAuditEvent, int64, error) {
	return s.store.ListPriceAuditEvents(ctx, provider, modelName, limit, offset)
}

func (s *Service) GetFreezeStatus(ctx context.Context, provider, modelName string) (*model.ModelPriceFreezeStatus, error) {
	return s.store.GetPriceFreezeStatus(ctx, provider, modelName)
}

func (s *Service) ListFrozenModels(ctx context.Context) ([]model.ModelPriceFreezeStatus, error) {
	return s.store.ListPriceFrozenModels(ctx)
}

func policyToSnapshot(p *model.ModelPricePolicy) *PolicySnapshot {
	return &PolicySnapshot{
		SchemaVersion:            "cost-policy/v1",
		PolicyVersion:            p.PolicyVersion,
		Provider:                 p.Provider,
		PublicModel:              p.Model,
		UpstreamModelHash:        p.UpstreamModelHash,
		PriceStatus:              p.PriceStatus,
		BillingUnit:              p.BillingUnit,
		PromptPer1M:              p.PromptPer1M,
		CompletionPer1M:          p.CompletionPer1M,
		CachePer1M:               p.CachePer1M,
		CacheReadPer1M:           p.CacheReadPer1M,
		CacheCreationPer1M:       p.CacheCreationPer1M,
		UnitPrice:                p.UnitPrice,
		MaxRequestCostUSD:        p.MaxRequestCostUSD,
		ProviderDailyBudgetUSD:   p.ProviderDailyBudgetUSD,
		ProviderMonthlyBudgetUSD: p.ProviderMonthlyBudgetUSD,
		GlobalDailyBudgetUSD:     p.GlobalDailyBudgetUSD,
		GlobalMonthlyBudgetUSD:   p.GlobalMonthlyBudgetUSD,
		EffectiveAtMS:            p.EffectiveAtMS,
		ExpiresAtMS:              p.ExpiresAtMS,
		Source:                   p.Source,
		ContentHash:              p.ContentHash,
		Signature:                p.Signature,
	}
}

func (s *Service) ExportApprovedPolicySnapshot(ctx context.Context, provider, modelName, operator string) (*PolicySnapshot, error) {
	if strings.TrimSpace(provider) == "" {
		return nil, errors.New("provider is required")
	}
	if strings.TrimSpace(modelName) == "" {
		return nil, errors.New("model name is required")
	}

	freezeStatus, err := s.store.GetPriceFreezeStatus(ctx, provider, modelName)
	if err != nil {
		return nil, err
	}
	if freezeStatus != nil && freezeStatus.IsFrozen {
		return nil, fmt.Errorf("cannot export policy: model %s under provider %s is frozen", modelName, provider)
	}

	candidates, _, err := s.store.ListPriceCandidates(ctx, modelpricerepo.CandidateFilter{
		Provider: provider,
		Model:    modelName,
		Status:   model.CandidateStatusBlocked,
	})
	if err != nil {
		return nil, err
	}
	if len(candidates) > 0 {
		return nil, fmt.Errorf("cannot export policy: model %s has unreviewed blocked candidate", modelName)
	}

	prices, err := s.store.LoadModelPrices(ctx)
	if err != nil {
		return nil, err
	}
	activePrice, exists := prices[modelName]
	if !exists {
		return nil, fmt.Errorf("cannot export policy: model %s has no active price", modelName)
	}

	priceStatus := "paid_confirmed"
	if activePrice.Prompt == 0 && activePrice.Completion == 0 && activePrice.Cache == 0 &&
		activePrice.CacheRead == 0 && activePrice.CacheCreation == 0 && activePrice.UnitPrice == 0 {
		priceStatus = "free_confirmed"
	}
	billingUnit := activePrice.BillingUnit
	if strings.TrimSpace(billingUnit) == "" {
		billingUnit = "tokens"
	}

	_, total, err := s.store.ListPricePolicies(ctx, modelpricerepo.PolicyFilter{
		Provider: provider,
		Model:    modelName,
	})
	if err != nil {
		return nil, err
	}

	activePolicy, err := s.store.GetActivePricePolicy(ctx, provider, modelName)
	if err != nil {
		return nil, err
	}

	upstreamHash := activePrice.SourceModelID
	if upstreamHash == "" {
		upstreamHash = fmt.Sprintf("upstream-%s-%s", provider, modelName)
	}

	nowMS := time.Now().UnixMilli()
	revision := total + 1
	policyVersion := fmt.Sprintf("cpamp-%s-%s-r%d", provider, modelName, revision)

	draft := &PolicySnapshot{
		SchemaVersion:            "cost-policy/v1",
		PolicyVersion:            policyVersion,
		Provider:                 provider,
		PublicModel:              modelName,
		UpstreamModelHash:        upstreamHash,
		PriceStatus:              priceStatus,
		BillingUnit:              billingUnit,
		PromptPer1M:              activePrice.Prompt,
		CompletionPer1M:          activePrice.Completion,
		CachePer1M:               activePrice.Cache,
		CacheReadPer1M:           activePrice.CacheRead,
		CacheCreationPer1M:       activePrice.CacheCreation,
		UnitPrice:                activePrice.UnitPrice,
		MaxRequestCostUSD:        10.0,
		ProviderDailyBudgetUSD:   100.0,
		ProviderMonthlyBudgetUSD: 2000.0,
		GlobalDailyBudgetUSD:     500.0,
		GlobalMonthlyBudgetUSD:   10000.0,
		EffectiveAtMS:            nowMS,
		ExpiresAtMS:              0,
		Source:                   "cpamp-approved",
	}

	if activePolicy != nil && activePolicy.Status == "active" {
		if activePolicy.PromptPer1M == draft.PromptPer1M &&
			activePolicy.CompletionPer1M == draft.CompletionPer1M &&
			activePolicy.UnitPrice == draft.UnitPrice &&
			activePolicy.BillingUnit == draft.BillingUnit &&
			activePolicy.PriceStatus == draft.PriceStatus {
			return policyToSnapshot(activePolicy), nil
		}
	}

	draft.ContentHash = draft.ComputeContentHash()
	if err := draft.Validate(); err != nil {
		return nil, fmt.Errorf("policy validation failed: %w", err)
	}

	snapshots, _, _ := s.store.ListPriceSnapshots(ctx, provider, modelName, 1, 0)
	var snapshotID int64
	if len(snapshots) > 0 {
		snapshotID = snapshots[0].ID
	}

	record := model.ModelPricePolicy{
		SnapshotID:               snapshotID,
		PolicyVersion:            draft.PolicyVersion,
		Provider:                 draft.Provider,
		Model:                    draft.PublicModel,
		UpstreamModelHash:        draft.UpstreamModelHash,
		PriceStatus:              draft.PriceStatus,
		BillingUnit:              draft.BillingUnit,
		PromptPer1M:              draft.PromptPer1M,
		CompletionPer1M:          draft.CompletionPer1M,
		CachePer1M:               draft.CachePer1M,
		CacheReadPer1M:           draft.CacheReadPer1M,
		CacheCreationPer1M:       draft.CacheCreationPer1M,
		UnitPrice:                draft.UnitPrice,
		MaxRequestCostUSD:        draft.MaxRequestCostUSD,
		ProviderDailyBudgetUSD:   draft.ProviderDailyBudgetUSD,
		ProviderMonthlyBudgetUSD: draft.ProviderMonthlyBudgetUSD,
		GlobalDailyBudgetUSD:     draft.GlobalDailyBudgetUSD,
		GlobalMonthlyBudgetUSD:   draft.GlobalMonthlyBudgetUSD,
		EffectiveAtMS:            draft.EffectiveAtMS,
		ExpiresAtMS:              draft.ExpiresAtMS,
		Source:                   draft.Source,
		ContentHash:              draft.ContentHash,
		Signature:                draft.Signature,
		Status:                   "active",
		CreatedAtMS:              nowMS,
		CreatedBy:                operator,
	}

	id, err := s.store.SavePricePolicy(ctx, record)
	if err != nil {
		return nil, err
	}
	record.ID = id

	_ = s.store.RecordPriceAuditEvent(ctx, model.ModelPriceAuditEvent{
		Action:      "export_policy",
		Provider:    provider,
		Model:       modelName,
		SnapshotID:  &snapshotID,
		Operator:    operator,
		Reason:      "policy snapshot exported",
		DetailsJSON: fmt.Sprintf(`{"policy_version":"%s","content_hash":"%s"}`, draft.PolicyVersion, draft.ContentHash),
		CreatedAtMS: nowMS,
	})

	return policyToSnapshot(&record), nil
}

func (s *Service) ListApprovedPolicySnapshots(ctx context.Context, filter modelpricerepo.PolicyFilter) ([]model.ModelPricePolicy, int64, error) {
	return s.store.ListPricePolicies(ctx, filter)
}

func (s *Service) GetPolicySnapshot(ctx context.Context, id int64) (*model.ModelPricePolicy, *PolicySnapshot, error) {
	policy, err := s.store.GetPricePolicyByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if policy == nil {
		return nil, nil, errors.New("policy not found")
	}
	return policy, policyToSnapshot(policy), nil
}

func (s *Service) RevokePolicySnapshot(ctx context.Context, id int64, operator, reason string) error {
	policy, err := s.store.GetPricePolicyByID(ctx, id)
	if err != nil {
		return err
	}
	if policy == nil {
		return errors.New("policy not found")
	}
	if policy.Status == "revoked" {
		return nil
	}
	nowMS := time.Now().UnixMilli()
	if err := s.store.UpdatePricePolicyStatus(ctx, id, "revoked", operator, reason, nowMS); err != nil {
		return err
	}
	_ = s.store.RecordPriceAuditEvent(ctx, model.ModelPriceAuditEvent{
		Action:      "revoke_policy",
		Provider:    policy.Provider,
		Model:       policy.Model,
		Operator:    operator,
		Reason:      reason,
		DetailsJSON: fmt.Sprintf(`{"policy_version":"%s"}`, policy.PolicyVersion),
		CreatedAtMS: nowMS,
	})
	return nil
}

func (s *Service) RollbackPolicySnapshot(ctx context.Context, id int64, operator, reason string) (*PolicySnapshot, error) {
	curr, err := s.store.GetPricePolicyByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if curr == nil {
		return nil, errors.New("policy not found")
	}

	prev, err := s.store.GetPreviousApprovedPricePolicy(ctx, curr.Provider, curr.Model, curr.ID)
	if err != nil {
		return nil, err
	}
	if prev == nil {
		return nil, fmt.Errorf("no previous approved policy found for %s/%s", curr.Provider, curr.Model)
	}

	nowMS := time.Now().UnixMilli()
	if prev.ExpiresAtMS > 0 && prev.ExpiresAtMS <= nowMS {
		return nil, errors.New("previous approved policy has expired, cannot rollback")
	}

	_ = s.store.UpdatePricePolicyStatus(ctx, curr.ID, "rolled_back", operator, reason, nowMS)

	totalPolicies, _, _ := s.store.ListPricePolicies(ctx, modelpricerepo.PolicyFilter{
		Provider: prev.Provider,
		Model:    prev.Model,
	})
	newVersion := fmt.Sprintf("cpamp-%s-%s-r%d", prev.Provider, prev.Model, len(totalPolicies)+1)

	newPolicy := model.ModelPricePolicy{
		SnapshotID:               prev.SnapshotID,
		PolicyVersion:            newVersion,
		Provider:                 prev.Provider,
		Model:                    prev.Model,
		UpstreamModelHash:        prev.UpstreamModelHash,
		PriceStatus:              prev.PriceStatus,
		BillingUnit:              prev.BillingUnit,
		PromptPer1M:              prev.PromptPer1M,
		CompletionPer1M:          prev.CompletionPer1M,
		CachePer1M:               prev.CachePer1M,
		CacheReadPer1M:           prev.CacheReadPer1M,
		CacheCreationPer1M:       prev.CacheCreationPer1M,
		UnitPrice:                prev.UnitPrice,
		MaxRequestCostUSD:        prev.MaxRequestCostUSD,
		ProviderDailyBudgetUSD:   prev.ProviderDailyBudgetUSD,
		ProviderMonthlyBudgetUSD: prev.ProviderMonthlyBudgetUSD,
		GlobalDailyBudgetUSD:     prev.GlobalDailyBudgetUSD,
		GlobalMonthlyBudgetUSD:   prev.GlobalMonthlyBudgetUSD,
		EffectiveAtMS:            nowMS,
		ExpiresAtMS:              prev.ExpiresAtMS,
		Source:                   prev.Source,
		Status:                   "active",
		CreatedAtMS:              nowMS,
		CreatedBy:                operator,
	}

	snap := policyToSnapshot(&newPolicy)
	newPolicy.ContentHash = snap.ComputeContentHash()
	snap.ContentHash = newPolicy.ContentHash

	newID, err := s.store.SavePricePolicy(ctx, newPolicy)
	if err != nil {
		return nil, err
	}
	newPolicy.ID = newID

	_ = s.store.RecordPriceAuditEvent(ctx, model.ModelPriceAuditEvent{
		Action:      "rollback_policy",
		Provider:    newPolicy.Provider,
		Model:       newPolicy.Model,
		Operator:    operator,
		Reason:      reason,
		DetailsJSON: fmt.Sprintf(`{"from_version":"%s","to_version":"%s"}`, curr.PolicyVersion, newPolicy.PolicyVersion),
		CreatedAtMS: nowMS,
	})

	return snap, nil
}

func (s *Service) DiscoverRuntimeModels(ctx context.Context) ([]string, error) {
	if s.setupResolver == nil {
		return nil, errors.New("runtime model discovery failed: missing setup resolver")
	}
	setup, ok, err := s.setupResolver.ResolveSetup(ctx)
	if err != nil {
		return nil, fmt.Errorf("runtime model discovery failed: %w", err)
	}
	if !ok {
		return nil, errors.New("runtime model discovery failed: setup not configured")
	}
	upstreamURL := strings.TrimSpace(setup.CPAUpstreamURL)
	managementKey := strings.TrimSpace(setup.ManagementKey)
	if upstreamURL == "" || managementKey == "" {
		return nil, errors.New("runtime model discovery failed: CPA upstream URL or management key is missing")
	}

	apiKeys, err := cpa.FetchAPIKeys(ctx, upstreamURL, managementKey)
	if err != nil {
		return nil, fmt.Errorf("runtime model discovery failed: %w", err)
	}

	apiKey := ""
	for _, key := range apiKeys {
		if strings.TrimSpace(key) != "" {
			apiKey = strings.TrimSpace(key)
			break
		}
	}

	models, err := cpa.FetchModels(ctx, upstreamURL, apiKey)
	if err != nil {
		return nil, fmt.Errorf("runtime model discovery failed: %w", err)
	}
	return models, nil
}

func (s *Service) SyncFromLiteLLM(ctx context.Context, req SyncRequest) (SyncResult, error) {
	return s.Sync(ctx, req)
}

func (s *Service) fetchAllModelPrices(ctx context.Context, client *http.Client, models []string) (modelPriceCollection, int, []string, []SyncSourceResult, error) {
	remotePrices := modelPriceCollection{}
	requestedModels := normalizedRequestedModels(models)
	sources := make([]string, 0, len(s.syncSources))
	sourceResults := make([]SyncSourceResult, 0, len(s.syncSources))
	failures := []string{}
	totalSkipped := 0

	for _, source := range s.syncSources {
		syncURL := source.currentURL()
		result := SyncSourceResult{Source: source.Source}
		if syncURL == "" {
			result.Error = "model price sync failed: missing source URL"
			sourceResults = append(sourceResults, result)
			failures = append(failures, source.Source+": "+result.Error)
			continue
		}
		sourceCtx := ctx
		cancel := func() {}
		if s.syncSourceTimeout > 0 {
			sourceCtx, cancel = context.WithTimeout(ctx, s.syncSourceTimeout)
		}
		fetched, skipped, err := source.Fetch(sourceCtx, syncURL, client)
		cancel()
		result.Skipped = skipped
		if err != nil {
			result.Error = err.Error()
			sourceResults = append(sourceResults, result)
			failures = append(failures, source.Source+": "+err.Error())
			continue
		}
		result.Models = len(fetched.Prices)
		sourceResults = append(sourceResults, result)
		sources = append(sources, source.Source)
		totalSkipped += skipped
		remotePrices.Metadata.merge(fetched.Metadata)

		for _, modelID := range sortedPriceKeys(fetched.Prices) {
			price := fetched.Prices[modelID]
			if price.Source == "" {
				price.Source = source.Source
			}
			if price.SourceModelID == "" {
				price.SourceModelID = modelID
			}
			remotePrices.Entries = append(remotePrices.Entries, modelPriceSourceEntry{
				Key:   modelID,
				Price: price,
			})
		}
		if len(requestedModels) > 0 && modelPriceCollectionCoversRequested(remotePrices, requestedModels) {
			break
		}
	}

	if len(sources) == 0 {
		if len(failures) == 0 {
			failures = append(failures, "no price sync sources configured")
		}
		return modelPriceCollection{}, 0, nil, sourceResults, errors.New("model price sync failed; existing prices were not changed: " + strings.Join(failures, "; "))
	}
	return remotePrices, totalSkipped, sources, sourceResults, nil
}

// preserveFailedSourcePrices prevents a transient failure of a preferred
// source from automatically downgrading an existing price to a lower-priority
// source. A successful preferred-source response that omits a model still
// permits the normal fallback behavior.
func preserveFailedSourcePrices(
	selection priceSelectionResult,
	existingPrices map[string]store.ModelPrice,
	sourceResults []SyncSourceResult,
	requestedModels []string,
) (priceSelectionResult, []string) {
	failedSources := make(map[string]bool, len(sourceResults))
	for _, result := range sourceResults {
		if result.Error != "" {
			failedSources[result.Source] = true
		}
	}
	if len(failedSources) == 0 {
		return selection, nil
	}
	requestedScope := requestedModelScope(requestedModels)
	preserved := make([]string, 0)
	for modelID, existing := range existingPrices {
		if requestedScope != nil && !requestedScope[modelID] {
			continue
		}
		if !failedSources[existing.Source] {
			continue
		}
		candidate, hasCandidate := selection.Prices[modelID]
		if hasCandidate && modelPriceSourcePriority(existing.Source) >= modelPriceSourcePriority(candidate.Source) {
			continue
		}
		if hasCandidate {
			delete(selection.Prices, modelID)
			delete(selection.Matched, modelID)
		}
		preserved = append(preserved, modelID)
	}
	sort.Strings(preserved)
	return selection, preserved
}

func requestedModelScope(models []string) map[string]bool {
	if len(models) == 0 {
		return nil
	}
	scope := make(map[string]bool, len(models))
	for _, modelID := range models {
		if normalized := strings.TrimSpace(modelID); normalized != "" {
			scope[normalized] = true
		}
	}
	return scope
}

func hasFailedSyncSource(sourceResults []SyncSourceResult) bool {
	for _, result := range sourceResults {
		if result.Error != "" {
			return true
		}
	}
	return false
}

func (source priceSyncSource) currentURL() string {
	if source.URL == nil {
		return ""
	}
	return strings.TrimSpace(*source.URL)
}

func syncResultSource(sources []string) string {
	if len(sources) == 1 {
		return sources[0]
	}
	if len(sources) > 1 {
		return SyncSourceMulti
	}
	return ""
}

func (s *Service) syncHTTPClient(ctx context.Context) (*http.Client, bool, error) {
	proxyCtx := ctx
	cancel := func() {}
	if s.syncProxyTimeout > 0 {
		proxyCtx, cancel = context.WithTimeout(ctx, s.syncProxyTimeout)
	}
	proxyURL := s.resolveCPAProxyURL(proxyCtx)
	cancel()
	if proxyURL == "" {
		return defaultSyncHTTPClient(), false, nil
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, false, errors.New("model price sync failed: invalid proxy URL")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(parsed)
	return &http.Client{Timeout: 30 * time.Second, Transport: transport}, true, nil
}

func (s *Service) resolveCPAProxyURL(ctx context.Context) string {
	if s.setupResolver == nil {
		return ""
	}
	setup, ok, err := s.setupResolver.ResolveSetup(ctx)
	if err != nil || !ok || setup.CPAUpstreamURL == "" || setup.ManagementKey == "" {
		return ""
	}
	cfg, err := cpa.FetchManagementConfig(ctx, setup.CPAUpstreamURL, setup.ManagementKey)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cfg.ProxyURL)
}

func defaultSyncHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

type modelsDevPriceCache struct {
	mu      sync.Mutex
	url     string
	etag    string
	fetched fetchedModelPriceSource
	skipped int
}

// fetchModelsDevModelPrices reads the provider-indexed models.dev catalog.
// models.dev prices are already expressed as USD per 1M tokens, unlike the
// token-level values published by LiteLLM and OpenRouter.
func fetchModelsDevModelPrices(ctx context.Context, syncURL string, client *http.Client) (map[string]store.ModelPrice, int, error) {
	fetched, skipped, err := fetchModelsDevPriceSource(ctx, syncURL, client)
	return fetched.Prices, skipped, err
}

func fetchModelsDevPriceSource(ctx context.Context, syncURL string, client *http.Client) (fetchedModelPriceSource, int, error) {
	res, err := fetchModelsDevResponse(ctx, syncURL, client, "")
	if err != nil {
		return fetchedModelPriceSource{}, 0, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotModified {
		return fetchedModelPriceSource{}, 0, errors.New("model price sync failed: unexpected 304 Not Modified")
	}
	return decodeModelsDevPriceSource(res.Body)
}

func (cache *modelsDevPriceCache) fetch(ctx context.Context, syncURL string, client *http.Client) (map[string]store.ModelPrice, int, error) {
	fetched, skipped, err := cache.fetchSource(ctx, syncURL, client)
	return fetched.Prices, skipped, err
}

func (cache *modelsDevPriceCache) fetchSource(ctx context.Context, syncURL string, client *http.Client) (fetchedModelPriceSource, int, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	if cache.url != syncURL {
		cache.url = syncURL
		cache.etag = ""
		cache.fetched = fetchedModelPriceSource{}
		cache.skipped = 0
	}

	res, err := fetchModelsDevResponse(ctx, syncURL, client, cache.etag)
	if err != nil {
		return fetchedModelPriceSource{}, 0, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotModified {
		if cache.fetched.Prices == nil {
			return fetchedModelPriceSource{}, 0, errors.New("model price sync failed: models.dev returned 304 without cached prices")
		}
		if etag := strings.TrimSpace(res.Header.Get("ETag")); etag != "" {
			cache.etag = etag
		}
		return cache.fetched, cache.skipped, nil
	}

	fetched, skipped, err := decodeModelsDevPriceSource(res.Body)
	if err != nil {
		return fetchedModelPriceSource{}, skipped, err
	}
	cache.etag = strings.TrimSpace(res.Header.Get("ETag"))
	cache.fetched = fetched
	cache.skipped = skipped
	return fetched, skipped, nil
}

func fetchModelsDevResponse(ctx context.Context, syncURL string, client *http.Client, etag string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, syncURL, nil)
	if err != nil {
		return nil, errors.New("model price sync failed: " + err.Error())
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if client == nil {
		client = defaultSyncHTTPClient()
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New("model price sync failed: " + err.Error())
	}
	if res.StatusCode == http.StatusNotModified {
		return res, nil
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		res.Body.Close()
		return nil, errors.New("model price sync failed: " + res.Status)
	}
	return res, nil
}

type modelsDevRawProvider struct {
	Models map[string]json.RawMessage `json:"models"`
}

func decodeModelsDevModelPrices(reader io.Reader) (map[string]store.ModelPrice, int, error) {
	fetched, skipped, err := decodeModelsDevPriceSource(reader)
	return fetched.Prices, skipped, err
}

func decodeModelsDevPriceSource(reader io.Reader) (fetchedModelPriceSource, int, error) {
	var root map[string]json.RawMessage
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		return fetchedModelPriceSource{}, 0, err
	}

	providerMessages := root
	canonicalModels := map[string]json.RawMessage(nil)
	if rawProviders, hasProviders := root["providers"]; hasProviders {
		rawModels, hasModels := root["models"]
		if !hasModels {
			return fetchedModelPriceSource{}, 0, errors.New("model price sync failed: models.dev catalog contained no canonical models")
		}
		var catalogProviders map[string]json.RawMessage
		if err := json.Unmarshal(rawProviders, &catalogProviders); err != nil {
			return fetchedModelPriceSource{}, 0, err
		}
		if err := json.Unmarshal(rawModels, &canonicalModels); err != nil {
			return fetchedModelPriceSource{}, 0, err
		}
		if len(canonicalModels) == 0 {
			return fetchedModelPriceSource{}, 0, errors.New("model price sync failed: models.dev catalog contained no canonical models")
		}
		providerMessages = catalogProviders
	}

	raw := make(map[string]modelsDevRawProvider, len(providerMessages))
	for providerID, message := range providerMessages {
		var provider modelsDevRawProvider
		if err := json.Unmarshal(message, &provider); err != nil {
			return fetchedModelPriceSource{}, 0, err
		}
		raw[providerID] = provider
	}

	now := time.Now().UnixMilli()
	prices := map[string]store.ModelPrice{}
	metadata := newModelsDevMatchMetadata(canonicalModels)
	skipped := 0
	providerIDs := make([]string, 0, len(raw))
	for providerID := range raw {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)

	for _, rawProviderID := range providerIDs {
		provider := raw[rawProviderID]
		providerID := strings.TrimSpace(rawProviderID)
		modelIDs := make([]string, 0, len(provider.Models))
		for modelID := range provider.Models {
			modelIDs = append(modelIDs, modelID)
		}
		sort.Strings(modelIDs)
		for _, rawModelID := range modelIDs {
			modelRaw := provider.Models[rawModelID]
			modelID := strings.TrimSpace(rawModelID)
			if providerID == "" || modelID == "" {
				skipped++
				continue
			}
			var entry map[string]any
			if err := json.Unmarshal(modelRaw, &entry); err != nil {
				skipped++
				continue
			}
			cost, ok := entry["cost"].(map[string]any)
			if !ok {
				skipped++
				continue
			}
			promptCost, hasPrompt := readFloat(cost, "input")
			completionCost, hasCompletion := readFloat(cost, "output")
			cacheReadCost, hasCacheRead := readFloat(cost, "cache_read")
			cacheCreationCost, hasCacheCreation := readFloat(cost, "cache_write")
			if !hasPrompt && !hasCompletion && !hasCacheRead && !hasCacheCreation {
				skipped++
				continue
			}

			sourceModelID := providerID + "/" + modelID
			price := store.ModelPrice{
				Prompt:                  promptCost,
				Completion:              completionCost,
				Cache:                   cacheReadCost,
				CacheRead:               cacheReadCost,
				CacheCreation:           cacheCreationCost,
				PromptConfigured:        hasPrompt,
				CompletionConfigured:    hasCompletion,
				CacheReadConfigured:     hasCacheRead,
				CacheCreationConfigured: hasCacheCreation,
				Source:                  SyncSourceModelsDev,
				SourceModelID:           sourceModelID,
				RawJSON:                 string(modelRaw),
				ContextTiers:            readModelsDevContextTiers(cost),
				ServiceTiers:            readModelsDevServiceTiers(entry),
				UpdatedAtMS:             now,
				SyncedAtMS:              &now,
			}
			prices[sourceModelID] = price
			if canonicalID, ok := metadata.modelsDevCanonicalByIdentity[normalizeModelPriceIdentity(sourceModelID)]; ok && strings.EqualFold(canonicalID, sourceModelID) {
				if metadata.modelsDevOfficialSourceModelIDs == nil {
					metadata.modelsDevOfficialSourceModelIDs = map[string]struct{}{}
				}
				metadata.modelsDevOfficialSourceModelIDs[normalizeModelPriceIdentity(sourceModelID)] = struct{}{}
			}
		}
	}
	if len(prices) == 0 {
		return fetchedModelPriceSource{}, skipped, errors.New("model price sync failed: models.dev catalog contained no usable prices")
	}

	return fetchedModelPriceSource{Prices: prices, Metadata: metadata}, skipped, nil
}

func readModelsDevContextTiers(cost map[string]any) []store.ModelPriceContextTier {
	rawTiers, ok := cost["tiers"].([]any)
	if !ok || len(rawTiers) == 0 {
		return nil
	}
	tiers := make([]store.ModelPriceContextTier, 0, len(rawTiers))
	for _, rawTier := range rawTiers {
		entry, ok := rawTier.(map[string]any)
		if !ok {
			continue
		}
		descriptor, ok := entry["tier"].(map[string]any)
		if !ok || !strings.EqualFold(readString(descriptor, "type"), "context") {
			continue
		}
		threshold, ok := readPositiveInt64(descriptor, "size")
		if !ok {
			return nil
		}
		prompt, hasPrompt := readFloat(entry, "input")
		completion, hasCompletion := readFloat(entry, "output")
		cacheRead, hasCacheRead := readFloat(entry, "cache_read")
		cacheCreation, hasCacheCreation := readFloat(entry, "cache_write")
		if !hasPrompt && !hasCompletion && !hasCacheRead && !hasCacheCreation {
			return nil
		}
		tiers = append(tiers, store.ModelPriceContextTier{
			ThresholdTokens:         threshold,
			Prompt:                  prompt,
			Completion:              completion,
			Cache:                   cacheRead,
			CacheRead:               cacheRead,
			CacheCreation:           cacheCreation,
			PromptConfigured:        hasPrompt,
			CompletionConfigured:    hasCompletion,
			CacheConfigured:         hasCacheRead,
			CacheReadConfigured:     hasCacheRead,
			CacheCreationConfigured: hasCacheCreation,
		})
	}
	normalized, err := model.NormalizeModelPriceContextTiers(tiers)
	if err != nil {
		return nil
	}
	return normalized
}

func readModelsDevServiceTiers(entry map[string]any) []store.ModelPriceServiceTier {
	experimental, ok := entry["experimental"].(map[string]any)
	if !ok {
		return nil
	}
	modes, ok := experimental["modes"].(map[string]any)
	if !ok {
		return nil
	}
	fast, ok := modes["fast"].(map[string]any)
	if !ok {
		return nil
	}
	cost, ok := fast["cost"].(map[string]any)
	if !ok {
		return nil
	}
	prompt, hasPrompt := readFloat(cost, "input")
	completion, hasCompletion := readFloat(cost, "output")
	cacheRead, hasCacheRead := readFloat(cost, "cache_read")
	cacheCreation, hasCacheCreation := readFloat(cost, "cache_write")
	if !hasPrompt && !hasCompletion && !hasCacheRead && !hasCacheCreation {
		return nil
	}
	tiers, err := model.NormalizeModelPriceServiceTiers([]store.ModelPriceServiceTier{{
		Mode:                    "fast",
		ServiceTier:             "priority",
		Prompt:                  prompt,
		Completion:              completion,
		Cache:                   cacheRead,
		CacheRead:               cacheRead,
		CacheCreation:           cacheCreation,
		PromptConfigured:        hasPrompt,
		CompletionConfigured:    hasCompletion,
		CacheConfigured:         hasCacheRead,
		CacheReadConfigured:     hasCacheRead,
		CacheCreationConfigured: hasCacheCreation,
	}})
	if err != nil {
		return nil
	}
	return tiers
}

func modelsDevModelID(sourceModelID string) (string, bool) {
	_, modelID, ok := strings.Cut(strings.TrimSpace(sourceModelID), "/")
	modelID = strings.TrimSpace(modelID)
	return modelID, ok && modelID != ""
}

func fetchLiteLLMModelPrices(ctx context.Context, syncURL string, client *http.Client) (map[string]store.ModelPrice, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, syncURL, nil)
	if err != nil {
		return nil, 0, errors.New("model price sync failed: " + err.Error())
	}
	if client == nil {
		client = defaultSyncHTTPClient()
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, 0, errors.New("model price sync failed: " + err.Error())
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, 0, errors.New("model price sync failed: " + res.Status)
	}
	var raw map[string]map[string]any
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return nil, 0, err
	}
	now := time.Now().UnixMilli()
	prices := map[string]store.ModelPrice{}
	skipped := 0
	for modelID, entry := range raw {
		promptCost, hasPrompt := readFloat(entry, "input_cost_per_token")
		completionCost, hasCompletion := readFloat(entry, "output_cost_per_token")
		cacheReadCost, hasCacheRead := readFirstFloat(entry, "cache_read_input_token_cost", "input_cache_read")
		cacheCreationCost, hasCacheCreation := readFirstFloat(entry, "cache_creation_input_token_cost", "cache_write_input_token_cost", "input_cache_write", "input_cache_creation")
		if !hasPrompt && !hasCompletion && !hasCacheRead && !hasCacheCreation {
			skipped++
			continue
		}
		rawEntry, _ := json.Marshal(entry)
		prices[modelID] = store.ModelPrice{
			Prompt:                  promptCost * 1_000_000,
			Completion:              completionCost * 1_000_000,
			Cache:                   cacheReadCost * 1_000_000,
			CacheRead:               cacheReadCost * 1_000_000,
			CacheCreation:           cacheCreationCost * 1_000_000,
			PromptConfigured:        hasPrompt,
			CompletionConfigured:    hasCompletion,
			CacheReadConfigured:     hasCacheRead,
			CacheCreationConfigured: hasCacheCreation,
			Source:                  SyncSourceLiteLLM,
			SourceModelID:           modelID,
			RawJSON:                 string(rawEntry),
			UpdatedAtMS:             now,
			SyncedAtMS:              &now,
		}
	}
	return prices, skipped, nil
}

func fetchOpenRouterModelPrices(ctx context.Context, syncURL string, client *http.Client) (map[string]store.ModelPrice, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, syncURL, nil)
	if err != nil {
		return nil, 0, errors.New("model price sync failed: " + err.Error())
	}
	if client == nil {
		client = defaultSyncHTTPClient()
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, 0, errors.New("model price sync failed: " + err.Error())
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, 0, errors.New("model price sync failed: " + res.Status)
	}

	var raw struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return nil, 0, err
	}

	now := time.Now().UnixMilli()
	prices := map[string]store.ModelPrice{}
	skipped := 0
	for _, entry := range raw.Data {
		modelID := readString(entry, "id")
		pricing, ok := entry["pricing"].(map[string]any)
		if modelID == "" || !ok {
			skipped++
			continue
		}
		promptCost, hasPrompt := readFloat(pricing, "prompt")
		completionCost, hasCompletion := readFloat(pricing, "completion")
		cacheReadCost, hasCacheRead := readFirstFloat(pricing, "input_cache_read", "cache_read_input_token_cost")
		cacheCreationCost, hasCacheCreation := readFirstFloat(pricing, "input_cache_write", "input_cache_creation", "cache_creation_input_token_cost", "cache_write_input_token_cost")
		if !hasPrompt && !hasCompletion && !hasCacheRead && !hasCacheCreation {
			skipped++
			continue
		}
		rawEntry, _ := json.Marshal(entry)
		prices[modelID] = store.ModelPrice{
			Prompt:                  promptCost * 1_000_000,
			Completion:              completionCost * 1_000_000,
			Cache:                   cacheReadCost * 1_000_000,
			CacheRead:               cacheReadCost * 1_000_000,
			CacheCreation:           cacheCreationCost * 1_000_000,
			PromptConfigured:        hasPrompt,
			CompletionConfigured:    hasCompletion,
			CacheReadConfigured:     hasCacheRead,
			CacheCreationConfigured: hasCacheCreation,
			Source:                  SyncSourceOpenRouter,
			SourceModelID:           modelID,
			RawJSON:                 string(rawEntry),
			UpdatedAtMS:             now,
			SyncedAtMS:              &now,
		}
	}
	return prices, skipped, nil
}

type priceSelectionResult struct {
	Prices     map[string]store.ModelPrice
	Matched    map[string]store.ModelPrice
	Candidates []SyncCandidateSet
	Unmatched  []string
}

type modelPriceEntry struct {
	key              string
	price            store.ModelPrice
	directIdentities []string
	aliasIdentities  []string
}

type modelPriceMatcher struct {
	entries     []modelPriceEntry
	exact       map[string][]int
	caseFold    map[string][]int
	aliasExact  map[string][]int
	aliasFold   map[string][]int
	tail        map[string][]int
	canonical   map[string][]int
	sourceEntry map[string][]int
	sources     []string
	metadata    modelPriceMatchMetadata
}

func newModelPriceMatcher(prices map[string]store.ModelPrice) *modelPriceMatcher {
	collection := modelPriceCollection{
		Entries: make([]modelPriceSourceEntry, 0, len(prices)),
	}
	for _, key := range sortedPriceKeys(prices) {
		collection.Entries = append(collection.Entries, modelPriceSourceEntry{
			Key:   key,
			Price: prices[key],
		})
	}
	return newModelPriceCollectionMatcher(collection)
}

func newModelPriceCollectionMatcher(collection modelPriceCollection) *modelPriceMatcher {
	matcher := &modelPriceMatcher{
		entries:     make([]modelPriceEntry, 0, len(collection.Entries)),
		exact:       make(map[string][]int, len(collection.Entries)),
		caseFold:    make(map[string][]int, len(collection.Entries)),
		aliasExact:  make(map[string][]int, len(collection.Entries)),
		aliasFold:   make(map[string][]int, len(collection.Entries)),
		tail:        make(map[string][]int, len(collection.Entries)),
		canonical:   make(map[string][]int, len(collection.Entries)),
		sourceEntry: make(map[string][]int, 4),
		metadata:    collection.Metadata,
	}
	for _, sourceEntry := range collection.Entries {
		key := sourceEntry.Key
		price := sourceEntry.Price
		directIdentities, aliasIdentities := modelPriceEntryIdentities(key, price)
		entryIndex := len(matcher.entries)
		matcher.entries = append(matcher.entries, modelPriceEntry{
			key:              key,
			price:            price,
			directIdentities: directIdentities,
			aliasIdentities:  aliasIdentities,
		})
		source := strings.TrimSpace(price.Source)
		if _, exists := matcher.sourceEntry[source]; !exists {
			matcher.sources = append(matcher.sources, source)
		}
		matcher.sourceEntry[source] = append(matcher.sourceEntry[source], entryIndex)
		for _, identity := range directIdentities {
			appendModelPriceIndex(matcher.exact, identity, entryIndex)
			appendModelPriceIndex(matcher.caseFold, strings.ToLower(identity), entryIndex)
			appendModelPriceIndex(matcher.tail, canonicalModelTail(identity), entryIndex)
			appendModelPriceIndex(matcher.canonical, canonicalModelID(identity), entryIndex)
		}
		for _, identity := range aliasIdentities {
			appendModelPriceIndex(matcher.aliasExact, identity, entryIndex)
			appendModelPriceIndex(matcher.aliasFold, strings.ToLower(identity), entryIndex)
			appendModelPriceIndex(matcher.tail, canonicalModelTail(identity), entryIndex)
			appendModelPriceIndex(matcher.canonical, canonicalModelID(identity), entryIndex)
		}
	}
	sort.SliceStable(matcher.sources, func(i, j int) bool {
		leftPriority := modelPriceSourcePriority(matcher.sources[i])
		rightPriority := modelPriceSourcePriority(matcher.sources[j])
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		return matcher.sources[i] < matcher.sources[j]
	})
	return matcher
}

func appendModelPriceIndex(index map[string][]int, identity string, entryIndex int) {
	if identity == "" {
		return
	}
	matches := index[identity]
	if len(matches) > 0 && matches[len(matches)-1] == entryIndex {
		return
	}
	index[identity] = append(matches, entryIndex)
}

func selectModelPrices(prices map[string]store.ModelPrice, models []string) priceSelectionResult {
	collection := modelPriceCollection{
		Entries: make([]modelPriceSourceEntry, 0, len(prices)),
	}
	for _, key := range sortedPriceKeys(prices) {
		collection.Entries = append(collection.Entries, modelPriceSourceEntry{
			Key:   key,
			Price: prices[key],
		})
	}
	return selectModelPriceCollection(collection, models)
}

func selectModelPriceCollection(collection modelPriceCollection, models []string) priceSelectionResult {
	result := priceSelectionResult{
		Prices:  map[string]store.ModelPrice{},
		Matched: map[string]store.ModelPrice{},
	}
	matcher := newModelPriceCollectionMatcher(collection)
	if len(models) == 0 {
		return matcher.selectAllUnambiguousModelPrices()
	}
	seen := map[string]bool{}
	for _, modelID := range models {
		normalized := strings.TrimSpace(modelID)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		price, _, ok, _ := matcher.findAutomaticModelPrice(normalized)
		if ok {
			result.Prices[normalized] = price
			result.Matched[normalized] = price
			continue
		}
		candidates := matcher.findCandidateModelPrices(normalized)
		if len(candidates) > 0 {
			result.Candidates = append(result.Candidates, SyncCandidateSet{
				Model:      normalized,
				Candidates: candidates,
			})
			continue
		}
		result.Unmatched = append(result.Unmatched, normalized)
	}
	return result
}

func selectAllUnambiguousModelPrices(prices map[string]store.ModelPrice) priceSelectionResult {
	return newModelPriceMatcher(prices).selectAllUnambiguousModelPrices()
}

func (matcher *modelPriceMatcher) selectAllUnambiguousModelPrices() priceSelectionResult {
	result := priceSelectionResult{
		Prices:  map[string]store.ModelPrice{},
		Matched: map[string]store.ModelPrice{},
	}
	modelIDs := map[string]struct{}{}
	for entryIndex := range matcher.entries {
		entry := &matcher.entries[entryIndex]
		if entry.price.Source == SyncSourceModelsDev {
			if modelID, ok := modelsDevModelID(entry.price.SourceModelID); ok {
				modelIDs[modelID] = struct{}{}
			}
			continue
		}
		modelIDs[entry.key] = struct{}{}
	}
	orderedModelIDs := make([]string, 0, len(modelIDs))
	for modelID := range modelIDs {
		orderedModelIDs = append(orderedModelIDs, modelID)
	}
	sort.Strings(orderedModelIDs)
	for _, modelID := range orderedModelIDs {
		price, _, ok, _ := matcher.findAutomaticModelPrice(modelID)
		if !ok {
			continue
		}
		result.Prices[modelID] = price
		result.Matched[modelID] = price
	}
	return result
}

func findAutomaticModelPrice(prices map[string]store.ModelPrice, modelID string) (store.ModelPrice, string, bool) {
	price, reason, ok, _ := newModelPriceMatcher(prices).findAutomaticModelPrice(modelID)
	return price, reason, ok
}

func (matcher *modelPriceMatcher) findAutomaticModelPrice(modelID string) (store.ModelPrice, string, bool, []int) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return store.ModelPrice{}, "", false, nil
	}
	allMatches := make([]int, 0)
	for _, source := range matcher.sources {
		matches, reason := matcher.indexedModelPriceMatchesForSource(modelID, source)
		allMatches = appendUniqueModelPriceIndexes(allMatches, matches...)
		if source == SyncSourceModelsDev && len(matcher.metadata.modelsDevCanonicalByIdentity) > 0 {
			if officialMatches := matcher.modelsDevOfficialMatches(modelID); len(officialMatches) == 1 {
				entry := matcher.entries[officialMatches[0]]
				return entry.price, "models.dev-official", true, allMatches
			}
			if strings.Contains(modelID, "/") {
				directMatches, directReason := matcher.directModelPriceMatchesForSource(modelID, source)
				allMatches = appendUniqueModelPriceIndexes(allMatches, directMatches...)
				if len(directMatches) == 1 {
					return matcher.entries[directMatches[0]].price, directReason, true, allMatches
				}
			}
			continue
		}
		if len(matches) == 1 {
			return matcher.entries[matches[0]].price, reason, true, allMatches
		}
	}
	return store.ModelPrice{}, "", false, allMatches
}

func (matcher *modelPriceMatcher) indexedModelPriceMatchesForSource(modelID string, source string) ([]int, string) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return nil, ""
	}
	if matches := matcher.filterIndexesBySource(matcher.exact[modelID], source); len(matches) > 0 {
		return matches, "exact"
	}
	if matches := matcher.filterIndexesBySource(matcher.caseFold[strings.ToLower(modelID)], source); len(matches) > 0 {
		return matches, "case-insensitive"
	}
	if matches := matcher.filterIndexesBySource(matcher.aliasExact[modelID], source); len(matches) > 0 {
		return matches, "source-model-id"
	}
	if matches := matcher.filterIndexesBySource(matcher.aliasFold[strings.ToLower(modelID)], source); len(matches) > 0 {
		return matches, "case-insensitive-source-model-id"
	}
	modelTail := canonicalModelTail(modelID)
	if modelTail != "" {
		if matches := matcher.filterIndexesBySource(matcher.tail[modelTail], source); len(matches) > 0 {
			return matches, "provider-prefix"
		}
	}
	modelCanonical := canonicalModelID(modelID)
	if modelCanonical != "" {
		if matches := matcher.filterIndexesBySource(matcher.canonical[modelCanonical], source); len(matches) > 0 {
			return matches, "normalized"
		}
	}
	return nil, ""
}

func (matcher *modelPriceMatcher) directModelPriceMatchesForSource(modelID string, source string) ([]int, string) {
	if matches := matcher.filterIndexesBySource(matcher.exact[modelID], source); len(matches) > 0 {
		return matches, "exact"
	}
	if matches := matcher.filterIndexesBySource(matcher.caseFold[strings.ToLower(modelID)], source); len(matches) > 0 {
		return matches, "case-insensitive"
	}
	return nil, ""
}

func (matcher *modelPriceMatcher) filterIndexesBySource(indexes []int, source string) []int {
	filtered := make([]int, 0, len(indexes))
	for _, entryIndex := range indexes {
		if strings.TrimSpace(matcher.entries[entryIndex].price.Source) == source {
			filtered = appendUniqueModelPriceIndexes(filtered, entryIndex)
		}
	}
	return filtered
}

func appendUniqueModelPriceIndexes(indexes []int, additions ...int) []int {
	for _, addition := range additions {
		exists := false
		for _, existing := range indexes {
			if existing == addition {
				exists = true
				break
			}
		}
		if !exists {
			indexes = append(indexes, addition)
		}
	}
	return indexes
}

func (matcher *modelPriceMatcher) modelsDevOfficialMatches(modelID string) []int {
	canonicalID, ok := matcher.metadata.modelsDevCanonicalModelID(modelID)
	if !ok {
		return nil
	}
	matches := make([]int, 0, 1)
	for _, entryIndex := range matcher.sourceEntry[SyncSourceModelsDev] {
		entry := &matcher.entries[entryIndex]
		sourceModelID := strings.TrimSpace(entry.price.SourceModelID)
		if !matcher.metadata.isModelsDevOfficialSourceModelID(sourceModelID) || !strings.EqualFold(sourceModelID, canonicalID) {
			continue
		}
		matches = append(matches, entryIndex)
	}
	return matches
}

func modelPriceSourcePriority(source string) int {
	switch source {
	case SyncSourceModelsDev:
		return 0
	case SyncSourceLiteLLM:
		return 1
	case SyncSourceOpenRouter:
		return 2
	default:
		return 3
	}
}

func modelPriceEntryIdentities(key string, price store.ModelPrice) ([]string, []string) {
	direct := make([]string, 0, 2)
	aliases := make([]string, 0, 1)
	add := func(target *[]string, identity string) {
		identity = strings.TrimSpace(identity)
		if identity == "" {
			return
		}
		for _, existing := range *target {
			if existing == identity {
				return
			}
		}
		*target = append(*target, identity)
	}
	add(&direct, key)
	add(&direct, price.SourceModelID)
	if price.Source == SyncSourceModelsDev {
		if modelID, ok := modelsDevModelID(price.SourceModelID); ok {
			add(&aliases, modelID)
		}
	}
	return direct, aliases
}

func findCandidateModelPrices(prices map[string]store.ModelPrice, modelID string) []SyncCandidate {
	return newModelPriceMatcher(prices).findCandidateModelPrices(modelID)
}

func (matcher *modelPriceMatcher) findCandidateModelPrices(modelID string) []SyncCandidate {
	candidates := make([]SyncCandidate, 0, maxSyncCandidates*len(matcher.sources))
	for _, source := range matcher.sources {
		indexes, _ := matcher.indexedModelPriceMatchesForSource(modelID, source)
		if len(indexes) == 0 {
			indexes = matcher.sourceEntry[source]
		}
		sourceCandidates := matcher.candidateModelPricesForIndexes(modelID, indexes)
		candidates = append(candidates, sourceCandidates...)
	}
	return candidates
}

func (matcher *modelPriceMatcher) candidateModelPricesForIndexes(modelID string, indexes []int) []SyncCandidate {
	candidates := make([]SyncCandidate, 0, maxSyncCandidates)
	for _, entryIndex := range indexes {
		candidates = appendModelPriceCandidate(candidates, modelID, &matcher.entries[entryIndex])
	}
	return matcher.sortAndLimitModelPriceCandidates(candidates)
}

func appendModelPriceCandidate(candidates []SyncCandidate, modelID string, entry *modelPriceEntry) []SyncCandidate {
	score := 0.0
	reason := ""
	for _, identity := range entry.directIdentities {
		candidateScore, candidateReason := modelIdentitySimilarity(modelID, identity)
		if candidateScore > score {
			score = candidateScore
			reason = candidateReason
		}
	}
	for _, identity := range entry.aliasIdentities {
		candidateScore, _ := modelIdentitySimilarity(modelID, identity)
		candidateScore = math.Min(candidateScore, 0.94)
		if candidateScore > score {
			score = candidateScore
			reason = "same-model-with-provider-prefix"
		}
	}
	if score < minCandidateScore && !(score >= minWeakCandidateScore && isWeakRecallReason(reason)) {
		return candidates
	}
	sourceModelID := strings.TrimSpace(entry.price.SourceModelID)
	if sourceModelID == "" {
		sourceModelID = entry.key
	}
	return append(candidates, SyncCandidate{
		SourceModelID: sourceModelID,
		Score:         math.Round(score*100) / 100,
		Reason:        reason,
		Price:         entry.price,
	})
}

func (matcher *modelPriceMatcher) sortAndLimitModelPriceCandidates(candidates []SyncCandidate) []SyncCandidate {
	sort.SliceStable(candidates, func(i, j int) bool {
		leftOfficial := candidates[i].Price.Source == SyncSourceModelsDev && matcher.metadata.isModelsDevOfficialSourceModelID(candidates[i].SourceModelID)
		rightOfficial := candidates[j].Price.Source == SyncSourceModelsDev && matcher.metadata.isModelsDevOfficialSourceModelID(candidates[j].SourceModelID)
		if leftOfficial != rightOfficial {
			return leftOfficial
		}
		if candidates[i].Score == candidates[j].Score {
			return candidates[i].SourceModelID < candidates[j].SourceModelID
		}
		return candidates[i].Score > candidates[j].Score
	})
	if len(candidates) > maxSyncCandidates {
		return candidates[:maxSyncCandidates]
	}
	return candidates
}

func normalizedRequestedModels(models []string) []string {
	normalized := make([]string, 0, len(models))
	seen := map[string]struct{}{}
	for _, modelID := range models {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" {
			continue
		}
		if _, exists := seen[modelID]; exists {
			continue
		}
		seen[modelID] = struct{}{}
		normalized = append(normalized, modelID)
	}
	return normalized
}

func modelPriceCollectionCoversRequested(collection modelPriceCollection, models []string) bool {
	if len(models) == 0 {
		return false
	}
	matcher := newModelPriceCollectionMatcher(collection)
	for _, modelID := range models {
		if _, _, ok, _ := matcher.findAutomaticModelPrice(modelID); !ok {
			return false
		}
	}
	return true
}

func modelIdentitySimilarity(left string, right string) (float64, string) {
	if left == right {
		return 1, "exact-model-id"
	}
	if strings.EqualFold(left, right) {
		return 0.98, "case-insensitive-model-id"
	}
	return modelSimilarity(left, right)
}

func sortedPriceKeys(prices map[string]store.ModelPrice) []string {
	keys := make([]string, 0, len(prices))
	for key := range prices {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func modelSimilarity(left string, right string) (float64, string) {
	leftTail := canonicalModelTail(left)
	rightTail := canonicalModelTail(right)
	if leftTail != "" && rightTail != "" {
		if leftTail == rightTail {
			return 0.94, "same-model-with-provider-prefix"
		}
		if strings.Contains(leftTail, rightTail) || strings.Contains(rightTail, leftTail) {
			return 0.78, "model-name-contains"
		}
	}

	leftCanonical := canonicalModelID(left)
	rightCanonical := canonicalModelID(right)
	if leftCanonical != "" && rightCanonical != "" {
		if leftCanonical == rightCanonical {
			return 0.9, "normalized-model-name"
		}
		if strings.Contains(leftCanonical, rightCanonical) || strings.Contains(rightCanonical, leftCanonical) {
			return 0.74, "normalized-name-contains"
		}
	}

	leftTokens := similarityTokens(left)
	rightTokens := similarityTokens(right)
	tokenScore := tokenJaccard(leftTokens, rightTokens)
	editScore := normalizedEditSimilarity(leftTail, rightTail)
	score := math.Max(tokenScore*0.86, editScore*0.82)
	switch {
	case tokenScore >= 0.65:
		return math.Max(score, 0.72), "shared-model-tokens"
	case tokenScore >= 0.4:
		return math.Max(score, 0.58), "shared-model-tokens"
	case sameModelFamily(leftTokens, rightTokens):
		return math.Max(score, 0.46), "same-model-family"
	case editScore >= 0.68:
		return score, "similar-model-name"
	default:
		return score, "weak-similarity"
	}
}

func isWeakRecallReason(reason string) bool {
	return reason == "same-model-family"
}

func canonicalModelID(value string) string {
	return strings.Join(modelTokens(value), "")
}

func canonicalModelTail(value string) string {
	return strings.Join(modelTokens(lastModelSegment(value)), "")
}

func lastModelSegment(value string) string {
	parts := strings.Split(strings.TrimSpace(value), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		part := strings.TrimSpace(parts[i])
		if part == "" || strings.EqualFold(part, "models") {
			continue
		}
		return part
	}
	return strings.TrimSpace(value)
}

func modelTokens(value string) []string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	tokens := make([]string, 0, 8)
	var builder strings.Builder
	flush := func() {
		if builder.Len() == 0 {
			return
		}
		token := builder.String()
		if token != "models" {
			tokens = append(tokens, token)
		}
		builder.Reset()
	}
	for _, r := range normalized {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return tokens
}

func similarityTokens(value string) []string {
	seen := map[string]bool{}
	tokens := make([]string, 0, 12)
	add := func(token string) {
		token = strings.ToLower(strings.TrimSpace(token))
		if token == "" || token == "models" || isLowSignalToken(token) || seen[token] {
			return
		}
		seen[token] = true
		tokens = append(tokens, token)
	}

	for _, token := range modelTokens(value) {
		add(token)
		for _, split := range splitAlphaNumericToken(token) {
			add(split)
		}
		for _, alias := range tokenAliases(token) {
			add(alias)
		}
		for _, split := range splitAlphaNumericToken(token) {
			for _, alias := range tokenAliases(split) {
				add(alias)
			}
		}
	}
	return tokens
}

func splitAlphaNumericToken(token string) []string {
	if token == "" {
		return nil
	}
	parts := []string{}
	var builder strings.Builder
	var previousClass int
	flush := func() {
		if builder.Len() == 0 {
			return
		}
		parts = append(parts, builder.String())
		builder.Reset()
	}
	for _, r := range token {
		class := 0
		switch {
		case unicode.IsLetter(r):
			class = 1
		case unicode.IsDigit(r):
			class = 2
		default:
			flush()
			previousClass = 0
			continue
		}
		if previousClass != 0 && previousClass != class {
			flush()
		}
		builder.WriteRune(r)
		previousClass = class
	}
	flush()
	return parts
}

func tokenAliases(token string) []string {
	switch token {
	case "mimo":
		return []string{"minimax", "m2", "m25"}
	case "minimax":
		return []string{"mimo"}
	case "m2", "m25":
		return []string{"mimo", "minimax"}
	case "low":
		return []string{"lite"}
	case "lite":
		return []string{"low"}
	case "flashlow":
		return []string{"flashlite", "flash", "lite"}
	case "flashlite":
		return []string{"flashlow", "flash", "low"}
	default:
		return nil
	}
}

func isLowSignalToken(token string) bool {
	switch token {
	case "latest", "preview", "free":
		return true
	default:
		return false
	}
}

func sameModelFamily(left []string, right []string) bool {
	for _, family := range []string{"qwen", "gemini", "minimax", "mimo"} {
		if containsToken(left, family) && containsToken(right, family) {
			return true
		}
	}
	return false
}

func containsToken(tokens []string, target string) bool {
	for _, token := range tokens {
		if token == target {
			return true
		}
	}
	return false
}

func tokenJaccard(left []string, right []string) float64 {
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	leftSet := map[string]bool{}
	for _, token := range left {
		leftSet[token] = true
	}
	rightSet := map[string]bool{}
	for _, token := range right {
		rightSet[token] = true
	}
	intersection := 0
	for token := range leftSet {
		if rightSet[token] {
			intersection++
		}
	}
	union := len(leftSet) + len(rightSet) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func normalizedEditSimilarity(left string, right string) float64 {
	if left == "" || right == "" {
		return 0
	}
	distance := levenshteinDistance(left, right)
	maxLen := max(len([]rune(left)), len([]rune(right)))
	if maxLen == 0 {
		return 0
	}
	return 1 - float64(distance)/float64(maxLen)
}

func levenshteinDistance(left string, right string) int {
	leftRunes := []rune(left)
	rightRunes := []rune(right)
	if len(leftRunes) == 0 {
		return len(rightRunes)
	}
	if len(rightRunes) == 0 {
		return len(leftRunes)
	}
	prev := make([]int, len(rightRunes)+1)
	curr := make([]int, len(rightRunes)+1)
	for j := range prev {
		prev[j] = j
	}
	for i, leftRune := range leftRunes {
		curr[0] = i + 1
		for j, rightRune := range rightRunes {
			cost := 1
			if leftRune == rightRune {
				cost = 0
			}
			curr[j+1] = min(
				curr[j]+1,
				min(prev[j+1]+1, prev[j]+cost),
			)
		}
		prev, curr = curr, prev
	}
	return prev[len(rightRunes)]
}

func readFloat(entry map[string]any, key string) (float64, bool) {
	value, ok := entry[key]
	if !ok || value == nil {
		return 0, false
	}
	switch typed := value.(type) {
	case float64:
		return typed, true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func readFirstFloat(entry map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		if value, ok := readFloat(entry, key); ok {
			return value, true
		}
	}
	return 0, false
}

func readPositiveInt64(entry map[string]any, key string) (int64, bool) {
	value, ok := entry[key]
	if !ok || value == nil {
		return 0, false
	}
	var parsed int64
	switch typed := value.(type) {
	case float64:
		if typed <= 0 || typed > math.MaxInt64 || math.Trunc(typed) != typed {
			return 0, false
		}
		parsed = int64(typed)
	case json.Number:
		value, err := typed.Int64()
		if err != nil || value <= 0 {
			return 0, false
		}
		parsed = value
	case string:
		value, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if err != nil || value <= 0 {
			return 0, false
		}
		parsed = value
	default:
		return 0, false
	}
	return parsed, true
}

func readString(entry map[string]any, key string) string {
	value, ok := entry[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return strings.TrimSpace(typed.String())
	default:
		return ""
	}
}

// filterSelectionFeedback drops candidate and unmatched entries for models that
// already have a stored price. Sync feedback should only report models the user
// can still act on; the prices page intentionally hides priced rows from the
// pending-confirmation filter to protect manually configured prices, so reporting
// them here only produces misleading counts.
func filterSelectionFeedback(selection priceSelectionResult, prices map[string]store.ModelPrice) priceSelectionResult {
	if len(selection.Candidates) == 0 && len(selection.Unmatched) == 0 {
		return selection
	}
	candidates := make([]SyncCandidateSet, 0, len(selection.Candidates))
	for _, set := range selection.Candidates {
		if _, priced := prices[set.Model]; !priced {
			candidates = append(candidates, set)
		}
	}
	unmatched := make([]string, 0, len(selection.Unmatched))
	for _, model := range selection.Unmatched {
		if _, priced := prices[model]; !priced {
			unmatched = append(unmatched, model)
		}
	}
	selection.Candidates = candidates
	selection.Unmatched = unmatched
	return selection
}
