package daemon

import (
	"errors"
	"fmt"
	"testing"

	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

func TestCredentialRejectedReadsTheTypeFirstAndTheTextSecond(t *testing.T) {
	typed := fmt.Errorf("jira GET /myself: %w", &target.CredentialRejectedError{Status: 401, Err: errors.New("expired")})
	if !CredentialRejected(typed) {
		t.Fatal("a plugin that says so is believed")
	}
	for _, text := range []string{
		"gitlab GET /user: HTTP 401: {\"message\":\"401 Unauthorized\"}",
		"github: 401 Unauthorized",
		"zammad: status 401",
	} {
		if !CredentialRejected(errors.New(text)) {
			t.Errorf("%q is a 401 in any plugin's words", text)
		}
	}
	for _, text := range []string{
		"gitlab POST /merge_requests: HTTP 403: forbidden",
		"issue 4011 not found",
		"HTTP 404",
	} {
		if CredentialRejected(errors.New(text)) {
			t.Errorf("%q is not the credential", text)
		}
	}
	if CredentialRejected(nil) {
		t.Fatal("no error, no rejection")
	}
}
