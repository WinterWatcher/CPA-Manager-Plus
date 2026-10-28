package monitoring

import (
	"context"
	"math"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/usage"
)

func TestMonitoringCapabilityAnalyticsAndFiltering(t *testing.T) {
	db := newMonitoringTestStore(t)
	ctx := context.Background()

	// 1. Setup price book
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
	}
	if err := db.SaveModelPrices(ctx, prices); err != nil {
		t.Fatalf("SaveModelPrices: %v", err)
	}

	const baseMS = 1_800_000_000_000
	events := []usage.Event{
		// Chat event: cost 3.5
		{
			EventHash:    testCanonicalHash("mon-chat"),
			TimestampMS:  baseMS + 1000,
			Timestamp:    "2027-01-15T00:00:01Z",
			Model:        "gpt-4o",
			InputTokens:  1_000_000,
			OutputTokens: 100_000,
			TotalTokens:  1_100_000,
		},
		// ASR event: cost 50 * 0.006 = 0.3
		{
			EventHash:     testCanonicalHash("mon-asr"),
			TimestampMS:   baseMS + 2000,
			Timestamp:     "2027-01-15T00:00:02Z",
			Model:         "whisper-1",
			Capability:    "asr",
			BillingUnit:   "audio_seconds",
			Quantity:      50,
			InputQuantity: 50,
		},
		// Image event: cost 3 * 0.040 = 0.12
		{
			EventHash:      testCanonicalHash("mon-image"),
			TimestampMS:    baseMS + 3000,
			Timestamp:      "2027-01-15T00:00:03Z",
			Model:          "dall-e-3",
			Capability:     "image",
			BillingUnit:    "images",
			Quantity:       3,
			OutputQuantity: 3,
		},
		// Failed image event: cost 0
		{
			EventHash:      testCanonicalHash("mon-image-fail"),
			TimestampMS:    baseMS + 4000,
			Timestamp:      "2027-01-15T00:00:04Z",
			Model:          "dall-e-3",
			Capability:     "image",
			BillingUnit:    "images",
			Quantity:       2,
			OutputQuantity: 2,
			Failed:         true,
			FailStatusCode: 500,
		},
	}

	if _, err := db.InsertEvents(ctx, events); err != nil {
		t.Fatalf("InsertEvents: %v", err)
	}

	service := New(db)

	// Query 1: Unfiltered Analytics
	resAll, err := service.Analytics(ctx, Request{
		FromMS: baseMS,
		ToMS:   baseMS + 10000,
		Include: Include{
			Summary:    true,
			ModelStats: true,
		},
	})
	if err != nil {
		t.Fatalf("Analytics unfiltered: %v", err)
	}

	// Total cost = 3.5 + 0.3 + 0.12 = 3.92
	if math.Abs(resAll.Summary.TotalCost-3.92) > 1e-6 {
		t.Fatalf("Summary.TotalCost = %v, want 3.92", resAll.Summary.TotalCost)
	}

	// Verify model stats
	if len(resAll.ModelStats) != 3 {
		t.Fatalf("expected 3 models, got %d: %#v", len(resAll.ModelStats), resAll.ModelStats)
	}
	modelMap := map[string]ModelStat{}
	for _, m := range resAll.ModelStats {
		modelMap[m.Model] = m
	}
	if w, ok := modelMap["whisper-1"]; !ok || w.Capability != "asr" || w.BillingUnit != "audio_seconds" || w.Quantity != 50 || math.Abs(w.Cost-0.3) > 1e-6 {
		t.Fatalf("whisper-1 stat mismatch: %#v", w)
	}
	if d, ok := modelMap["dall-e-3"]; !ok || d.Capability != "image" || d.BillingUnit != "images" || d.Quantity != 3 || math.Abs(d.Cost-0.12) > 1e-6 {
		t.Fatalf("dall-e-3 stat mismatch: %#v", d)
	}

	// Query 2: Filter by capability="asr"
	resASR, err := service.Analytics(ctx, Request{
		FromMS: baseMS,
		ToMS:   baseMS + 10000,
		Filters: Filters{
			Capabilities: []string{"asr"},
		},
		Include: Include{
			Summary:    true,
			ModelStats: true,
		},
	})
	if err != nil {
		t.Fatalf("Analytics ASR filter: %v", err)
	}
	if math.Abs(resASR.Summary.TotalCost-0.3) > 1e-6 {
		t.Fatalf("ASR filtered TotalCost = %v, want 0.3", resASR.Summary.TotalCost)
	}
	if len(resASR.ModelStats) != 1 || resASR.ModelStats[0].Model != "whisper-1" {
		t.Fatalf("expected only whisper-1 for ASR filter, got %#v", resASR.ModelStats)
	}

	// Query 3: Filter by billing_unit="images"
	resImg, err := service.Analytics(ctx, Request{
		FromMS: baseMS,
		ToMS:   baseMS + 10000,
		Filters: Filters{
			BillingUnits: []string{"images"},
		},
		Include: Include{
			Summary:    true,
			ModelStats: true,
		},
	})
	if err != nil {
		t.Fatalf("Analytics image filter: %v", err)
	}
	if math.Abs(resImg.Summary.TotalCost-0.12) > 1e-6 {
		t.Fatalf("Image filtered TotalCost = %v, want 0.12", resImg.Summary.TotalCost)
	}
	if len(resImg.ModelStats) != 1 || resImg.ModelStats[0].Model != "dall-e-3" {
		t.Fatalf("expected only dall-e-3 for images filter, got %#v", resImg.ModelStats)
	}
}
