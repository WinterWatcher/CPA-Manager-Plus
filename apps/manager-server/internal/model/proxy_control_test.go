package model

import (
	"errors"
	"testing"
)

func TestValidateProxyPolicy(t *testing.T) {
	valid := DefaultProxyPolicy()
	if err := ValidateProxyPolicy(valid); err != nil {
		t.Fatalf("DefaultProxyPolicy failed validation: %v", err)
	}

	// Invalid country
	badCountry := valid
	badCountry.AllowedCountries = []string{"JPN", "US"}
	if err := ValidateProxyPolicy(badCountry); !errors.Is(err, ErrInvalidCountryCode) {
		t.Fatalf("expected ErrInvalidCountryCode, got %v", err)
	}

	// Invalid probe concurrency
	badConcurrency := valid
	badConcurrency.ProbeConcurrency = 0
	if err := ValidateProxyPolicy(badConcurrency); !errors.Is(err, ErrInvalidProbeConcurrency) {
		t.Fatalf("expected ErrInvalidProbeConcurrency, got %v", err)
	}
	badConcurrency.ProbeConcurrency = 25
	if err := ValidateProxyPolicy(badConcurrency); !errors.Is(err, ErrInvalidProbeConcurrency) {
		t.Fatalf("expected ErrInvalidProbeConcurrency, got %v", err)
	}

	// Invalid timeout
	badTimeout := valid
	badTimeout.ProbeTimeoutSeconds = 0
	if err := ValidateProxyPolicy(badTimeout); !errors.Is(err, ErrInvalidProbeTimeout) {
		t.Fatalf("expected ErrInvalidProbeTimeout, got %v", err)
	}
	badTimeout.ProbeTimeoutSeconds = 61
	if err := ValidateProxyPolicy(badTimeout); !errors.Is(err, ErrInvalidProbeTimeout) {
		t.Fatalf("expected ErrInvalidProbeTimeout, got %v", err)
	}

	// Invalid rounds
	badRounds := valid
	badRounds.HealthCheckRounds = 11
	if err := ValidateProxyPolicy(badRounds); !errors.Is(err, ErrInvalidHealthRounds) {
		t.Fatalf("expected ErrInvalidHealthRounds, got %v", err)
	}

	// Invalid failure threshold
	badFail := valid
	badFail.FailureThreshold = 0
	if err := ValidateProxyPolicy(badFail); !errors.Is(err, ErrInvalidFailureThreshold) {
		t.Fatalf("expected ErrInvalidFailureThreshold, got %v", err)
	}

	// Invalid success threshold
	badSuccess := valid
	badSuccess.SuccessThreshold = 0
	if err := ValidateProxyPolicy(badSuccess); !errors.Is(err, ErrInvalidSuccessThreshold) {
		t.Fatalf("expected ErrInvalidSuccessThreshold, got %v", err)
	}

	// Invalid cooldown
	badCooldown := valid
	badCooldown.CooldownSeconds = -1
	if err := ValidateProxyPolicy(badCooldown); !errors.Is(err, ErrInvalidCooldownSeconds) {
		t.Fatalf("expected ErrInvalidCooldownSeconds, got %v", err)
	}
}
