package main

import (
	"net/http"
	"testing"
)

// A refresh failure is terminal only when it says the session is gone. The
// expensive mistake is the other direction: treating a transient failure as
// terminal disables a credential that still works, and the operator has no way to
// tell that from a real expiry.
func TestOnlyDeadSessionsAreTerminal(t *testing.T) {
	terminal := []struct {
		name   string
		status int
		body   string
	}{
		{"offline session", http.StatusUnauthorized, `{"code":12153,"msg":"Offline user session not found"}`},
		{"explicit marker", http.StatusForbidden, `{"error":"SESSION_EXPIRED"}`},
		{"html error page", http.StatusUnauthorized, `<html><body>Offline user session not found</body></html>`},
	}
	for _, tc := range terminal {
		if !refreshFailureIsTerminal(tc.status, []byte(tc.body)) {
			t.Fatalf("%s: should be terminal (status=%d body=%s)", tc.name, tc.status, tc.body)
		}
	}

	retryable := []struct {
		name   string
		status int
		body   string
	}{
		{"transport failure", 0, ``},
		{"server error", http.StatusInternalServerError, `{"code":500,"msg":"oops"}`},
		{"bad gateway", http.StatusBadGateway, `<html>502 Bad Gateway</html>`},
		{"rate limited", http.StatusTooManyRequests, `{"code":429,"msg":"slow down"}`},
		// A 401 that does not name a dead session is a token problem, which a
		// later refresh can still fix.
		{"plain unauthorized", http.StatusUnauthorized, `{"code":401,"msg":"unauthorized"}`},
		{"empty unauthorized", http.StatusUnauthorized, ``},
		{"not modified", http.StatusNotModified, `{"code":12153}`},
	}
	for _, tc := range retryable {
		if refreshFailureIsTerminal(tc.status, []byte(tc.body)) {
			t.Fatalf("%s: must stay retryable (status=%d body=%s)", tc.name, tc.status, tc.body)
		}
	}
}

// The marker list is policy: asserted so a removal is deliberate, since dropping
// one turns a dead credential back into an endlessly retried one.
func TestSessionDeadMarkersArePresent(t *testing.T) {
	for _, want := range []string{"Offline user session not found", "12153"} {
		found := false
		for _, marker := range sessionDeadMarkers {
			if marker == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("marker %q is missing from sessionDeadMarkers", want)
		}
	}
}
