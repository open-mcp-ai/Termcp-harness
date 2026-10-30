package controlapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"
)

var (
	ErrApprovalAuthRequired = errors.New("approval authorization is required")
	ErrApprovalAuthInvalid  = errors.New("approval authorization is invalid")
	ErrApprovalOriginDenied = errors.New("approval request origin is denied")
)

const (
	minApprovalTokenBytes = 32
	maxApprovalTokenBytes = 4096
)

// ApprovalAuthorizer protects mutating approval decisions. The bearer token is
// held only as a digest and is never returned in snapshots, errors, or events.
type ApprovalAuthorizer struct {
	tokenHash [sha256.Size]byte
}

func NewApprovalAuthorizer(token string) (*ApprovalAuthorizer, error) {
	if err := validateApprovalToken(token); err != nil {
		return nil, err
	}
	return &ApprovalAuthorizer{tokenHash: sha256.Sum256([]byte(token))}, nil
}

func (a *ApprovalAuthorizer) Authorize(request *http.Request) error {
	if a == nil {
		return ErrApprovalAuthRequired
	}
	if request == nil {
		return ErrApprovalAuthInvalid
	}
	// A browser origin makes this a cross-origin or same-origin web request.
	// Until a CSRF design exists, mutating approval requests are CLI/trusted-client
	// only and must omit Origin.
	if request.Header.Get("Origin") != "" {
		return ErrApprovalOriginDenied
	}
	authorization := request.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(authorization, prefix) {
		return ErrApprovalAuthInvalid
	}
	candidate := strings.TrimPrefix(authorization, prefix)
	candidateHash := sha256.Sum256([]byte(candidate))
	if subtle.ConstantTimeCompare(candidateHash[:], a.tokenHash[:]) != 1 {
		return ErrApprovalAuthInvalid
	}
	return nil
}

func validateApprovalToken(token string) error {
	if len(token) < minApprovalTokenBytes {
		return fmt.Errorf("approval token must be at least %d bytes", minApprovalTokenBytes)
	}
	if len(token) > maxApprovalTokenBytes {
		return fmt.Errorf("approval token exceeds %d bytes", maxApprovalTokenBytes)
	}
	for _, r := range token {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return errors.New("approval token contains whitespace or control characters")
		}
	}
	return nil
}
