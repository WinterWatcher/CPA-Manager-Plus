package dashboard

import (
	"context"
	"math"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

func TestDashboardCapabilityAwareCostsAndCostRank(t *testing.T) {
	db := newDashboardTestStore(t)
	ctx := context.Background()

	// 1. Set model prices with various capabilities
	prices := map[string]model.ModelPrice{
		"gpt-4o": {
			Prompt:     2.5,
			Completion: 10.0,
		},
		"whisper-1": {
			Capability:  "asr",
			BillingUnit: "audio_seconds",
			UnitPrice:   0.006,
		},
		"dall-e-3": {
			Capability:  "image",
			BillingUnit: "images",
			UnitPrice:   0.040,
		},
		"sora": {
			Capability:  "video",
			BillingUnit: "video_tasks",
			UnitPrice:   0.50,
		},
	}
	if err := db.SaveModelPrices(ctx, prices); err != nil {
		t.Fatalf("SaveModelPrices: %v", err)
	}

	const baseMS = 1_800_000_000_000
	events := []usage.Event{
		// Chat event: 1M input, 100k output -> cost = 2.5 + 1.0 = 3.5
		{
			EventHash:    testCanonicalHash("ev-chat"),
			TimestampMS:  baseMS + 1000,
			Timestamp:    "2027-01-15T00:00:01Z",
			Model:        "gpt-4o",
			InputTokens:  1_000_000,
			OutputTokens: 100_000,
			TotalTokens:  1_100_000,
		},
		// ASR event: 100 audio seconds -> cost = 100 * 0.006 = 0.6
		{
			EventHash:     testCanonicalHash("ev-asr"),
			TimestampMS:   baseMS + 2000,
			Timestamp:     "2027-01-15T00:00:02Z",
			Model:         "whisper-1",
			Capability:    "asr",
			BillingUnit:   "audio_seconds",
			Quantity:      100,
			InputQuantity: 100,
		},
		// Image event: 5 images -> cost = 5 * 0.04 = 0.2
		{
			EventHash:      testCanonicalHash("ev-image"),
			TimestampMS:    baseMS + 3000,
			Timestamp:      "2027-01-15T00:00:03Z",
			Model:          "dall-e-3",
			Capability:     "image",
			BillingUnit:    "images",
			Quantity:       5,
			OutputQuantity: 5,
		},
		// Video submit success: 1 video task -> cost = 1 * 0.50 = 0.5
		{
			EventHash:   testCanonicalHash("ev-video-submit"),
			TimestampMS: baseMS + 4000,
			Timestamp:   "2027-01-15T00:00:04Z",
			Model:       "sora",
			Method:      "POST",
			Path:        "/v1/videos/generations",
			Capability:  "video",
			BillingUnit: "video_tasks",
			Quantity:    1,
		},
		// Video status poll: 0 task -> cost = 0 (requests count incremented, no extra task cost)
		{
			EventHash:   testCanonicalHash("ev-video-poll"),
			TimestampMS: baseMS + 5000,
			Timestamp:   "2027-01-15T00:00:05Z",
			Model:       "sora",
			Method:      "GET",
			Path:        "/v1/videos/tasks/task-999",
			Capability:  "video",
			BillingUnit: "video_tasks",
			Quantity:    0,
		},
		// Failed image event: 2 images, failed=true -> cost = 0
		{
			EventHash:      testCanonicalHash("ev-image-fail"),
			TimestampMS:    baseMS + 6000,
			Timestamp:      "2027-01-15T00:00:06Z",
			Model:          "dall-e-3",
			Capability:     "image",
			BillingUnit:    "images",
			Quantity:       2,
			OutputQuantity: 2,
			Failed:         true,
			FailStatusCode: 500,
		},
		// ASR with missing quantity: quantity=0 -> cost = 0
		{
			EventHash:   testCanonicalHash("ev-asr-missing"),
			TimestampMS: baseMS + 7000,
			Timestamp:   "2027-01-15T00:00:07Z",
			Model:       "whisper-1",
			Capability:  "asr",
			BillingUnit: "audio_seconds",
			Quantity:    0,
		},
	}

	if _, err := db.InsertEvents(ctx, events); err != nil {
		t.Fatalf("InsertEvents: %v", err)
	}

	service := New(db)
	resp, err := service.Summary(ctx, SummaryParams{
		TodayStartMS: baseMS,
		NowMS:        baseMS + 8000,
	})
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}

	// Total cost = 3.5 (chat) + 0.6 (asr) + 0.2 (image) + 0.5 (video submit) = 4.8
	const expectedCost = 4.8
	if math.Abs(resp.Today.TotalCost-expectedCost) > 1e-6 {
		t.Fatalf("Today.TotalCost = %v, want %v", resp.Today.TotalCost, expectedCost)
	}

	// Verify ModelCostRank
	if len(resp.ModelCostRank) != 4 {
		t.Fatalf("expected 4 models in cost rank, got %d: %#v", len(resp.ModelCostRank), resp.ModelCostRank)
	}

	// Verify rank 1: gpt-4o (3.5)
	if resp.ModelCostRank[0].Model != "gpt-4o" || math.Abs(resp.ModelCostRank[0].Cost-3.5) > 1e-6 {
		t.Fatalf("rank 0 = %#v, want gpt-4o with cost 3.5", resp.ModelCostRank[0])
	}
	if math.Abs(resp.ModelCostRank[0].CostShare-1.0) > 1e-6 {
		t.Fatalf("rank 0 cost share = %v, want 1.0", resp.ModelCostRank[0].CostShare)
	}

	// Verify rank 2: whisper-1 (0.6)
	if resp.ModelCostRank[1].Model != "whisper-1" || math.Abs(resp.ModelCostRank[1].Cost-0.6) > 1e-6 {
		t.Fatalf("rank 1 = %#v, want whisper-1 with cost 0.6", resp.ModelCostRank[1])
	}

	// Verify rank 3: sora (0.5)
	if resp.ModelCostRank[2].Model != "sora" || math.Abs(resp.ModelCostRank[2].Cost-0.5) > 1e-6 {
		t.Fatalf("rank 2 = %#v, want sora with cost 0.5", resp.ModelCostRank[2])
	}

	// Verify rank 4: dall-e-3 (0.2)
	if resp.ModelCostRank[3].Model != "dall-e-3" || math.Abs(resp.ModelCostRank[3].Cost-0.2) > 1e-6 {
		t.Fatalf("rank 3 = %#v, want dall-e-3 with cost 0.2", resp.ModelCostRank[3])
	}
}
