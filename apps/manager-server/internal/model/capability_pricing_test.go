package model

import (
	"encoding/json"
	"testing"
)

func TestModelPriceCapabilityPricingIsBackwardCompatible(t *testing.T) {
	var legacy ModelPrice
	if err := json.Unmarshal([]byte(`{"prompt":1.2,"completion":3.4}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Capability != "" || legacy.BillingUnit != "" || legacy.UnitPrice != 0 {
		t.Fatalf("legacy price gained capability fields: %#v", legacy)
	}
	if err := ValidateModelPrice("legacy", legacy); err != nil {
		t.Fatal(err)
	}

	price := ModelPrice{Capability: "asr", BillingUnit: "audio_seconds", UnitPrice: 0.00022}
	raw, err := json.Marshal(price)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip ModelPrice
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip.Capability != "asr" || roundTrip.BillingUnit != "audio_seconds" || roundTrip.UnitPrice != price.UnitPrice {
		t.Fatalf("capability price round trip = %#v", roundTrip)
	}
	if err := ValidateModelPrice("asr-model", roundTrip); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCapabilityPricingRejectsUnknownUnit(t *testing.T) {
	if err := ValidateCapabilityPricing(ModelPrice{Capability: "image", BillingUnit: "requests", UnitPrice: 1}); err == nil {
		t.Fatal("unknown billing unit accepted")
	}
	if err := ValidateCapabilityPricing(ModelPrice{Capability: "image"}); err == nil {
		t.Fatal("capability without billing unit accepted")
	}
}
