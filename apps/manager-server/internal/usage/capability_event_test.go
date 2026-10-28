package usage

import (
	"encoding/json"
	"testing"
)

func TestEventCapabilityQuantitiesRoundTripAndLegacyJSON(t *testing.T) {
	var legacy Event
	if err := json.Unmarshal([]byte(`{"model":"legacy","input_tokens":3,"output_tokens":2}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Capability != "" || legacy.BillingUnit != "" || legacy.Quantity != 0 {
		t.Fatalf("legacy event gained capability fields: %#v", legacy)
	}

	want := Event{
		Model:         "SenseVoiceSmall",
		Capability:    "asr",
		BillingUnit:   "audio_seconds",
		Quantity:      12.5,
		InputQuantity: 12.5,
	}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Event
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Capability != want.Capability || got.BillingUnit != want.BillingUnit || got.Quantity != want.Quantity || got.InputQuantity != want.InputQuantity {
		t.Fatalf("capability usage round trip = %#v", got)
	}
}

func TestNormalizeRawCapabilityFields(t *testing.T) {
	// 1. Asr event
	asrRaw := []byte(`{
		"timestamp": "2026-10-28T10:00:00Z",
		"model": "SenseVoiceSmall",
		"capability": "asr",
		"billing_unit": "audio_seconds",
		"quantity": 30.5,
		"input_quantity": 30.5
	}`)
	evASR, err := NormalizeRaw(asrRaw)
	if err != nil {
		t.Fatalf("NormalizeRaw ASR: %v", err)
	}
	if evASR.Capability != "asr" || evASR.BillingUnit != "audio_seconds" || evASR.Quantity != 30.5 || evASR.InputQuantity != 30.5 {
		t.Fatalf("evASR = %#v", evASR)
	}

	// 2. Legacy event has empty capability
	legacyRaw := []byte(`{
		"timestamp": "2026-10-28T10:00:00Z",
		"model": "gpt-4o",
		"input_tokens": 100,
		"output_tokens": 50
	}`)
	evLegacy, err := NormalizeRaw(legacyRaw)
	if err != nil {
		t.Fatalf("NormalizeRaw legacy: %v", err)
	}
	if evLegacy.Capability != "" || evLegacy.BillingUnit != "" || evLegacy.Quantity != 0 {
		t.Fatalf("evLegacy gained capability: %#v", evLegacy)
	}

	// 3. Video submit vs status polling
	videoSubmitRaw := []byte(`{
		"timestamp": "2026-10-28T10:00:00Z",
		"model": "sora",
		"method": "POST",
		"path": "/v1/videos/generations",
		"capability": "video",
		"billing_unit": "video_tasks",
		"quantity": 1
	}`)
	evVideoSubmit, err := NormalizeRaw(videoSubmitRaw)
	if err != nil {
		t.Fatalf("NormalizeRaw video submit: %v", err)
	}
	if evVideoSubmit.Capability != "video" || evVideoSubmit.Quantity != 1 {
		t.Fatalf("evVideoSubmit = %#v", evVideoSubmit)
	}

	videoPollRaw := []byte(`{
		"timestamp": "2026-10-28T10:01:00Z",
		"model": "sora",
		"method": "GET",
		"path": "/v1/videos/tasks/task-12345",
		"capability": "video",
		"billing_unit": "video_tasks"
	}`)
	evVideoPoll, err := NormalizeRaw(videoPollRaw)
	if err != nil {
		t.Fatalf("NormalizeRaw video poll: %v", err)
	}
	if evVideoPoll.Quantity != 0 {
		t.Fatalf("video status poll must have quantity 0, got %v", evVideoPoll.Quantity)
	}
}

func TestEventHashDistinguishesCapabilityAndIsStableForLegacy(t *testing.T) {
	legacy := Event{
		Timestamp:   "2026-10-28T10:00:00Z",
		Model:       "gpt-4o",
		InputTokens: 100,
	}
	legacyHash1 := buildEventHash(legacy)
	legacyHash2 := buildEventHash(legacy)
	if legacyHash1 != legacyHash2 {
		t.Fatalf("hash not deterministic")
	}

	withCap := legacy
	withCap.Capability = "chat"
	withCap.BillingUnit = "tokens"
	withCap.Quantity = 100
	capHash := buildEventHash(withCap)
	if capHash == legacyHash1 {
		t.Fatalf("capability event should have differentiated hash from legacy empty-capability event")
	}
}
