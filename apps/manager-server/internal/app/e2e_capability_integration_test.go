package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/httpapi"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	dashboardsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/dashboard"
	monitoringsvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/monitoring"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/testutil"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

func TestEndToEndCapabilityProviderPricingAndMonitoring(t *testing.T) {
	cfg := testutil.NewConfig(t)
	st := testutil.NewStore(t, cfg)
	srv := httpapi.New(cfg, st, nil)

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	client := ts.Client()
	adminAuth := "Bearer " + testutil.AdminKey

	// 1. ModelPrice 读取与保存验证
	// 1.1 保存未知 billing unit 应当被后端拒绝 (400 Bad Request)
	invalidPrices := map[string]model.ModelPrice{
		"invalid-model": {
			Capability:  "image",
			BillingUnit: "unsupported_box_units",
			UnitPrice:   1.0,
		},
	}
	invalidReqBody, _ := json.Marshal(map[string]any{"prices": invalidPrices})
	reqInvalid, _ := http.NewRequest(http.MethodPut, ts.URL+"/v0/management/model-prices", bytes.NewReader(invalidReqBody))
	reqInvalid.Header.Set("Authorization", adminAuth)
	reqInvalid.Header.Set("Content-Type", "application/json")
	respInvalid, err := client.Do(reqInvalid)
	if err != nil {
		t.Fatalf("PUT model-prices invalid unit: %v", err)
	}
	if respInvalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for unknown billing unit, got %d", respInvalid.StatusCode)
	}
	_ = respInvalid.Body.Close()

	// 1.2 保存合法的多能力价格（包含 unitPrice=0 显式免费处理）
	validPrices := map[string]model.ModelPrice{
		"gpt-4o": {
			Prompt:     2.5,
			Completion: 10.0,
		},
		"whisper-1": {
			Capability:  "asr",
			BillingUnit: "audio_seconds",
			UnitPrice:   0.006, // $0.006 / sec
		},
		"dall-e-3": {
			Capability:  "image",
			BillingUnit: "images",
			UnitPrice:   0.040, // $0.040 / img
		},
		"sora-video": {
			Capability:  "video",
			BillingUnit: "video_tasks",
			UnitPrice:   0.50, // $0.50 / task
		},
		"free-tts": {
			Capability:  "tts",
			BillingUnit: "characters",
			UnitPrice:   0.0, // 显式 0 免费
		},
	}
	validReqBody, _ := json.Marshal(map[string]any{"prices": validPrices})
	reqValid, _ := http.NewRequest(http.MethodPut, ts.URL+"/v0/management/model-prices", bytes.NewReader(validReqBody))
	reqValid.Header.Set("Authorization", adminAuth)
	reqValid.Header.Set("Content-Type", "application/json")
	respValid, err := client.Do(reqValid)
	if err != nil {
		t.Fatalf("PUT model-prices valid: %v", err)
	}
	if respValid.StatusCode != http.StatusOK {
		t.Fatalf("PUT model-prices status = %d", respValid.StatusCode)
	}
	_ = respValid.Body.Close()

	// 1.3 读取已保存的 ModelPrices 并验证字段与 unitPrice=0 保留
	reqGetPrices, _ := http.NewRequest(http.MethodGet, ts.URL+"/v0/management/model-prices", nil)
	reqGetPrices.Header.Set("Authorization", adminAuth)
	respGetPrices, err := client.Do(reqGetPrices)
	if err != nil {
		t.Fatalf("GET model-prices: %v", err)
	}
	if respGetPrices.StatusCode != http.StatusOK {
		t.Fatalf("GET model-prices status = %d", respGetPrices.StatusCode)
	}
	var getPricesResult struct {
		Prices map[string]model.ModelPrice `json:"prices"`
	}
	if err := json.NewDecoder(respGetPrices.Body).Decode(&getPricesResult); err != nil {
		t.Fatalf("decode GET model-prices: %v", err)
	}
	_ = respGetPrices.Body.Close()

	savedWhisper, ok := getPricesResult.Prices["whisper-1"]
	if !ok || savedWhisper.Capability != "asr" || savedWhisper.BillingUnit != "audio_seconds" || math.Abs(savedWhisper.UnitPrice-0.006) > 1e-6 {
		t.Fatalf("whisper-1 price mismatch: %#v", savedWhisper)
	}
	savedFreeTTS, ok := getPricesResult.Prices["free-tts"]
	if !ok || savedFreeTTS.Capability != "tts" || savedFreeTTS.BillingUnit != "characters" || savedFreeTTS.UnitPrice != 0.0 {
		t.Fatalf("free-tts zero price lost or changed: %#v", savedFreeTTS)
	}

	// 2. 写入混合 UsageEvents (Chat、ASR、Image、Video Submit 与 Video Status Poll)
	now := time.Now().UnixMilli()
	events := []usage.Event{
		// Chat event: 1M in, 100k out -> cost = 2.5 + 1.0 = 3.5
		{
			EventHash:    fmt.Sprintf("%064x", 101),
			TimestampMS:  now,
			Timestamp:    time.Now().UTC().Format(time.RFC3339),
			Model:        "gpt-4o",
			InputTokens:  1_000_000,
			OutputTokens: 100_000,
			CreatedAtMS:  now,
		},
		// ASR event: 50 seconds -> cost = 50 * 0.006 = 0.30
		{
			EventHash:     fmt.Sprintf("%064x", 102),
			TimestampMS:   now + 1,
			Timestamp:     time.Now().UTC().Format(time.RFC3339),
			Model:         "whisper-1",
			Capability:    "asr",
			BillingUnit:   "audio_seconds",
			Quantity:      50,
			InputQuantity: 50,
			CreatedAtMS:   now + 1,
		},
		// Image event: 3 images -> cost = 3 * 0.040 = 0.12
		{
			EventHash:      fmt.Sprintf("%064x", 103),
			TimestampMS:    now + 2,
			Timestamp:      time.Now().UTC().Format(time.RFC3339),
			Model:          "dall-e-3",
			Capability:     "image",
			BillingUnit:    "images",
			Quantity:       3,
			OutputQuantity: 3,
			CreatedAtMS:    now + 2,
		},
		// Video submit event: 1 video task -> cost = 1 * 0.50 = 0.50
		{
			EventHash:   fmt.Sprintf("%064x", 104),
			TimestampMS: now + 3,
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			Model:       "sora-video",
			Capability:  "video",
			BillingUnit: "video_tasks",
			Quantity:    1,
			CreatedAtMS: now + 3,
		},
		// Video status poll event 1: quantity=0 -> cost = 0.00
		{
			EventHash:   fmt.Sprintf("%064x", 105),
			TimestampMS: now + 4,
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			Model:       "sora-video",
			Capability:  "video",
			BillingUnit: "video_tasks",
			Quantity:    0,
			CreatedAtMS: now + 4,
		},
		// Video status poll event 2: quantity=0 -> cost = 0.00
		{
			EventHash:   fmt.Sprintf("%064x", 106),
			TimestampMS: now + 5,
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			Model:       "sora-video",
			Capability:  "video",
			BillingUnit: "video_tasks",
			Quantity:    0,
			CreatedAtMS: now + 5,
		},
	}
	insertRes, err := st.UsageEvents.InsertBatch(context.Background(), events)
	if err != nil {
		t.Fatalf("InsertBatch events: %v", err)
	}
	if insertRes.Inserted != 6 {
		t.Fatalf("expected 6 events inserted, got %d", insertRes.Inserted)
	}

	// 2.1 重复事件幂等去重验证
	dupRes, err := st.UsageEvents.InsertBatch(context.Background(), events)
	if err != nil {
		t.Fatalf("InsertBatch duplicate events: %v", err)
	}
	if dupRes.Inserted != 0 {
		t.Fatalf("expected 0 duplicates inserted, got %d", dupRes.Inserted)
	}

	// 3. 查询 Dashboard 接口验证能力计费与总成本
	todayStartMS := now - (now % (24 * 3600 * 1000))
	dashboardURL := fmt.Sprintf("%s/v0/management/dashboard/summary?today_start_ms=%d&now_ms=%d", ts.URL, todayStartMS, now+10000)
	reqDashboard, _ := http.NewRequest(http.MethodGet, dashboardURL, nil)
	reqDashboard.Header.Set("Authorization", adminAuth)
	respDashboard, err := client.Do(reqDashboard)
	if err != nil {
		t.Fatalf("GET dashboard summary: %v", err)
	}
	if respDashboard.StatusCode != http.StatusOK {
		t.Fatalf("GET dashboard summary status = %d", respDashboard.StatusCode)
	}
	var dashSummary dashboardsvc.SummaryResponse
	if err := json.NewDecoder(respDashboard.Body).Decode(&dashSummary); err != nil {
		t.Fatalf("decode dashboard summary: %v", err)
	}
	_ = respDashboard.Body.Close()

	// 验证总成本: 3.5 (chat) + 0.30 (asr) + 0.12 (img) + 0.50 (video) = 4.42
	expectedTotalCost := 3.5 + 0.30 + 0.12 + 0.50
	if math.Abs(dashSummary.Today.TotalCost-expectedTotalCost) > 1e-3 {
		t.Fatalf("dashboard today cost = %v, want %v", dashSummary.Today.TotalCost, expectedTotalCost)
	}

	// 4. 查询 Monitoring Analytics 接口验证模型用量明细聚合
	monReqBody, _ := json.Marshal(monitoringsvc.Request{
		FromMS: todayStartMS,
		ToMS:   now + 10000,
		Include: monitoringsvc.Include{
			ModelStats: true,
		},
	})
	reqMon, _ := http.NewRequest(http.MethodPost, ts.URL+"/v0/management/monitoring/analytics", bytes.NewReader(monReqBody))
	reqMon.Header.Set("Authorization", adminAuth)
	reqMon.Header.Set("Content-Type", "application/json")
	respMon, err := client.Do(reqMon)
	if err != nil {
		t.Fatalf("POST monitoring analytics: %v", err)
	}
	if respMon.StatusCode != http.StatusOK {
		t.Fatalf("POST monitoring analytics status = %d", respMon.StatusCode)
	}
	var monResp monitoringsvc.Response
	if err := json.NewDecoder(respMon.Body).Decode(&monResp); err != nil {
		t.Fatalf("decode monitoring response: %v", err)
	}
	_ = respMon.Body.Close()

	monByModel := map[string]monitoringsvc.ModelStat{}
	for _, m := range monResp.ModelStats {
		monByModel[m.Model] = m
	}

	// Video: 3 requests (1 submit + 2 polls), but exactly 1 video_task billable quantity
	soraMon, ok := monByModel["sora-video"]
	if !ok {
		t.Fatalf("sora-video not found in monitoring stats")
	}
	if soraMon.Calls != 3 || soraMon.Quantity != 1 || soraMon.Capability != "video" || soraMon.BillingUnit != "video_tasks" {
		t.Fatalf("sora-video monitoring mismatch: %#v (expected 3 calls, 1 task)", soraMon)
	}

	// ASR: 50 seconds
	asrMon, ok := monByModel["whisper-1"]
	if !ok || asrMon.Quantity != 50 || asrMon.Capability != "asr" || asrMon.BillingUnit != "audio_seconds" {
		t.Fatalf("whisper-1 monitoring mismatch: %#v", asrMon)
	}

	// Image: 3 images
	imgMon, ok := monByModel["dall-e-3"]
	if !ok || imgMon.Quantity != 3 || imgMon.Capability != "image" || imgMon.BillingUnit != "images" {
		t.Fatalf("dall-e-3 monitoring mismatch: %#v", imgMon)
	}
}
