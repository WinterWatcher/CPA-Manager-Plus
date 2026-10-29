package model

type ModelPrice struct {
	Provider                string                  `json:"provider,omitempty"`
	Prompt                  float64                 `json:"prompt"`
	Completion              float64                 `json:"completion"`
	Cache                   float64                 `json:"cache"`
	CacheRead               float64                 `json:"cacheRead,omitempty"`
	CacheCreation           float64                 `json:"cacheCreation,omitempty"`
	Capability              string                  `json:"capability,omitempty"`
	BillingUnit             string                  `json:"billingUnit,omitempty"`
	UnitPrice               float64                 `json:"unitPrice,omitempty"`
	PromptConfigured        bool                    `json:"promptConfigured,omitempty"`
	CompletionConfigured    bool                    `json:"completionConfigured,omitempty"`
	CacheReadConfigured     bool                    `json:"cacheReadConfigured,omitempty"`
	CacheCreationConfigured bool                    `json:"cacheCreationConfigured,omitempty"`
	Source                  string                  `json:"source,omitempty"`
	SourceModelID           string                  `json:"sourceModelId,omitempty"`
	RawJSON                 string                  `json:"rawJson,omitempty"`
	ContextTiers            []ModelPriceContextTier `json:"contextTiers,omitempty"`
	ServiceTiers            []ModelPriceServiceTier `json:"serviceTiers,omitempty"`
	UpdatedAtMS             int64                   `json:"updatedAtMs,omitempty"`
	SyncedAtMS              *int64                  `json:"syncedAtMs,omitempty"`
}

// Candidate status constants.
const (
	CandidateStatusPending  = "pending"
	CandidateStatusApproved = "approved"
	CandidateStatusRejected = "rejected"
	CandidateStatusExpired  = "expired"
	CandidateStatusBlocked  = "blocked"
	CandidateStatusUnchanged = "unchanged"

	ChangeKindUnchanged      = "unchanged"
	ChangeKindPriceIncrease  = "price_increase"
	ChangeKindPriceDecrease  = "price_decrease"
	ChangeKindFreeToPaid     = "free_to_paid"
	ChangeKindPaidToFree     = "paid_to_free"
	ChangeKindUnitChanged    = "unit_changed"
	ChangeKindModelRemapped  = "model_remapped"
	ChangeKindUnknownToKnown = "unknown_to_known"
	ChangeKindKnownToUnknown = "known_to_unknown"
)

// ModelPriceCandidate represents a proposed price change waiting for approval or record.
type ModelPriceCandidate struct {
	ID                 int64   `json:"id"`
	Provider           string  `json:"provider"`
	Model              string  `json:"model"`
	Capability         string  `json:"capability,omitempty"`
	BillingUnit        string  `json:"billingUnit,omitempty"`
	PromptPer1M        float64 `json:"prompt"`
	CompletionPer1M    float64 `json:"completion"`
	CachePer1M         float64 `json:"cache"`
	CacheReadPer1M     float64 `json:"cacheRead,omitempty"`
	CacheCreationPer1M float64 `json:"cacheCreation,omitempty"`
	UnitPrice          float64 `json:"unitPrice,omitempty"`
	Source             string  `json:"source"`
	SourceModelID      string  `json:"sourceModelId,omitempty"`
	RawJSON            string  `json:"rawJson,omitempty"`
	ActivePriceHash    string  `json:"activePriceHash,omitempty"`
	CandidatePriceHash string  `json:"candidatePriceHash"`
	ChangeKind         string  `json:"changeKind"`
	Status             string  `json:"status"`
	DetectedAtMS       int64   `json:"detectedAtMs"`
	ReviewedAtMS       *int64  `json:"reviewedAtMs,omitempty"`
	ReviewedBy         string  `json:"reviewedBy,omitempty"`
	ReviewReason       string  `json:"reviewReason,omitempty"`
}

// ModelPriceSnapshot represents an immutable historical record of an active price.
type ModelPriceSnapshot struct {
	ID                 int64   `json:"id"`
	Provider           string  `json:"provider"`
	Model              string  `json:"model"`
	Capability         string  `json:"capability,omitempty"`
	BillingUnit        string  `json:"billingUnit,omitempty"`
	PromptPer1M        float64 `json:"prompt"`
	CompletionPer1M    float64 `json:"completion"`
	CachePer1M         float64 `json:"cache"`
	CacheReadPer1M     float64 `json:"cacheRead,omitempty"`
	CacheCreationPer1M float64 `json:"cacheCreation,omitempty"`
	UnitPrice          float64 `json:"unitPrice,omitempty"`
	PriceHash          string  `json:"priceHash"`
	Source             string  `json:"source"`
	CreatedAtMS        int64   `json:"createdAtMs"`
	CreatedBy          string  `json:"createdBy,omitempty"`
}

// ModelPriceAuditEvent records every approval, rejection, freeze, or change in price policy.
type ModelPriceAuditEvent struct {
	ID          int64  `json:"id"`
	Action      string `json:"action"`
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	CandidateID *int64 `json:"candidateId,omitempty"`
	SnapshotID  *int64 `json:"snapshotId,omitempty"`
	Operator    string `json:"operator"`
	Reason      string `json:"reason,omitempty"`
	DetailsJSON string `json:"detailsJson,omitempty"`
	CreatedAtMS int64  `json:"createdAtMs"`
}

// ModelPriceFreezeStatus tracks whether a provider/model pricing is currently frozen.
type ModelPriceFreezeStatus struct {
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	IsFrozen     bool   `json:"isFrozen"`
	FrozenReason string `json:"frozenReason,omitempty"`
	FrozenAtMS   *int64 `json:"frozenAtMs,omitempty"`
	FrozenBy     string `json:"frozenBy,omitempty"`
}

type ModelPriceSyncResult struct {
	Imported  int      `json:"imported"`
	Skipped   int      `json:"skipped"`
	Preserved []string `json:"preserved,omitempty"`
}

type ModelUsageStat struct {
	Model          string `json:"model"`
	Calls          int64  `json:"calls"`
	RequestedCalls int64  `json:"requested_calls"`
	ResolvedCalls  int64  `json:"resolved_calls"`
}

type ModelUsageSummary struct {
	SampledEvents int64            `json:"sampled_events"`
	TotalEvents   int64            `json:"total_events"`
	Truncated     bool             `json:"truncated"`
	Models        []ModelUsageStat `json:"models"`
}
