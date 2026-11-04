package approval

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Correlation describes whether a tool call has a trustworthy approval request
// reference. A pending tool call without an explicit ID remains uncorrelated.
type Correlation string

const (
	CorrelationUnknown  Correlation = "unknown"
	CorrelationTrusted  Correlation = "trusted"
	CorrelationConflict Correlation = "conflict"
)

// Reference is metadata accepted from a review_pending result. Harness does not
// infer any of these fields from timing, text, or nearby events.
type Reference struct {
	Correlation Correlation `json:"correlation"`
	ID          string      `json:"approval_id,omitempty"`
	SessionID   string      `json:"session_id,omitempty"`
	ExpiresAt   *time.Time  `json:"expires_at,omitempty"`
	Summary     string      `json:"summary,omitempty"`
}

func UncorrelatedReference() Reference {
	return Reference{Correlation: CorrelationUnknown}
}

func ConflictingReference() Reference {
	return Reference{Correlation: CorrelationConflict}
}

// TrustedReference validates the explicit correlation fields supplied by the
// remote contract. Callers must not synthesize missing values.
func TrustedReference(id, sessionID string, expiresAt *time.Time) (Reference, error) {
	if err := validateReferenceToken("approval ID", id); err != nil {
		return Reference{}, err
	}
	if sessionID != "" {
		if err := validateReferenceToken("approval session ID", sessionID); err != nil {
			return Reference{}, err
		}
	}

	var expiresCopy *time.Time
	if expiresAt != nil {
		value := expiresAt.UTC()
		expiresCopy = &value
	}
	return Reference{
		Correlation: CorrelationTrusted,
		ID:          id,
		SessionID:   sessionID,
		ExpiresAt:   expiresCopy,
	}, nil
}

func validateReferenceToken(label, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", label)
	}
	if len(value) > 128 {
		return fmt.Errorf("%s exceeds 128 characters", label)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s contains surrounding whitespace", label)
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return fmt.Errorf("%s contains whitespace or control characters", label)
		}
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' || r == ':') {
			return fmt.Errorf("%s contains an unsafe character", label)
		}
	}
	return nil
}
