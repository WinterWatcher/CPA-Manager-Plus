package sqlite

import (
	"database/sql"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	modelpricerepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/modelprice"
	usageeventrepo "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/usageevent"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/pricing"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

func TestIsolatedDatabaseMigrationAndCapabilityRollup(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "isolated_migration_test.sqlite")

	// 1. 从旧schema创建数据库（不包含 capability / billing_unit / quantity 列）
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open raw sqlite: %v", err)
	}

	legacyUsageEventsSchema := `
	create table usage_events (
		id integer primary key autoincrement,
		request_id text,
		event_hash text not null unique,
		timestamp_ms integer not null,
		timestamp text not null,
		provider text,
		executor_type text,
		model text not null,
		endpoint text,
		method text,
		path text,
		client_ip text,
		x_forwarded_for text,
		user_agent text,
		auth_type text,
		auth_index text,
		source text,
		source_hash text,
		api_key_hash text,
		account_snapshot text,
		auth_label_snapshot text,
		auth_file_snapshot text,
		auth_provider_snapshot text,
		auth_account_id_snapshot text,
		auth_project_id_snapshot text,
		auth_snapshot_at_ms integer,
		requested_model text,
		resolved_model text,
		reasoning_effort text,
		service_tier text,
		request_service_tier text,
		response_service_tier text,
		cache_input_mode text,
		input_tokens integer not null default 0,
		output_tokens integer not null default 0,
		reasoning_tokens integer not null default 0,
		cached_tokens integer not null default 0,
		cache_tokens integer not null default 0,
		cache_read_tokens integer not null default 0,
		cache_creation_tokens integer not null default 0,
		normalized_uncached_input_tokens integer,
		normalized_total_input_tokens integer,
		normalized_cache_read_tokens integer,
		normalized_cache_creation_tokens integer,
		total_tokens integer not null default 0,
		latency_ms integer,
		ttft_ms integer,
		failed integer not null default 0,
		fail_status_code integer,
		fail_summary text,
		response_metadata_json text,
		header_quota_recover_at_ms integer,
		header_quota_used_percent real,
		header_quota_plan_type text,
		header_error_kind text,
		header_error_code text,
		header_trace_id text,
		fail_body text,
		raw_json text,
		response_model text,
		session_id text,
		parent_session_id text,
		access_token_sha256 text,
		generate integer,
		stream integer,
		created_at_ms integer not null
	);`

	legacyModelPricesSchema := `
	create table model_prices (
		model text primary key,
		prompt_per_1m real not null default 0,
		completion_per_1m real not null default 0,
		cache_per_1m real not null default 0,
		cache_read_per_1m real not null default 0,
		cache_creation_per_1m real not null default 0,
		prompt_configured integer not null default 0,
		completion_configured integer not null default 0,
		cache_read_configured integer not null default 0,
		cache_creation_configured integer not null default 0,
		source text,
		source_model_id text,
		raw_json text,
		updated_at_ms integer not null default 0,
		synced_at_ms integer
	);`

	if _, err := rawDB.Exec(legacyUsageEventsSchema); err != nil {
		t.Fatalf("failed to create legacy usage_events schema: %v", err)
	}
	if _, err := rawDB.Exec(legacyModelPricesSchema); err != nil {
		t.Fatalf("failed to create legacy model_prices schema: %v", err)
	}

	// 4. 写入旧Chat事件和旧价格
	_, err = rawDB.Exec(`
		insert into usage_events (
			event_hash, timestamp_ms, timestamp, model, input_tokens, output_tokens, cached_tokens, failed, created_at_ms
		) values (
			'0000000000000000000000000000000000000000000000000000000000000001', 1700000000000, '2023-11-14T22:13:20Z', 'gpt-4o', 1000000, 200000, 100000, 0, 1700000000000
		)
	`)
	if err != nil {
		t.Fatalf("insert legacy chat event: %v", err)
	}

	_, err = rawDB.Exec(`
		insert into model_prices (
			model, prompt_per_1m, completion_per_1m, prompt_configured, completion_configured, raw_json, updated_at_ms
		) values (
			'gpt-4o', 2.5, 10.0, 1, 1, '{"prompt":2.5,"completion":10.0}', 1700000000000
		)
	`)
	if err != nil {
		t.Fatalf("insert legacy model price: %v", err)
	}

	if err := rawDB.Close(); err != nil {
		t.Fatalf("close raw sqlite: %v", err)
	}

	// 2. 执行当前migration（使用 Open 自动执行 migrate）
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open and migrate db: %v", err)
	}
	defer func() { _ = db.Close() }()

	// 3. 重复执行migration（幂等性验证）
	if err := ensureUsageEventSnapshotColumns(db); err != nil {
		t.Fatalf("repeat ensureUsageEventSnapshotColumns failed: %v", err)
	}
	if err := ensureModelPriceColumns(db); err != nil {
		t.Fatalf("repeat ensureModelPriceColumns failed: %v", err)
	}

	// 11. 验证 migration 不会丢字段，且新增列全部就绪
	eventCols := map[string]bool{}
	rows, err := db.Query(`pragma table_info(usage_events)`)
	if err != nil {
		t.Fatalf("query table_info: %v", err)
	}
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dflt any
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan col: %v", err)
		}
		eventCols[name] = true
	}
	_ = rows.Close()

	requiredEventCols := []string{
		"event_hash", "model", "input_tokens", "output_tokens",
		"capability", "billing_unit", "quantity", "input_quantity", "output_quantity",
	}
	for _, col := range requiredEventCols {
		if !eventCols[col] {
			t.Fatalf("missing expected column %s in migrated usage_events", col)
		}
	}

	// 9. 验证旧事件数据与费用完整性
	var legacyTokensIn, legacyTokensOut int64
	var legacyModel string
	var legacyCap sql.NullString
	if err := db.QueryRow(`select model, input_tokens, output_tokens, capability from usage_events where event_hash = '0000000000000000000000000000000000000000000000000000000000000001'`).Scan(&legacyModel, &legacyTokensIn, &legacyTokensOut, &legacyCap); err != nil {
		t.Fatalf("query legacy event: %v", err)
	}
	if legacyTokensIn != 1000000 || legacyTokensOut != 200000 {
		t.Fatalf("legacy tokens changed! got %d/%d, want 1M/200k", legacyTokensIn, legacyTokensOut)
	}
	if legacyCap.Valid && legacyCap.String != "" {
		t.Fatalf("legacy capability expected empty, got %s", legacyCap.String)
	}

	// 5. 写入新 ASR / Image / Video 事件
	eventRepo := usageeventrepo.New(db)
	priceRepo := modelpricerepo.New(db)
	ctx := t.Context()

	// 保存新能力价格
	now := time.Now().UnixMilli()
	newPrices := map[string]model.ModelPrice{
		"whisper-1": {
			Capability:  "asr",
			BillingUnit: "audio_seconds",
			UnitPrice:   0.006, // $0.006 / sec
			RawJSON:     "{}",
			UpdatedAtMS: now,
		},
		"dall-e-3": {
			Capability:  "image",
			BillingUnit: "images",
			UnitPrice:   0.040, // $0.040 / img
			RawJSON:     "{}",
			UpdatedAtMS: now,
		},
		"sora-video": {
			Capability:  "video",
			BillingUnit: "video_tasks",
			UnitPrice:   0.50, // $0.50 / task
			RawJSON:     "{}",
			UpdatedAtMS: now,
		},
	}
	if err := priceRepo.ReplaceAll(ctx, newPrices); err != nil {
		t.Fatalf("save new model prices: %v", err)
	}

	// 写入新事件
	newEvents := []usage.Event{
		{
			EventHash:     "1000000000000000000000000000000000000000000000000000000000000001",
			TimestampMS:   now,
			Timestamp:     time.Now().UTC().Format(time.RFC3339),
			Model:         "whisper-1",
			Capability:    "asr",
			BillingUnit:   "audio_seconds",
			Quantity:      50, // 50 seconds -> $0.30
			InputQuantity: 50,
			CreatedAtMS:   now,
		},
		{
			EventHash:      "1000000000000000000000000000000000000000000000000000000000000002",
			TimestampMS:    now + 1,
			Timestamp:      time.Now().UTC().Format(time.RFC3339),
			Model:          "dall-e-3",
			Capability:     "image",
			BillingUnit:    "images",
			Quantity:       2, // 2 images -> $0.08
			OutputQuantity: 2,
			CreatedAtMS:    now + 1,
		},
		{
			EventHash:   "1000000000000000000000000000000000000000000000000000000000000003",
			TimestampMS: now + 2,
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			Model:       "sora-video",
			Capability:  "video",
			BillingUnit: "video_tasks",
			Quantity:    1, // 1 task -> $0.50
			CreatedAtMS: now + 2,
		},
		{
			EventHash:   "1000000000000000000000000000000000000000000000000000000000000004",
			TimestampMS: now + 3,
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			Model:       "sora-video",
			Capability:  "video",
			BillingUnit: "video_tasks",
			Quantity:    0, // Status poll -> $0.00
			CreatedAtMS: now + 3,
		},
	}
	insertResult, err := eventRepo.InsertBatch(ctx, newEvents)
	if err != nil {
		t.Fatalf("save new events: %v", err)
	}
	if insertResult.Inserted != 4 {
		t.Fatalf("expected 4 events inserted, got %d", insertResult.Inserted)
	}

	// 10. 验证非Token事件按正确单位计费
	pricesMap, err := priceRepo.LoadAll(ctx)
	if err != nil {
		t.Fatalf("get prices map: %v", err)
	}

	// ASR
	asrTokens := pricing.ModelTokens{
		Capability:  "asr",
		BillingUnit: "audio_seconds",
		Quantity:    50,
	}
	asrCostRes := pricing.EvaluateCost([]string{"whisper-1"}, "", asrTokens, pricesMap)
	if math.Abs(asrCostRes.Cost-0.30) > 1e-6 {
		t.Fatalf("evaluated ASR cost = %v, want 0.30", asrCostRes.Cost)
	}
	if asrCostRes.BillingUnit != "audio_seconds" {
		t.Fatalf("evaluated ASR unit = %v, want audio_seconds", asrCostRes.BillingUnit)
	}

	// Image
	imgTokens := pricing.ModelTokens{
		Capability:  "image",
		BillingUnit: "images",
		Quantity:    2,
	}
	imgCostRes := pricing.EvaluateCost([]string{"dall-e-3"}, "", imgTokens, pricesMap)
	if math.Abs(imgCostRes.Cost-0.08) > 1e-6 {
		t.Fatalf("evaluated Image cost = %v, want 0.08", imgCostRes.Cost)
	}
	if imgCostRes.BillingUnit != "images" {
		t.Fatalf("evaluated Image unit = %v, want images", imgCostRes.BillingUnit)
	}

	// Video submit & poll
	vidSubmitTokens := pricing.ModelTokens{
		Capability:  "video",
		BillingUnit: "video_tasks",
		Quantity:    1,
	}
	vidSubmitRes := pricing.EvaluateCost([]string{"sora-video"}, "", vidSubmitTokens, pricesMap)
	if math.Abs(vidSubmitRes.Cost-0.50) > 1e-6 {
		t.Fatalf("evaluated Video submit cost = %v, want 0.50", vidSubmitRes.Cost)
	}
	vidPollTokens := pricing.ModelTokens{
		Capability:  "video",
		BillingUnit: "video_tasks",
		Quantity:    0,
	}
	vidPollRes := pricing.EvaluateCost([]string{"sora-video"}, "", vidPollTokens, pricesMap)
	if vidPollRes.Cost != 0 {
		t.Fatalf("evaluated Video poll cost = %v, want 0", vidPollRes.Cost)
	}

	// 6. 查询聚合 (ModelStatsWithFilter)
	filter := usageeventrepo.AnalyticsFilter{
		FromMS: 0,
		ToMS:   now + 3600_000,
	}
	stats, err := eventRepo.ModelStatsWithFilter(ctx, filter, 100)
	if err != nil {
		t.Fatalf("query model stats with filter: %v", err)
	}
	t.Logf("total aggregated stats returned: %d", len(stats))
	statByModel := map[string]usageeventrepo.ModelStat{}
	for _, s := range stats {
		t.Logf("found aggregated model: %q, calls: %d", s.Model, s.Calls)
		statByModel[s.Model] = s
	}

	// gpt-4o (Chat)
	chatStat, ok := statByModel["gpt-4o"]
	if !ok {
		t.Fatalf("chat model stat not found in aggregation")
	}
	if chatStat.InputTokens != 1000000 || chatStat.OutputTokens != 200000 {
		t.Fatalf("chat stat tokens mismatch: %#v", chatStat)
	}

	// whisper-1 (ASR)
	asrStat, ok := statByModel["whisper-1"]
	if !ok {
		t.Fatalf("asr model stat not found in aggregation")
	}
	if asrStat.Capability != "asr" || asrStat.BillingUnit != "audio_seconds" || math.Abs(asrStat.Quantity-50) > 1e-6 {
		t.Fatalf("asr stat mismatch: %#v", asrStat)
	}

	// dall-e-3 (Image)
	imgStat, ok := statByModel["dall-e-3"]
	if !ok {
		t.Fatalf("image model stat not found in aggregation")
	}
	if imgStat.Capability != "image" || imgStat.BillingUnit != "images" || math.Abs(imgStat.Quantity-2) > 1e-6 {
		t.Fatalf("image stat mismatch: %#v", imgStat)
	}

	// sora-video (Video: 1 submit + 1 poll = 2 calls, 1 quantity)
	vidStat, ok := statByModel["sora-video"]
	if !ok {
		t.Fatalf("video model stat not found in aggregation")
	}
	if vidStat.Capability != "video" || vidStat.BillingUnit != "video_tasks" || math.Abs(vidStat.Quantity-1) > 1e-6 {
		t.Fatalf("video stat mismatch: %#v", vidStat)
	}
	if vidStat.Calls != 2 {
		t.Fatalf("video total calls = %d, want 2", vidStat.Calls)
	}
}
