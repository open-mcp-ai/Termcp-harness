package controlapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestApprovalAuthorizerRequiresBearerTokenAndNoOrigin(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	authorizer, err := NewApprovalAuthorizer(token)
	if err != nil {
		t.Fatalf("NewApprovalAuthorizer() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/approvals/a/decision", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	if err := authorizer.Authorize(request); err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}

	request.Header.Set("Origin", "http://127.0.0.1:8787")
	if err := authorizer.Authorize(request); !errors.Is(err, ErrApprovalOriginDenied) {
		t.Fatalf("Origin Authorize() error = %v", err)
	}

	request.Header.Del("Origin")
	request.Header.Set("Authorization", "Bearer wrong")
	if err := authorizer.Authorize(request); !errors.Is(err, ErrApprovalAuthInvalid) {
		t.Fatalf("wrong token error = %v", err)
	}
	if err := (*ApprovalAuthorizer)(nil).Authorize(request); !errors.Is(err, ErrApprovalAuthRequired) {
		t.Fatalf("nil authorizer error = %v", err)
	}
}

func TestApprovalAuthorizerRejectsWeakTokens(t *testing.T) {
	for _, token := range []string{
		"short",
		strings.Repeat("a", maxApprovalTokenBytes+1),
		"0123456789abcdef0123456789abcde ",
	} {
		if _, err := NewApprovalAuthorizer(token); err == nil {
			t.Fatalf("NewApprovalAuthorizer(%q) error = nil", token)
		}
	}
}
