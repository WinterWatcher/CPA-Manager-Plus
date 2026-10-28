package modelprice_test

import (
	"context"
	"math"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

func TestModelPriceCapabilityAndUnitPricePersistence(t *testing.T) {
	ctx := context.Background()
	_, repo := openTestDB(t)

	initialPrices := map[string]model.ModelPrice{
		"whisper-1": {
			Capability:  "asr",
			BillingUnit: "audio_seconds",
			UnitPrice:   0.006,
			Source:      "manual",
		},
		"free-tts": {
			Capability:  "tts",
			BillingUnit: "characters",
			UnitPrice:   0.0,
			Source:      "manual",
		},
		"gpt-4o": {
			Prompt:               2.5,
			Completion:           10.0,
			PromptConfigured:     true,
			CompletionConfigured: true,
			Source:               "manual",
		},
	}

	if err := repo.ReplaceAll(ctx, initialPrices); err != nil {
		t.Fatalf("ReplaceAll failed: %v", err)
	}

	persisted, err := repo.LoadAll(ctx)
	if err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}

	// 1. Verify whisper-1
	pWhisper, ok := persisted["whisper-1"]
	if !ok {
		t.Fatalf("whisper-1 not found in persisted prices")
	}
	if pWhisper.Capability != "asr" || pWhisper.BillingUnit != "audio_seconds" || math.Abs(pWhisper.UnitPrice-0.006) > 1e-9 {
		t.Fatalf("whisper-1 price mismatch: %#v", pWhisper)
	}

	// 2. Verify free-tts (explicit zero price)
	pTTS, ok := persisted["free-tts"]
	if !ok {
		t.Fatalf("free-tts not found in persisted prices")
	}
	if pTTS.Capability != "tts" || pTTS.BillingUnit != "characters" || pTTS.UnitPrice != 0.0 {
		t.Fatalf("free-tts price mismatch: %#v", pTTS)
	}

	// 3. Verify gpt-4o (legacy chat rates preserved, empty capability/unit)
	pChat, ok := persisted["gpt-4o"]
	if !ok {
		t.Fatalf("gpt-4o not found in persisted prices")
	}
	if pChat.Prompt != 2.5 || pChat.Completion != 10.0 || pChat.Capability != "" {
		t.Fatalf("gpt-4o price mismatch: %#v", pChat)
	}
}

func TestModelPriceUpsertSyncedPreservesManualNonTokenPrice(t *testing.T) {
	ctx := context.Background()
	_, repo := openTestDB(t)

	manualPrice := map[string]model.ModelPrice{
		"custom-asr": {
			Capability:  "asr",
			BillingUnit: "audio_seconds",
			UnitPrice:   0.008,
			Source:      "manual",
		},
	}
	if err := repo.ReplaceAll(ctx, manualPrice); err != nil {
		t.Fatalf("ReplaceAll failed: %v", err)
	}

	// Sync attempting to overwrite manual-sourced non-token model
	syncPrices := map[string]model.ModelPrice{
		"custom-asr": {
			Prompt:      1.0,
			Completion:  2.0,
			Source:      "sync",
			Capability:  "chat",
			BillingUnit: "tokens",
		},
	}
	result, err := repo.UpsertSynced(ctx, syncPrices)
	if err != nil {
		t.Fatalf("UpsertSynced failed: %v", err)
	}
	if len(result.Preserved) != 1 || result.Preserved[0] != "custom-asr" {
		t.Fatalf("expected custom-asr to be preserved, got %#v", result.Preserved)
	}

	persisted, err := repo.LoadAll(ctx)
	if err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}
	if p := persisted["custom-asr"]; p.Capability != "asr" || p.UnitPrice != 0.008 {
		t.Fatalf("manual non-token price was overwritten: %#v", p)
	}
}

func TestModelPriceRejectsUnknownBillingUnit(t *testing.T) {
	ctx := context.Background()
	_, repo := openTestDB(t)

	invalidPrice := map[string]model.ModelPrice{
		"bad-model": {
			Capability:  "custom",
			BillingUnit: "apples",
			UnitPrice:   1.0,
		},
	}
	err := repo.ReplaceAll(ctx, invalidPrice)
	if err == nil {
		t.Fatalf("expected error for unknown billing unit 'apples', got nil")
	}
}
