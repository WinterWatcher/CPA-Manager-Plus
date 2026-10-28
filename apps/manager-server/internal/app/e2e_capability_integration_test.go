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
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/httpqueue"
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

	// 1.2 保存涵盖全能力（Chat, Embedding, Rerank, ASR, TTS, Image, Video）的多能力价格
	validPrices := map[string]model.ModelPrice{
		"gpt-4o": {
			Prompt:     2.5,
			Completion: 10.0,
		},
		"text-embedding-3-small": {
			Capability:  "embedding",
			BillingUnit: "tokens",
			Prompt:      0.02, // $0.02 / 1M input tokens
			Completion:  1.00, // Should NOT charge completion tokens
		},
		"cohere-rerank-v3": {
			Capability:  "rerank",
			BillingUnit: "characters",
			UnitPrice:   0.0001, // $0.0001 / character
		},
		"whisper-1": {
			Capability:  "asr",
			BillingUnit: "audio_seconds",
			UnitPrice:   0.006, // $0.006 / second
		},
		"tts-1": {
			Capability:  "tts",
			BillingUnit: "characters",
			UnitPrice:   0.000015, // $0.000015 / character
		},
		"tts-1-hd": {
			Capability:  "tts",
			BillingUnit: "audio_seconds",
			UnitPrice:   0.005, // $0.005 / audio second
		},
		"dall-e-3": {
			Capability:  "image",
			BillingUnit: "images",
			UnitPrice:   0.040, // $0.040 / image
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

	// 2. 本地 Mock CLIProxy Usage API 队列模拟与消费
	cpaMock := testutil.NewCPAMock(t)
	now := time.Now().UnixMilli()

	// 构造各能力真实上游事件 payload
	mockQueuePayloads := []string{
		// Chat event: 1M in, 100k out -> cost = 2.5 + 1.0 = 3.50
		fmt.Sprintf(`{"event_hash":"%064x","timestamp_ms":%d,"timestamp":"%s","model":"gpt-4o","input_tokens":1000000,"output_tokens":100000}`,
			101, now, time.Now().UTC().Format(time.RFC3339)),

		// Embedding event: 500k in, 50 out -> cost = 500k * 0.02 / 1M = 0.010 (不计 output tokens)
		fmt.Sprintf(`{"event_hash":"%064x","timestamp_ms":%d,"timestamp":"%s","model":"text-embedding-3-small","capability":"embedding","billing_unit":"tokens","input_tokens":500000,"output_tokens":50}`,
			102, now+1, time.Now().UTC().Format(time.RFC3339)),

		// Rerank event: 2,000 characters -> cost = 2000 * 0.0001 = 0.20
		fmt.Sprintf(`{"event_hash":"%064x","timestamp_ms":%d,"timestamp":"%s","model":"cohere-rerank-v3","capability":"rerank","billing_unit":"characters","quantity":2000,"input_quantity":2000}`,
			103, now+2, time.Now().UTC().Format(time.RFC3339)),

		// ASR event: 50 seconds -> cost = 50 * 0.006 = 0.30
		fmt.Sprintf(`{"event_hash":"%064x","timestamp_ms":%d,"timestamp":"%s","model":"whisper-1","capability":"asr","billing_unit":"audio_seconds","quantity":50,"input_quantity":50}`,
			104, now+3, time.Now().UTC().Format(time.RFC3339)),

		// TTS characters event: 1,000 chars -> cost = 1000 * 0.000015 = 0.015
		fmt.Sprintf(`{"event_hash":"%064x","timestamp_ms":%d,"timestamp":"%s","model":"tts-1","capability":"tts","billing_unit":"characters","quantity":1000}`,
			105, now+4, time.Now().UTC().Format(time.RFC3339)),

		// TTS audio_seconds event: 20 seconds -> cost = 20 * 0.005 = 0.10
		fmt.Sprintf(`{"event_hash":"%064x","timestamp_ms":%d,"timestamp":"%s","model":"tts-1-hd","capability":"tts","billing_unit":"audio_seconds","quantity":20}`,
			106, now+5, time.Now().UTC().Format(time.RFC3339)),

		// Image event (成功): 3 images -> cost = 3 * 0.040 = 0.12
		fmt.Sprintf(`{"event_hash":"%064x","timestamp_ms":%d,"timestamp":"%s","model":"dall-e-3","capability":"image","billing_unit":"images","quantity":3,"output_quantity":3,"failed":false}`,
			107, now+6, time.Now().UTC().Format(time.RFC3339)),

		// Image event (失败): 2 images, failed=true -> cost = 0.00 (失败不计成功费用)
		fmt.Sprintf(`{"event_hash":"%064x","timestamp_ms":%d,"timestamp":"%s","model":"dall-e-3","capability":"image","billing_unit":"images","quantity":2,"output_quantity":2,"failed":true,"fail_status_code":500}`,
			108, now+7, time.Now().UTC().Format(time.RFC3339)),

		// Video submit event (成功创建任务): 1 task -> cost = 1 * 0.50 = 0.50
		fmt.Sprintf(`{"event_hash":"%064x","timestamp_ms":%d,"timestamp":"%s","model":"sora-video","capability":"video","billing_unit":"video_tasks","quantity":1,"method":"POST","path":"/v1/videos/generations"}`,
			109, now+8, time.Now().UTC().Format(time.RFC3339)),

		// Video status poll 1: quantity=0 -> cost = 0.00 (轮询不得重复计费)
		fmt.Sprintf(`{"request_id":"req-video-poll-1","timestamp_ms":%d,"timestamp":"%s","model":"sora-video","capability":"video","billing_unit":"video_tasks","quantity":0,"method":"GET","path":"/v1/videos/tasks/task-abc-1"}`,
			now+9, time.Now().UTC().Format(time.RFC3339)),

		// Video status poll 2: quantity=0 -> cost = 0.00 (轮询不得重复计费)
		fmt.Sprintf(`{"request_id":"req-video-poll-2","timestamp_ms":%d,"timestamp":"%s","model":"sora-video","capability":"video","billing_unit":"video_tasks","quantity":0,"method":"GET","path":"/v1/videos/tasks/task-abc-1"}`,
			now+10, time.Now().UTC().Format(time.RFC3339)),

		// Video failed submit: failed=true -> cost = 0.00
		fmt.Sprintf(`{"event_hash":"%064x","timestamp_ms":%d,"timestamp":"%s","model":"sora-video","capability":"video","billing_unit":"video_tasks","quantity":1,"failed":true,"fail_status_code":429,"method":"POST","path":"/v1/videos/generations"}`,
			112, now+11, time.Now().UTC().Format(time.RFC3339)),

		// Missing quantity ASR event: quantity=0 -> cost = 0.00 (必须标记 missing_quantity/unpriced，不可按1Token收费)
		fmt.Sprintf(`{"event_hash":"%064x","timestamp_ms":%d,"timestamp":"%s","model":"whisper-1","capability":"asr","billing_unit":"audio_seconds","quantity":0}`,
			113, now+12, time.Now().UTC().Format(time.RFC3339)),
	}

	for _, payload := range mockQueuePayloads {
		cpaMock.EnqueueUsageItem(payload)
	}

	// 通过 HTTP 队列客户端从 Mock CLIProxy 消费
	qClient := httpqueue.New(cpaMock.URL(), cpaMock.ManagementKey)
	items, err := qClient.Pop(context.Background(), len(mockQueuePayloads)+10)
	if err != nil {
		t.Fatalf("Pop from mock CLIProxy usage queue: %v", err)
	}
	if len(items) != len(mockQueuePayloads) {
		t.Fatalf("popped items = %d, want %d", len(items), len(mockQueuePayloads))
	}

	// 规范化并插入存储
	events := make([]usage.Event, 0, len(items))
	for _, rawItem := range items {
		ev, err := usage.NormalizeRaw([]byte(rawItem))
		if err != nil {
			t.Fatalf("NormalizeRaw error for %s: %v", rawItem, err)
		}
		events = append(events, ev)
	}

	insertRes, err := st.UsageEvents.InsertBatch(context.Background(), events)
	if err != nil {
		t.Fatalf("InsertBatch events: %v", err)
	}
	if insertRes.Inserted != len(mockQueuePayloads) {
		t.Fatalf("expected %d events inserted, got %d", len(mockQueuePayloads), insertRes.Inserted)
	}

	// 重复事件去重验证 (再次插入相同批次，新增必须为 0)
	dupRes, err := st.UsageEvents.InsertBatch(context.Background(), events)
	if err != nil {
		t.Fatalf("InsertBatch duplicate events: %v", err)
	}
	if dupRes.Inserted != 0 {
		t.Fatalf("expected 0 duplicates inserted, got %d", dupRes.Inserted)
	}

	// 3. 查询 Dashboard 接口验证能力计费与费用汇总
	// 预期费用明细:
	// - gpt-4o (Chat): 2.50 + 1.00 = 3.50
	// - text-embedding-3-small (Embedding): 0.010
	// - cohere-rerank-v3 (Rerank): 0.20
	// - whisper-1 (ASR): 0.30
	// - tts-1 (TTS chars): 0.015
	// - tts-1-hd (TTS audio): 0.10
	// - dall-e-3 (Image): 0.12 (失败项费用 0)
	// - sora-video (Video): 0.50 (2次轮询与1次失败提交费用 0)
	// - missing quantity: 0.00
	// 总计: 3.50 + 0.010 + 0.20 + 0.30 + 0.015 + 0.10 + 0.12 + 0.50 = 4.745
	expectedTotalCost := 3.50 + 0.010 + 0.20 + 0.30 + 0.015 + 0.10 + 0.12 + 0.50

	todayStartMS := now - (now % (24 * 3600 * 1000))
	dashboardURL := fmt.Sprintf("%s/v0/management/dashboard/summary?today_start_ms=%d&now_ms=%d", ts.URL, todayStartMS, now+30000)
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

	if math.Abs(dashSummary.Today.TotalCost-expectedTotalCost) > 1e-3 {
		t.Fatalf("dashboard today cost = %v, want %v", dashSummary.Today.TotalCost, expectedTotalCost)
	}

	// 4. 查询 Monitoring Analytics 接口验证全维度用量聚合
	monReqBody, _ := json.Marshal(monitoringsvc.Request{
		FromMS: todayStartMS,
		ToMS:   now + 30000,
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

	// Video: 4 requests (1 submit + 2 polls + 1 fail), exactly 1 video_task billed
	soraMon, ok := monByModel["sora-video"]
	if !ok {
		t.Fatalf("sora-video not found in monitoring stats")
	}
	if soraMon.Calls != 4 || soraMon.Quantity != 1 || soraMon.Capability != "video" || soraMon.BillingUnit != "video_tasks" {
		t.Fatalf("sora-video monitoring mismatch: %#v (expected 4 calls, 1 task)", soraMon)
	}

	// Image: 2 requests (1 success + 1 fail), exactly 3 images billed
	imgMon, ok := monByModel["dall-e-3"]
	if !ok || imgMon.Calls != 2 || imgMon.Quantity != 3 || imgMon.Capability != "image" || imgMon.BillingUnit != "images" {
		t.Fatalf("dall-e-3 monitoring mismatch: %#v", imgMon)
	}

	// ASR: 2 requests (1 valid + 1 missing quantity), exactly 50 audio_seconds billed
	asrMon, ok := monByModel["whisper-1"]
	if !ok || asrMon.Calls != 2 || asrMon.Quantity != 50 || asrMon.Capability != "asr" || asrMon.BillingUnit != "audio_seconds" {
		t.Fatalf("whisper-1 monitoring mismatch: %#v", asrMon)
	}

	// Embedding: 500k input tokens
	embMon, ok := monByModel["text-embedding-3-small"]
	if !ok || embMon.InputTokens != 500000 || embMon.Capability != "embedding" {
		t.Fatalf("embedding monitoring mismatch: %#v", embMon)
	}

	// Rerank: 2,000 characters
	rerankMon, ok := monByModel["cohere-rerank-v3"]
	if !ok || rerankMon.Quantity != 2000 || rerankMon.Capability != "rerank" {
		t.Fatalf("rerank monitoring mismatch: %#v", rerankMon)
	}

	// TTS: tts-1 (1000 characters) and tts-1-hd (20 seconds)
	ttsCharMon, ok := monByModel["tts-1"]
	if !ok || ttsCharMon.Quantity != 1000 || ttsCharMon.BillingUnit != "characters" {
		t.Fatalf("tts-1 monitoring mismatch: %#v", ttsCharMon)
	}
	ttsAudioMon, ok := monByModel["tts-1-hd"]
	if !ok || ttsAudioMon.Quantity != 20 || ttsAudioMon.BillingUnit != "audio_seconds" {
		t.Fatalf("tts-1-hd monitoring mismatch: %#v", ttsAudioMon)
	}
}
