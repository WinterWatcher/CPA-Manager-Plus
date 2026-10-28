package pricing

import (
	"math"
	"testing"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

func TestCapabilityPricingLegacyChat(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"gpt-4o": {
			Prompt:     2.5,
			Completion: 10.0,
			Cache:      1.25,
		},
	}

	tokens := ModelTokens{
		InputTokens:  1_000_000,
		OutputTokens: 500_000,
		CachedTokens: 200_000,
	}

	cost := CostForModel("gpt-4o", tokens, prices)
	// (800_000 * 2.5 + 200_000 * 1.25 + 500_000 * 10.0) / 1M = 2.0 + 0.25 + 5.0 = 7.25
	if math.Abs(cost-7.25) > 0.000001 {
		t.Fatalf("legacy chat cost = %v, want 7.25", cost)
	}

	// Explicit capability="chat", billingUnit="tokens"
	tokensChat := tokens
	tokensChat.Capability = "chat"
	tokensChat.BillingUnit = "tokens"
	costChat := CostForModel("gpt-4o", tokensChat, prices)
	if math.Abs(costChat-7.25) > 0.000001 {
		t.Fatalf("explicit chat cost = %v, want 7.25", costChat)
	}
}

func TestCapabilityPricingEmbedding(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"text-embedding-3-small": {
			Capability:  "embedding",
			BillingUnit: "tokens",
			Prompt:      0.02,
			Completion:  1.00, // Should NOT be used
		},
	}

	// 500 input tokens across multiple texts, but provider also returned 50 output tokens
	tokens := ModelTokens{
		Capability:   "embedding",
		BillingUnit:  "tokens",
		InputTokens:  500,
		OutputTokens: 50, // Must NOT generate completion fees
	}

	res := EvaluateCost([]string{"text-embedding-3-small"}, "", tokens, prices)
	expectedCost := 500.0 * 0.02 / PerMillion
	if math.Abs(res.Cost-expectedCost) > 1e-9 {
		t.Fatalf("embedding cost = %v, want %v", res.Cost, expectedCost)
	}
	if res.Status != "priced" {
		t.Fatalf("embedding status = %s, want priced", res.Status)
	}

	// CostForModel convenience wrapper
	if got := CostForModel("text-embedding-3-small", tokens, prices); math.Abs(got-expectedCost) > 1e-9 {
		t.Fatalf("CostForModel embedding = %v, want %v", got, expectedCost)
	}
}

func TestCapabilityPricingRerank(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"bge-reranker-large": {
			Capability:  "rerank",
			BillingUnit: "tokens",
			Prompt:      0.05,
			Completion:  0.20,
		},
		"cohere-rerank-v3": {
			Capability:  "rerank",
			BillingUnit: "characters",
			UnitPrice:   0.0001,
		},
	}

	// Token-based rerank: only input tokens charged, output tokens / ranked count ignored
	tokens := ModelTokens{
		Capability:   "rerank",
		BillingUnit:  "tokens",
		InputTokens:  2000,
		OutputTokens: 100, // Sorted results count must not be charged as completion tokens
	}
	resToken := EvaluateCost([]string{"bge-reranker-large"}, "", tokens, prices)
	wantTokenCost := 2000.0 * 0.05 / PerMillion
	if math.Abs(resToken.Cost-wantTokenCost) > 1e-9 {
		t.Fatalf("rerank token cost = %v, want %v", resToken.Cost, wantTokenCost)
	}

	// Quantity-based rerank (unit price)
	tokensQty := ModelTokens{
		Capability:  "rerank",
		BillingUnit: "characters",
		Quantity:    1500,
	}
	resQty := EvaluateCost([]string{"cohere-rerank-v3"}, "", tokensQty, prices)
	wantQtyCost := 1500.0 * 0.0001
	if math.Abs(resQty.Cost-wantQtyCost) > 1e-9 {
		t.Fatalf("rerank qty cost = %v, want %v", resQty.Cost, wantQtyCost)
	}
}

func TestCapabilityPricingASR(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"whisper-1": {
			Capability:  "asr",
			BillingUnit: "audio_seconds",
			UnitPrice:   0.006, // $0.006 per second ($0.36/min)
			Prompt:      10.0,  // should not be written to or used
			Completion:  20.0,  // should not be written to or used
		},
	}

	tokens := ModelTokens{
		Capability:    "asr",
		BillingUnit:   "audio_seconds",
		Quantity:      45.5,
		InputQuantity: 45.5,
	}

	res := EvaluateCost([]string{"whisper-1"}, "", tokens, prices)
	wantCost := 45.5 * 0.006
	if math.Abs(res.Cost-wantCost) > 1e-6 {
		t.Fatalf("asr cost = %v, want %v", res.Cost, wantCost)
	}
	if res.Status != "priced" {
		t.Fatalf("asr status = %s, want priced", res.Status)
	}

	// Missing audio seconds (quantity <= 0): marked unpriced / missing_quantity, 0 cost
	tokensMissing := ModelTokens{
		Capability:  "asr",
		BillingUnit: "audio_seconds",
		Quantity:    0,
	}
	resMissing := EvaluateCost([]string{"whisper-1"}, "", tokensMissing, prices)
	if resMissing.Cost != 0 {
		t.Fatalf("missing audio_seconds cost = %v, want 0", resMissing.Cost)
	}
	if resMissing.Status != "missing_quantity" {
		t.Fatalf("missing audio_seconds status = %s, want missing_quantity", resMissing.Status)
	}
}

func TestCapabilityPricingTTS(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"tts-1": {
			Capability:  "tts",
			BillingUnit: "characters",
			UnitPrice:   0.000015, // $15 / 1M chars
		},
		"tts-1-hd": {
			Capability:  "tts",
			BillingUnit: "audio_seconds",
			UnitPrice:   0.005,
		},
	}

	// Characters billing
	tokensChar := ModelTokens{
		Capability:  "tts",
		BillingUnit: "characters",
		Quantity:    250,
	}
	resChar := EvaluateCost([]string{"tts-1"}, "", tokensChar, prices)
	wantCharCost := 250.0 * 0.000015
	if math.Abs(resChar.Cost-wantCharCost) > 1e-9 {
		t.Fatalf("tts char cost = %v, want %v", resChar.Cost, wantCharCost)
	}

	// Missing quantity for TTS -> no charge
	tokensMissing := ModelTokens{
		Capability:  "tts",
		BillingUnit: "characters",
		Quantity:    0,
	}
	resMissing := EvaluateCost([]string{"tts-1"}, "", tokensMissing, prices)
	if resMissing.Cost != 0 {
		t.Fatalf("tts missing quantity cost = %v, want 0", resMissing.Cost)
	}
	if resMissing.Status != "missing_quantity" {
		t.Fatalf("tts missing quantity status = %s, want missing_quantity", resMissing.Status)
	}

	// Audio seconds billing for TTS
	tokensAudio := ModelTokens{
		Capability:  "tts",
		BillingUnit: "audio_seconds",
		Quantity:    12.0,
	}
	resAudio := EvaluateCost([]string{"tts-1-hd"}, "", tokensAudio, prices)
	wantAudioCost := 12.0 * 0.005
	if math.Abs(resAudio.Cost-wantAudioCost) > 1e-9 {
		t.Fatalf("tts audio cost = %v, want %v", resAudio.Cost, wantAudioCost)
	}
}

func TestCapabilityPricingImage(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"dall-e-3": {
			Capability:  "image",
			BillingUnit: "images",
			UnitPrice:   0.040, // $0.040 per image
		},
	}

	tokens := ModelTokens{
		Capability:     "image",
		BillingUnit:    "images",
		Quantity:       3,
		OutputQuantity: 3,
	}
	res := EvaluateCost([]string{"dall-e-3"}, "", tokens, prices)
	wantCost := 3.0 * 0.040
	if math.Abs(res.Cost-wantCost) > 1e-6 {
		t.Fatalf("image cost = %v, want %v", res.Cost, wantCost)
	}

	// Failed image request: 0 cost
	tokensFailed := tokens
	tokensFailed.Failed = true
	resFailed := EvaluateCost([]string{"dall-e-3"}, "", tokensFailed, prices)
	if resFailed.Cost != 0 {
		t.Fatalf("failed image cost = %v, want 0", resFailed.Cost)
	}
}

func TestCapabilityPricingVideo(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"sora-video": {
			Capability:  "video",
			BillingUnit: "video_tasks",
			UnitPrice:   0.50, // $0.50 per generation task
		},
	}

	// Submit success: 1 video task
	tokensSubmit := ModelTokens{
		Capability:  "video",
		BillingUnit: "video_tasks",
		Quantity:    1,
	}
	resSubmit := EvaluateCost([]string{"sora-video"}, "", tokensSubmit, prices)
	if math.Abs(resSubmit.Cost-0.50) > 1e-6 {
		t.Fatalf("video submit cost = %v, want 0.50", resSubmit.Cost)
	}

	// Status polling: 0 quantity, must not add fees
	tokensPoll := ModelTokens{
		Capability:  "video",
		BillingUnit: "video_tasks",
		Quantity:    0,
	}
	resPoll := EvaluateCost([]string{"sora-video"}, "", tokensPoll, prices)
	if resPoll.Cost != 0 {
		t.Fatalf("video poll cost = %v, want 0", resPoll.Cost)
	}

	// Failed submit: 0 cost
	tokensFail := ModelTokens{
		Capability:  "video",
		BillingUnit: "video_tasks",
		Quantity:    1,
		Failed:      true,
	}
	resFail := EvaluateCost([]string{"sora-video"}, "", tokensFail, prices)
	if resFail.Cost != 0 {
		t.Fatalf("video fail cost = %v, want 0", resFail.Cost)
	}
}

func TestCapabilityPricingUnknownBillingUnit(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"mystery-model": {
			Capability:  "custom",
			BillingUnit: "mystery_units",
			UnitPrice:   1.0,
			Prompt:      5.0,
		},
	}

	tokens := ModelTokens{
		Capability:  "custom",
		BillingUnit: "mystery_units",
		Quantity:    10,
		InputTokens: 1000,
	}

	// Must NOT treat unknown unit as tokens, must yield 0 cost
	res := EvaluateCost([]string{"mystery-model"}, "", tokens, prices)
	if res.Cost != 0 {
		t.Fatalf("unknown unit cost = %v, want 0", res.Cost)
	}
	if res.Status != "unsupported_unit" {
		t.Fatalf("unknown unit status = %s, want unsupported_unit", res.Status)
	}
}

func TestCapabilityPricingZeroUnitPrice(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"free-tts": {
			Capability:  "tts",
			BillingUnit: "characters",
			UnitPrice:   0.0,
		},
	}

	tokens := ModelTokens{
		Capability:  "tts",
		BillingUnit: "characters",
		Quantity:    500,
	}

	res := EvaluateCost([]string{"free-tts"}, "", tokens, prices)
	if res.Cost != 0 {
		t.Fatalf("zero unit price cost = %v, want 0", res.Cost)
	}
	if res.Status != "priced" {
		t.Fatalf("zero unit price status = %s, want priced", res.Status)
	}
}

func TestCapabilityPricingExplicitUnitPrice(t *testing.T) {
	prices := map[string]model.ModelPrice{
		"paid-model": {
			Capability:  "image",
			BillingUnit: "images",
			UnitPrice:   0.125,
		},
	}

	tokens := ModelTokens{
		Capability:  "image",
		BillingUnit: "images",
		Quantity:    8,
	}

	res := EvaluateCost([]string{"paid-model"}, "", tokens, prices)
	if math.Abs(res.Cost-1.0) > 1e-6 {
		t.Fatalf("explicit unit price cost = %v, want 1.0", res.Cost)
	}
}

func TestCapabilityPricingMixedChatAndNonChatSumCost(t *testing.T) {
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

	items := []Item{
		{
			Model: "gpt-4o",
			Tokens: ModelTokens{
				InputTokens:  1_000_000,
				OutputTokens: 100_000,
			}, // 2.5 + 1.0 = 3.5
		},
		{
			Model: "whisper-1",
			Tokens: ModelTokens{
				Capability:  "asr",
				BillingUnit: "audio_seconds",
				Quantity:    100, // 100 * 0.006 = 0.6
			},
		},
		{
			Model: "dall-e-3",
			Tokens: ModelTokens{
				Capability:  "image",
				BillingUnit: "images",
				Quantity:    5, // 5 * 0.04 = 0.2
			},
		},
	}

	total := SumCost(items, prices)
	wantTotal := 3.5 + 0.6 + 0.2 // 4.3
	if math.Abs(total-wantTotal) > 1e-6 {
		t.Fatalf("SumCost mixed = %v, want %v", total, wantTotal)
	}
}
