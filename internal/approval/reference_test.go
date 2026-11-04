package approval

import (
	"strings"
	"testing"
	"time"
)

func TestTrustedReferenceValidatesAndNormalizesIdentity(t *testing.T) {
	expires := time.Date(2026, 10, 30, 2, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	ref, err := TrustedReference("approval-1", "session-1", &expires)
	if err != nil {
		t.Fatalf("TrustedReference() error = %v", err)
	}
	if ref.Correlation != CorrelationTrusted || ref.ID != "approval-1" || ref.SessionID != "session-1" {
		t.Fatalf("reference = %+v", ref)
	}
	if ref.ExpiresAt == nil || ref.ExpiresAt.Location() != time.UTC || !ref.ExpiresAt.Equal(expires) {
		t.Fatalf("expiry = %+v, want UTC equivalent", ref.ExpiresAt)
	}
}

func TestTrustedReferenceRejectsUnsafeIdentifiers(t *testing.T) {
	for _, values := range [][2]string{
		{"", ""},
		{" approval-1", ""},
		{"approval\n1", ""},
		{"../approval", ""},
		{"approval-1", "session 1"},
		{strings.Repeat("a", 129), ""},
	} {
		if _, err := TrustedReference(values[0], values[1], nil); err == nil {
			t.Fatalf("TrustedReference(%q, %q) error = nil", values[0], values[1])
		}
	}
}
