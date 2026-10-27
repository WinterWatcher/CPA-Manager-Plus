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
