package modelprice

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/usagepricing"
)

var ErrStructureChangeAfterRawDeletion = errors.New("model price structure cannot change after archived raw usage has been deleted")

type CandidateFilter struct {
	Status     string
	ChangeKind string
	Provider   string
	Model      string
	Limit      int
	Offset     int
}

type Repository interface {
	LoadAll(ctx context.Context) (map[string]model.ModelPrice, error)
	LoadAllTx(ctx context.Context, tx *sql.Tx) (map[string]model.ModelPrice, error)
	ReplaceAll(ctx context.Context, prices map[string]model.ModelPrice) error
	UpsertSynced(ctx context.Context, prices map[string]model.ModelPrice) (model.ModelPriceSyncResult, error)

	// Candidate operations
	SaveCandidates(ctx context.Context, candidates []model.ModelPriceCandidate) error
	ListCandidates(ctx context.Context, filter CandidateFilter) ([]model.ModelPriceCandidate, int64, error)
	GetCandidateByID(ctx context.Context, id int64) (*model.ModelPriceCandidate, error)
	UpdateCandidateReview(ctx context.Context, id int64, status, reviewer, reason string) error

	// Snapshot operations
	CreateSnapshot(ctx context.Context, snapshot model.ModelPriceSnapshot) (int64, error)
	ListSnapshots(ctx context.Context, provider, model string, limit, offset int) ([]model.ModelPriceSnapshot, int64, error)

	// Audit operations
	RecordAuditEvent(ctx context.Context, event model.ModelPriceAuditEvent) error
	ListAuditEvents(ctx context.Context, provider, model string, limit, offset int) ([]model.ModelPriceAuditEvent, int64, error)

	// Freeze operations
	SetFreezeStatus(ctx context.Context, status model.ModelPriceFreezeStatus) error
	GetFreezeStatus(ctx context.Context, provider, model string) (*model.ModelPriceFreezeStatus, error)
	ListFrozenModels(ctx context.Context) ([]model.ModelPriceFreezeStatus, error)

	// Active price upsert from approved candidate
	ApplyApprovedCandidate(ctx context.Context, candidate model.ModelPriceCandidate, reviewer, reason string) (*model.ModelPriceSnapshot, error)
}

type repository struct {
	db *sql.DB
}

type configuredFlag bool

func (f *configuredFlag) Scan(value any) error {
	switch value := value.(type) {
	case int64:
		*f = value != 0
		return nil
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("invalid configured flag numeric value %v", value)
		}
		*f = value != 0
		return nil
	default:
		return fmt.Errorf("invalid configured flag storage type %T", value)
	}
}

func New(db *sql.DB) Repository {
	return &repository{db: db}
}

func (r *repository) LoadAll(ctx context.Context) (map[string]model.ModelPrice, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	prices, err := r.LoadAllTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return prices, nil
}

func (r *repository) LoadAllTx(ctx context.Context, tx *sql.Tx) (map[string]model.ModelPrice, error) {
	rows, err := tx.QueryContext(ctx, `select
		model, prompt_per_1m, completion_per_1m, cache_per_1m, cache_read_per_1m, cache_creation_per_1m,
		prompt_configured, completion_configured, cache_read_configured, cache_creation_configured, source, source_model_id, raw_json,
		updated_at_ms, synced_at_ms, capability, billing_unit, unit_price
		from model_prices order by model`)
	if err != nil {
		return nil, err
	}

	prices := map[string]model.ModelPrice{}
	for rows.Next() {
		var modelID string
		var price model.ModelPrice
		var source, sourceModelID, rawJSON, capability, billingUnit sql.NullString
		var syncedAt sql.NullInt64
		var unitPrice sql.NullFloat64
		var promptConfigured, completionConfigured, cacheReadConfigured, cacheCreationConfigured configuredFlag
		if err := rows.Scan(
			&modelID,
			&price.Prompt,
			&price.Completion,
			&price.Cache,
			&price.CacheRead,
			&price.CacheCreation,
			&promptConfigured,
			&completionConfigured,
			&cacheReadConfigured,
			&cacheCreationConfigured,
			&source,
			&sourceModelID,
			&rawJSON,
			&price.UpdatedAtMS,
			&syncedAt,
			&capability,
			&billingUnit,
			&unitPrice,
		); err != nil {
			return nil, err
		}
		price.Source = source.String
		price.PromptConfigured = bool(promptConfigured)
		price.CompletionConfigured = bool(completionConfigured)
		price.CacheReadConfigured = bool(cacheReadConfigured)
		price.CacheCreationConfigured = bool(cacheCreationConfigured)
		price.SourceModelID = sourceModelID.String
		price.RawJSON = rawJSON.String
		price.Capability = capability.String
		price.BillingUnit = billingUnit.String
		price.UnitPrice = unitPrice.Float64
		if syncedAt.Valid {
			value := syncedAt.Int64
			price.SyncedAtMS = &value
		}
		prices[modelID] = price
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	tierRows, err := tx.QueryContext(ctx, `select
		model, threshold_tokens, prompt_per_1m, completion_per_1m, cache_per_1m, cache_read_per_1m, cache_creation_per_1m,
		prompt_configured, completion_configured, cache_configured, cache_read_configured, cache_creation_configured
		from model_price_context_tiers order by model, threshold_tokens`)
	if err != nil {
		return nil, err
	}
	for tierRows.Next() {
		var modelID string
		var tier model.ModelPriceContextTier
		var promptConfigured, completionConfigured, cacheConfigured, cacheReadConfigured, cacheCreationConfigured configuredFlag
		if err := tierRows.Scan(
			&modelID,
			&tier.ThresholdTokens,
			&tier.Prompt,
			&tier.Completion,
			&tier.Cache,
			&tier.CacheRead,
			&tier.CacheCreation,
			&promptConfigured,
			&completionConfigured,
			&cacheConfigured,
			&cacheReadConfigured,
			&cacheCreationConfigured,
		); err != nil {
			return nil, err
		}
		tier.PromptConfigured = bool(promptConfigured)
		tier.CompletionConfigured = bool(completionConfigured)
		tier.CacheConfigured = bool(cacheConfigured)
		tier.CacheReadConfigured = bool(cacheReadConfigured)
		tier.CacheCreationConfigured = bool(cacheCreationConfigured)
		price, ok := prices[modelID]
		if !ok {
			continue
		}
		price.ContextTiers = append(price.ContextTiers, tier)
		prices[modelID] = price
	}
	if err := tierRows.Err(); err != nil {
		_ = tierRows.Close()
		return nil, err
	}
	if err := tierRows.Close(); err != nil {
		return nil, err
	}

	serviceTierRows, err := tx.QueryContext(ctx, `select
		model, mode, service_tier, prompt_per_1m, completion_per_1m, cache_per_1m, cache_read_per_1m, cache_creation_per_1m,
		prompt_configured, completion_configured, cache_configured, cache_read_configured, cache_creation_configured
		from model_price_service_tiers order by model, mode, service_tier`)
	if err != nil {
		return nil, err
	}
	for serviceTierRows.Next() {
		var modelID string
		var tier model.ModelPriceServiceTier
		var promptConfigured, completionConfigured, cacheConfigured, cacheReadConfigured, cacheCreationConfigured configuredFlag
		if err := serviceTierRows.Scan(
			&modelID,
			&tier.Mode,
			&tier.ServiceTier,
			&tier.Prompt,
			&tier.Completion,
			&tier.Cache,
			&tier.CacheRead,
			&tier.CacheCreation,
			&promptConfigured,
			&completionConfigured,
			&cacheConfigured,
			&cacheReadConfigured,
			&cacheCreationConfigured,
		); err != nil {
			return nil, err
		}
		tier.PromptConfigured = bool(promptConfigured)
		tier.CompletionConfigured = bool(completionConfigured)
		tier.CacheConfigured = bool(cacheConfigured)
		tier.CacheReadConfigured = bool(cacheReadConfigured)
		tier.CacheCreationConfigured = bool(cacheCreationConfigured)
		price, ok := prices[modelID]
		if !ok {
			continue
		}
		price.ServiceTiers = append(price.ServiceTiers, tier)
		prices[modelID] = price
	}
	if err := serviceTierRows.Err(); err != nil {
		_ = serviceTierRows.Close()
		return nil, err
	}
	if err := serviceTierRows.Close(); err != nil {
		return nil, err
	}
	return prices, nil
}

func (r *repository) ReplaceAll(ctx context.Context, prices map[string]model.ModelPrice) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	beforePrices, err := r.LoadAllTx(ctx, tx)
	if err != nil {
		return err
	}

	normalizedPrices := make(map[string]model.ModelPrice, len(prices))
	for modelID, price := range prices {
		if err := model.ValidateModelPrice(modelID, price); err != nil {
			return err
		}
		price.ContextTiers, err = model.NormalizeModelPriceContextTiers(price.ContextTiers)
		if err != nil {
			return err
		}
		price.ServiceTiers, err = model.NormalizeModelPriceServiceTiers(price.ServiceTiers)
		if err != nil {
			return err
		}
		normalizedPrices[modelID] = price
	}

	beforeRevision := model.ModelPriceStructureRevision(beforePrices)
	afterRevision := model.ModelPriceStructureRevision(normalizedPrices)
	if beforeRevision != afterRevision {
		if err := usagepricing.VerifyRetainedPricingRebuildSourceTx(ctx, tx); err != nil {
			if errors.Is(err, usagepricing.ErrRetainedPricingHistoryIncomplete) {
				return fmt.Errorf("%w: %v", ErrStructureChangeAfterRawDeletion, err)
			}
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `delete from model_price_service_tiers`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from model_price_context_tiers`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from model_prices`); err != nil {
		return err
	}
	if len(normalizedPrices) == 0 {
		return tx.Commit()
	}

	stmt, err := tx.PrepareContext(ctx, `insert into model_prices (
		model, prompt_per_1m, completion_per_1m, cache_per_1m, cache_read_per_1m, cache_creation_per_1m,
		prompt_configured, completion_configured, cache_read_configured, cache_creation_configured, source, source_model_id,
		raw_json, updated_at_ms, synced_at_ms, capability, billing_unit, unit_price
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	tierStmt, err := prepareContextTierInsert(ctx, tx)
	if err != nil {
		return err
	}
	defer tierStmt.Close()
	serviceTierStmt, err := prepareServiceTierInsert(ctx, tx)
	if err != nil {
		return err
	}
	defer serviceTierStmt.Close()

	now := time.Now().UnixMilli()
	for modelID, price := range normalizedPrices {
		if _, err := stmt.ExecContext(
			ctx,
			modelID,
			price.Prompt,
			price.Completion,
			price.Cache,
			price.CacheRead,
			price.CacheCreation,
			price.PromptConfigured,
			price.CompletionConfigured,
			price.CacheReadConfigured,
			price.CacheCreationConfigured,
			nullString(price.Source),
			nullString(price.SourceModelID),
			nullString(price.RawJSON),
			now,
			nullInt(price.SyncedAtMS),
			nullString(price.Capability),
			nullString(price.BillingUnit),
			price.UnitPrice,
		); err != nil {
			return err
		}
		if err := insertContextTiers(ctx, tierStmt, modelID, price.ContextTiers); err != nil {
			return err
		}
		if err := insertServiceTiers(ctx, serviceTierStmt, modelID, price.ServiceTiers); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *repository) UpsertSynced(ctx context.Context, prices map[string]model.ModelPrice) (model.ModelPriceSyncResult, error) {
	if len(prices) == 0 {
		return model.ModelPriceSyncResult{}, nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return model.ModelPriceSyncResult{}, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	beforePrices, err := r.LoadAllTx(ctx, tx)
	if err != nil {
		return model.ModelPriceSyncResult{}, err
	}

	stmt, err := tx.PrepareContext(ctx, `insert into model_prices (
		model, prompt_per_1m, completion_per_1m, cache_per_1m, cache_read_per_1m, cache_creation_per_1m,
		prompt_configured, completion_configured, cache_read_configured, cache_creation_configured, source, source_model_id,
		raw_json, updated_at_ms, synced_at_ms, capability, billing_unit, unit_price
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	on conflict(model) do update set
		prompt_per_1m = excluded.prompt_per_1m,
		completion_per_1m = excluded.completion_per_1m,
		cache_per_1m = excluded.cache_per_1m,
		cache_read_per_1m = excluded.cache_read_per_1m,
		cache_creation_per_1m = excluded.cache_creation_per_1m,
		prompt_configured = excluded.prompt_configured,
		completion_configured = excluded.completion_configured,
		cache_read_configured = excluded.cache_read_configured,
		cache_creation_configured = excluded.cache_creation_configured,
		source = excluded.source,
		source_model_id = excluded.source_model_id,
		raw_json = excluded.raw_json,
		updated_at_ms = excluded.updated_at_ms,
		synced_at_ms = excluded.synced_at_ms,
		capability = excluded.capability,
		billing_unit = excluded.billing_unit,
		unit_price = excluded.unit_price
	where lower(trim(coalesce(model_prices.source, ''))) <> 'manual'`)
	if err != nil {
		return model.ModelPriceSyncResult{}, err
	}
	defer stmt.Close()
	deleteTierStmt, err := tx.PrepareContext(ctx, `delete from model_price_context_tiers where model = ?`)
	if err != nil {
		return model.ModelPriceSyncResult{}, err
	}
	defer deleteTierStmt.Close()
	tierStmt, err := prepareContextTierInsert(ctx, tx)
	if err != nil {
		return model.ModelPriceSyncResult{}, err
	}
	defer tierStmt.Close()
	deleteServiceTierStmt, err := tx.PrepareContext(ctx, `delete from model_price_service_tiers where model = ?`)
	if err != nil {
		return model.ModelPriceSyncResult{}, err
	}
	defer deleteServiceTierStmt.Close()
	serviceTierStmt, err := prepareServiceTierInsert(ctx, tx)
	if err != nil {
		return model.ModelPriceSyncResult{}, err
	}
	defer serviceTierStmt.Close()

	now := time.Now().UnixMilli()
	result := model.ModelPriceSyncResult{}
	for modelID, price := range prices {
		if err := model.ValidateModelPrice(modelID, price); err != nil {
			result.Skipped++
			continue
		}
		price.ContextTiers, err = model.NormalizeModelPriceContextTiers(price.ContextTiers)
		if err != nil {
			result.Skipped++
			continue
		}
		price.ServiceTiers, err = model.NormalizeModelPriceServiceTiers(price.ServiceTiers)
		if err != nil {
			result.Skipped++
			continue
		}
		if price.Source == "" {
			price.Source = "sync"
		}
		if price.SourceModelID == "" {
			price.SourceModelID = modelID
		}
		price.UpdatedAtMS = now
		price.SyncedAtMS = &now
		execResult, err := stmt.ExecContext(
			ctx,
			modelID,
			price.Prompt,
			price.Completion,
			price.Cache,
			price.CacheRead,
			price.CacheCreation,
			price.PromptConfigured,
			price.CompletionConfigured,
			price.CacheReadConfigured,
			price.CacheCreationConfigured,
			nullString(price.Source),
			nullString(price.SourceModelID),
			nullString(price.RawJSON),
			now,
			now,
			nullString(price.Capability),
			nullString(price.BillingUnit),
			price.UnitPrice,
		)
		if err != nil {
			return model.ModelPriceSyncResult{}, err
		}
		rowsAffected, err := execResult.RowsAffected()
		if err != nil {
			return model.ModelPriceSyncResult{}, err
		}
		if rowsAffected == 0 {
			result.Preserved = append(result.Preserved, modelID)
			continue
		}
		if _, err := deleteTierStmt.ExecContext(ctx, modelID); err != nil {
			return model.ModelPriceSyncResult{}, err
		}
		if err := insertContextTiers(ctx, tierStmt, modelID, price.ContextTiers); err != nil {
			return model.ModelPriceSyncResult{}, err
		}
		if _, err := deleteServiceTierStmt.ExecContext(ctx, modelID); err != nil {
			return model.ModelPriceSyncResult{}, err
		}
		if err := insertServiceTiers(ctx, serviceTierStmt, modelID, price.ServiceTiers); err != nil {
			return model.ModelPriceSyncResult{}, err
		}
		result.Imported++
	}
	afterPrices, err := r.LoadAllTx(ctx, tx)
	if err != nil {
		return model.ModelPriceSyncResult{}, err
	}
	beforeRevision := model.ModelPriceStructureRevision(beforePrices)
	afterRevision := model.ModelPriceStructureRevision(afterPrices)
	if beforeRevision != afterRevision {
		if err := usagepricing.VerifyRetainedPricingRebuildSourceTx(ctx, tx); err != nil {
			if errors.Is(err, usagepricing.ErrRetainedPricingHistoryIncomplete) {
				return model.ModelPriceSyncResult{}, fmt.Errorf("%w: %v", ErrStructureChangeAfterRawDeletion, err)
			}
			return model.ModelPriceSyncResult{}, err
		}
	}
	sort.Strings(result.Preserved)
	if err := tx.Commit(); err != nil {
		return model.ModelPriceSyncResult{}, err
	}
	return result, nil
}

func prepareContextTierInsert(ctx context.Context, tx *sql.Tx) (*sql.Stmt, error) {
	return tx.PrepareContext(ctx, `insert into model_price_context_tiers (
		model, threshold_tokens, prompt_per_1m, completion_per_1m, cache_per_1m, cache_read_per_1m, cache_creation_per_1m,
		prompt_configured, completion_configured, cache_configured, cache_read_configured, cache_creation_configured
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
}

func insertContextTiers(ctx context.Context, stmt *sql.Stmt, modelID string, tiers []model.ModelPriceContextTier) error {
	for _, tier := range tiers {
		if _, err := stmt.ExecContext(
			ctx,
			modelID,
			tier.ThresholdTokens,
			tier.Prompt,
			tier.Completion,
			tier.Cache,
			tier.CacheRead,
			tier.CacheCreation,
			tier.PromptConfigured,
			tier.CompletionConfigured,
			tier.CacheConfigured,
			tier.CacheReadConfigured,
			tier.CacheCreationConfigured,
		); err != nil {
			return err
		}
	}
	return nil
}

func prepareServiceTierInsert(ctx context.Context, tx *sql.Tx) (*sql.Stmt, error) {
	return tx.PrepareContext(ctx, `insert into model_price_service_tiers (
		model, mode, service_tier, prompt_per_1m, completion_per_1m, cache_per_1m, cache_read_per_1m, cache_creation_per_1m,
		prompt_configured, completion_configured, cache_configured, cache_read_configured, cache_creation_configured
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
}

func insertServiceTiers(ctx context.Context, stmt *sql.Stmt, modelID string, tiers []model.ModelPriceServiceTier) error {
	for _, tier := range tiers {
		if _, err := stmt.ExecContext(
			ctx,
			modelID,
			tier.Mode,
			tier.ServiceTier,
			tier.Prompt,
			tier.Completion,
			tier.Cache,
			tier.CacheRead,
			tier.CacheCreation,
			tier.PromptConfigured,
			tier.CompletionConfigured,
			tier.CacheConfigured,
			tier.CacheReadConfigured,
			tier.CacheCreationConfigured,
		); err != nil {
			return err
		}
	}
	return nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullInt(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func (r *repository) SaveCandidates(ctx context.Context, candidates []model.ModelPriceCandidate) error {
	if len(candidates) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := usagepricing.VerifyRetainedPricingRebuildSourceTx(ctx, tx); err != nil {
		if errors.Is(err, usagepricing.ErrRetainedPricingHistoryIncomplete) {
			return fmt.Errorf("%w: %v", ErrStructureChangeAfterRawDeletion, err)
		}
		return err
	}

	stmt, err := tx.PrepareContext(ctx, `insert into model_price_candidates (
		provider, model, capability, billing_unit, prompt_per_1m, completion_per_1m,
		cache_per_1m, cache_read_per_1m, cache_creation_per_1m, unit_price,
		source, source_model_id, raw_json, active_price_hash, candidate_price_hash,
		change_kind, status, detected_at_ms, reviewed_at_ms, reviewed_by, review_reason
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	checkExistingStmt, err := tx.PrepareContext(ctx, `select id, status from model_price_candidates
		where provider = ? and model = ? and candidate_price_hash = ? and status in ('pending', 'blocked') limit 1`)
	if err != nil {
		return err
	}
	defer checkExistingStmt.Close()

	updateDetectedStmt, err := tx.PrepareContext(ctx, `update model_price_candidates set detected_at_ms = ? where id = ?`)
	if err != nil {
		return err
	}
	defer updateDetectedStmt.Close()

	for _, c := range candidates {
		var existingID int64
		var existingStatus string
		errCheck := checkExistingStmt.QueryRowContext(ctx, c.Provider, c.Model, c.CandidatePriceHash).Scan(&existingID, &existingStatus)
		if errCheck == nil && existingID > 0 {
			if _, errUpdate := updateDetectedStmt.ExecContext(ctx, c.DetectedAtMS, existingID); errUpdate != nil {
				return errUpdate
			}
			continue
		}

		if _, errExec := stmt.ExecContext(ctx,
			c.Provider, c.Model, c.Capability, c.BillingUnit, c.PromptPer1M, c.CompletionPer1M,
			c.CachePer1M, c.CacheReadPer1M, c.CacheCreationPer1M, c.UnitPrice,
			c.Source, c.SourceModelID, c.RawJSON, c.ActivePriceHash, c.CandidatePriceHash,
			c.ChangeKind, c.Status, c.DetectedAtMS, c.ReviewedAtMS, c.ReviewedBy, c.ReviewReason,
		); errExec != nil {
			return errExec
		}
	}

	return tx.Commit()
}

func (r *repository) ListCandidates(ctx context.Context, filter CandidateFilter) ([]model.ModelPriceCandidate, int64, error) {
	where := []string{"1=1"}
	var args []any

	if filter.Status != "" {
		where = append(where, "status = ?")
		args = append(args, filter.Status)
	}
	if filter.ChangeKind != "" {
		where = append(where, "change_kind = ?")
		args = append(args, filter.ChangeKind)
	}
	if filter.Provider != "" {
		where = append(where, "provider = ?")
		args = append(args, filter.Provider)
	}
	if filter.Model != "" {
		where = append(where, "(model like ? or source_model_id like ?)")
		args = append(args, "%"+filter.Model+"%", "%"+filter.Model+"%")
	}

	whereClause := strings.Join(where, " and ")

	var total int64
	countQuery := "select count(*) from model_price_candidates where " + whereClause
	if err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	query := fmt.Sprintf(`select id, provider, model, capability, billing_unit,
		prompt_per_1m, completion_per_1m, cache_per_1m, cache_read_per_1m, cache_creation_per_1m, unit_price,
		source, source_model_id, raw_json, active_price_hash, candidate_price_hash,
		change_kind, status, detected_at_ms, reviewed_at_ms, reviewed_by, review_reason
		from model_price_candidates where %s order by detected_at_ms desc, id desc limit ? offset ?`, whereClause)
	queryArgs := append(args, limit, offset)

	rows, err := r.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var candidates []model.ModelPriceCandidate
	for rows.Next() {
		var c model.ModelPriceCandidate
		var capNull, billNull, smIDNull, rawNull, actHashNull, revByNull, revReasonNull sql.NullString
		var revAtNull sql.NullInt64
		if err := rows.Scan(
			&c.ID, &c.Provider, &c.Model, &capNull, &billNull,
			&c.PromptPer1M, &c.CompletionPer1M, &c.CachePer1M, &c.CacheReadPer1M, &c.CacheCreationPer1M, &c.UnitPrice,
			&c.Source, &smIDNull, &rawNull, &actHashNull, &c.CandidatePriceHash,
			&c.ChangeKind, &c.Status, &c.DetectedAtMS, &revAtNull, &revByNull, &revReasonNull,
		); err != nil {
			return nil, 0, err
		}
		c.Capability = capNull.String
		c.BillingUnit = billNull.String
		c.SourceModelID = smIDNull.String
		c.RawJSON = rawNull.String
		c.ActivePriceHash = actHashNull.String
		c.ReviewedBy = revByNull.String
		c.ReviewReason = revReasonNull.String
		if revAtNull.Valid {
			v := revAtNull.Int64
			c.ReviewedAtMS = &v
		}
		candidates = append(candidates, c)
	}
	return candidates, total, rows.Err()
}

func (r *repository) GetCandidateByID(ctx context.Context, id int64) (*model.ModelPriceCandidate, error) {
	row := r.db.QueryRowContext(ctx, `select id, provider, model, capability, billing_unit,
		prompt_per_1m, completion_per_1m, cache_per_1m, cache_read_per_1m, cache_creation_per_1m, unit_price,
		source, source_model_id, raw_json, active_price_hash, candidate_price_hash,
		change_kind, status, detected_at_ms, reviewed_at_ms, reviewed_by, review_reason
		from model_price_candidates where id = ?`, id)

	var c model.ModelPriceCandidate
	var capNull, billNull, smIDNull, rawNull, actHashNull, revByNull, revReasonNull sql.NullString
	var revAtNull sql.NullInt64
	if err := row.Scan(
		&c.ID, &c.Provider, &c.Model, &capNull, &billNull,
		&c.PromptPer1M, &c.CompletionPer1M, &c.CachePer1M, &c.CacheReadPer1M, &c.CacheCreationPer1M, &c.UnitPrice,
		&c.Source, &smIDNull, &rawNull, &actHashNull, &c.CandidatePriceHash,
		&c.ChangeKind, &c.Status, &c.DetectedAtMS, &revAtNull, &revByNull, &revReasonNull,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	c.Capability = capNull.String
	c.BillingUnit = billNull.String
	c.SourceModelID = smIDNull.String
	c.RawJSON = rawNull.String
	c.ActivePriceHash = actHashNull.String
	c.ReviewedBy = revByNull.String
	c.ReviewReason = revReasonNull.String
	if revAtNull.Valid {
		v := revAtNull.Int64
		c.ReviewedAtMS = &v
	}
	return &c, nil
}

func (r *repository) UpdateCandidateReview(ctx context.Context, id int64, status, reviewer, reason string) error {
	nowMS := time.Now().UnixMilli()
	res, err := r.db.ExecContext(ctx, `update model_price_candidates set
		status = ?, reviewed_at_ms = ?, reviewed_by = ?, review_reason = ?
		where id = ?`, status, nowMS, reviewer, reason, id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return errors.New("candidate not found")
	}
	return nil
}

func (r *repository) CreateSnapshot(ctx context.Context, s model.ModelPriceSnapshot) (int64, error) {
	res, err := r.db.ExecContext(ctx, `insert into model_price_snapshots (
		provider, model, capability, billing_unit, prompt_per_1m, completion_per_1m,
		cache_per_1m, cache_read_per_1m, cache_creation_per_1m, unit_price,
		price_hash, source, created_at_ms, created_by
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.Provider, s.Model, s.Capability, s.BillingUnit, s.PromptPer1M, s.CompletionPer1M,
		s.CachePer1M, s.CacheReadPer1M, s.CacheCreationPer1M, s.UnitPrice,
		s.PriceHash, s.Source, s.CreatedAtMS, s.CreatedBy)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *repository) ListSnapshots(ctx context.Context, provider, modelName string, limit, offset int) ([]model.ModelPriceSnapshot, int64, error) {
	where := []string{"1=1"}
	var args []any
	if provider != "" {
		where = append(where, "provider = ?")
		args = append(args, provider)
	}
	if modelName != "" {
		where = append(where, "model = ?")
		args = append(args, modelName)
	}
	whereClause := strings.Join(where, " and ")

	var total int64
	if err := r.db.QueryRowContext(ctx, "select count(*) from model_price_snapshots where "+whereClause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	query := fmt.Sprintf(`select id, provider, model, capability, billing_unit,
		prompt_per_1m, completion_per_1m, cache_per_1m, cache_read_per_1m, cache_creation_per_1m, unit_price,
		price_hash, source, created_at_ms, created_by
		from model_price_snapshots where %s order by created_at_ms desc, id desc limit ? offset ?`, whereClause)
	queryArgs := append(args, limit, offset)

	rows, err := r.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var snapshots []model.ModelPriceSnapshot
	for rows.Next() {
		var s model.ModelPriceSnapshot
		var capNull, billNull, createdByNull sql.NullString
		if err := rows.Scan(
			&s.ID, &s.Provider, &s.Model, &capNull, &billNull,
			&s.PromptPer1M, &s.CompletionPer1M, &s.CachePer1M, &s.CacheReadPer1M, &s.CacheCreationPer1M, &s.UnitPrice,
			&s.PriceHash, &s.Source, &s.CreatedAtMS, &createdByNull,
		); err != nil {
			return nil, 0, err
		}
		s.Capability = capNull.String
		s.BillingUnit = billNull.String
		s.CreatedBy = createdByNull.String
		snapshots = append(snapshots, s)
	}
	return snapshots, total, rows.Err()
}

func (r *repository) RecordAuditEvent(ctx context.Context, e model.ModelPriceAuditEvent) error {
	_, err := r.db.ExecContext(ctx, `insert into model_price_audit_events (
		action, provider, model, candidate_id, snapshot_id, operator, reason, details_json, created_at_ms
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Action, e.Provider, e.Model, e.CandidateID, e.SnapshotID, e.Operator, e.Reason, e.DetailsJSON, e.CreatedAtMS)
	return err
}

func (r *repository) ListAuditEvents(ctx context.Context, provider, modelName string, limit, offset int) ([]model.ModelPriceAuditEvent, int64, error) {
	where := []string{"1=1"}
	var args []any
	if provider != "" {
		where = append(where, "provider = ?")
		args = append(args, provider)
	}
	if modelName != "" {
		where = append(where, "model = ?")
		args = append(args, modelName)
	}
	whereClause := strings.Join(where, " and ")

	var total int64
	if err := r.db.QueryRowContext(ctx, "select count(*) from model_price_audit_events where "+whereClause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	query := fmt.Sprintf(`select id, action, provider, model, candidate_id, snapshot_id,
		operator, reason, details_json, created_at_ms
		from model_price_audit_events where %s order by created_at_ms desc, id desc limit ? offset ?`, whereClause)
	queryArgs := append(args, limit, offset)

	rows, err := r.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var events []model.ModelPriceAuditEvent
	for rows.Next() {
		var e model.ModelPriceAuditEvent
		var cidNull, sidNull sql.NullInt64
		var reasonNull, detailsNull sql.NullString
		if err := rows.Scan(
			&e.ID, &e.Action, &e.Provider, &e.Model, &cidNull, &sidNull,
			&e.Operator, &reasonNull, &detailsNull, &e.CreatedAtMS,
		); err != nil {
			return nil, 0, err
		}
		if cidNull.Valid {
			v := cidNull.Int64
			e.CandidateID = &v
		}
		if sidNull.Valid {
			v := sidNull.Int64
			e.SnapshotID = &v
		}
		e.Reason = reasonNull.String
		e.DetailsJSON = detailsNull.String
		events = append(events, e)
	}
	return events, total, rows.Err()
}

func (r *repository) SetFreezeStatus(ctx context.Context, status model.ModelPriceFreezeStatus) error {
	isFrozenInt := 0
	if status.IsFrozen {
		isFrozenInt = 1
	}
	_, err := r.db.ExecContext(ctx, `insert into model_price_freeze_status (
		provider, model, is_frozen, frozen_reason, frozen_at_ms, frozen_by
	) values (?, ?, ?, ?, ?, ?)
	on conflict(provider, model) do update set
		is_frozen = excluded.is_frozen,
		frozen_reason = excluded.frozen_reason,
		frozen_at_ms = excluded.frozen_at_ms,
		frozen_by = excluded.frozen_by`,
		status.Provider, status.Model, isFrozenInt, status.FrozenReason, status.FrozenAtMS, status.FrozenBy)
	return err
}

func (r *repository) GetFreezeStatus(ctx context.Context, provider, modelName string) (*model.ModelPriceFreezeStatus, error) {
	row := r.db.QueryRowContext(ctx, `select provider, model, is_frozen, frozen_reason, frozen_at_ms, frozen_by
		from model_price_freeze_status where provider = ? and model = ?`, provider, modelName)
	var s model.ModelPriceFreezeStatus
	var isFrozenInt int
	var reasonNull, byNull sql.NullString
	var atNull sql.NullInt64
	if err := row.Scan(&s.Provider, &s.Model, &isFrozenInt, &reasonNull, &atNull, &byNull); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &model.ModelPriceFreezeStatus{Provider: provider, Model: modelName, IsFrozen: false}, nil
		}
		return nil, err
	}
	s.IsFrozen = isFrozenInt != 0
	s.FrozenReason = reasonNull.String
	s.FrozenBy = byNull.String
	if atNull.Valid {
		v := atNull.Int64
		s.FrozenAtMS = &v
	}
	return &s, nil
}

func (r *repository) ListFrozenModels(ctx context.Context) ([]model.ModelPriceFreezeStatus, error) {
	rows, err := r.db.QueryContext(ctx, `select provider, model, is_frozen, frozen_reason, frozen_at_ms, frozen_by
		from model_price_freeze_status where is_frozen = 1 order by provider, model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []model.ModelPriceFreezeStatus
	for rows.Next() {
		var s model.ModelPriceFreezeStatus
		var isFrozenInt int
		var reasonNull, byNull sql.NullString
		var atNull sql.NullInt64
		if err := rows.Scan(&s.Provider, &s.Model, &isFrozenInt, &reasonNull, &atNull, &byNull); err != nil {
			return nil, err
		}
		s.IsFrozen = isFrozenInt != 0
		s.FrozenReason = reasonNull.String
		s.FrozenBy = byNull.String
		if atNull.Valid {
			v := atNull.Int64
			s.FrozenAtMS = &v
		}
		list = append(list, s)
	}
	return list, rows.Err()
}

func (r *repository) ApplyApprovedCandidate(ctx context.Context, c model.ModelPriceCandidate, reviewer, reason string) (*model.ModelPriceSnapshot, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	nowMS := time.Now().UnixMilli()

	// 1. Update candidate record
	if _, err := tx.ExecContext(ctx, `update model_price_candidates set
		status = ?, reviewed_at_ms = ?, reviewed_by = ?, review_reason = ?
		where id = ?`, model.CandidateStatusApproved, nowMS, reviewer, reason, c.ID); err != nil {
		return nil, err
	}

	// 2. Upsert into model_prices
	promptConfigured := 1
	completionConfigured := 1
	cacheReadConfigured := 0
	if c.CacheReadPer1M != 0 {
		cacheReadConfigured = 1
	}
	cacheCreationConfigured := 0
	if c.CacheCreationPer1M != 0 {
		cacheCreationConfigured = 1
	}

	if _, err := tx.ExecContext(ctx, `insert into model_prices (
		model, prompt_per_1m, completion_per_1m, cache_per_1m, cache_read_per_1m, cache_creation_per_1m,
		prompt_configured, completion_configured, cache_read_configured, cache_creation_configured,
		source, source_model_id, raw_json, updated_at_ms, synced_at_ms, capability, billing_unit, unit_price, provider
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	on conflict(model) do update set
		prompt_per_1m = excluded.prompt_per_1m,
		completion_per_1m = excluded.completion_per_1m,
		cache_per_1m = excluded.cache_per_1m,
		cache_read_per_1m = excluded.cache_read_per_1m,
		cache_creation_per_1m = excluded.cache_creation_per_1m,
		prompt_configured = excluded.prompt_configured,
		completion_configured = excluded.completion_configured,
		cache_read_configured = excluded.cache_read_configured,
		cache_creation_configured = excluded.cache_creation_configured,
		source = excluded.source,
		source_model_id = excluded.source_model_id,
		raw_json = excluded.raw_json,
		updated_at_ms = excluded.updated_at_ms,
		synced_at_ms = excluded.synced_at_ms,
		capability = excluded.capability,
		billing_unit = excluded.billing_unit,
		unit_price = excluded.unit_price,
		provider = excluded.provider`,
		c.Model, c.PromptPer1M, c.CompletionPer1M, c.CachePer1M, c.CacheReadPer1M, c.CacheCreationPer1M,
		promptConfigured, completionConfigured, cacheReadConfigured, cacheCreationConfigured,
		c.Source, c.SourceModelID, c.RawJSON, nowMS, nowMS, c.Capability, c.BillingUnit, c.UnitPrice, c.Provider,
	); err != nil {
		return nil, err
	}

	// 3. Create immutable price snapshot
	res, err := tx.ExecContext(ctx, `insert into model_price_snapshots (
		provider, model, capability, billing_unit, prompt_per_1m, completion_per_1m,
		cache_per_1m, cache_read_per_1m, cache_creation_per_1m, unit_price,
		price_hash, source, created_at_ms, created_by
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Provider, c.Model, c.Capability, c.BillingUnit, c.PromptPer1M, c.CompletionPer1M,
		c.CachePer1M, c.CacheReadPer1M, c.CacheCreationPer1M, c.UnitPrice,
		c.CandidatePriceHash, c.Source, nowMS, reviewer)
	if err != nil {
		return nil, err
	}
	snapshotID, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}

	// 4. Unfreeze model if it was frozen
	if _, err := tx.ExecContext(ctx, `update model_price_freeze_status set is_frozen = 0 where provider = ? and model = ?`,
		c.Provider, c.Model); err != nil {
		return nil, err
	}

	// 5. Record audit event
	candidateID := c.ID
	if _, err := tx.ExecContext(ctx, `insert into model_price_audit_events (
		action, provider, model, candidate_id, snapshot_id, operator, reason, created_at_ms
	) values (?, ?, ?, ?, ?, ?, ?, ?)`,
		"approve", c.Provider, c.Model, candidateID, snapshotID, reviewer, reason, nowMS); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &model.ModelPriceSnapshot{
		ID:                 snapshotID,
		Provider:           c.Provider,
		Model:              c.Model,
		Capability:         c.Capability,
		BillingUnit:        c.BillingUnit,
		PromptPer1M:        c.PromptPer1M,
		CompletionPer1M:    c.CompletionPer1M,
		CachePer1M:         c.CachePer1M,
		CacheReadPer1M:     c.CacheReadPer1M,
		CacheCreationPer1M: c.CacheCreationPer1M,
		UnitPrice:          c.UnitPrice,
		PriceHash:          c.CandidatePriceHash,
		Source:             c.Source,
		CreatedAtMS:        nowMS,
		CreatedBy:          reviewer,
	}, nil
}
