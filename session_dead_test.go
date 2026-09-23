package main

import (
	"net/http"
	"testing"
)

// A refresh failure is terminal only when it says the session is gone. The
// expensive mistake is the other direction: treating a transient failure as
// terminal disables a credential that still works, and the operator cannot tell
// that from a real expiry.
func TestOnlyDeadSessionsAreTerminal(t *testing.T) {
	terminal := []struct {
		name   string
		status int
		body   string
	}{
		{"offline session phrase", http.StatusUnauthorized, `{"code":12153,"msg":"Offline user session not found"}`},
		{"offline session in html", http.StatusUnauthorized, `<html><body>Offline user session not found</body></html>`},
		{"explicit phrase", http.StatusForbidden, `{"error":"SESSION_EXPIRED"}`},
		{"code as field", http.StatusUnauthorized, `{"code":12153}`},
		{"errorCode as field", http.StatusForbidden, `{"errorCode":12153}`},
	}
	for _, tc := range terminal {
		if !refreshFailureIsTerminal(tc.status, []byte(tc.body)) {
			t.Fatalf("%s: should be terminal (status=%d body=%s)", tc.name, tc.status, tc.body)
		}
	}
}

// The false positive that shipped: "12153" matched as a bare substring, so a
// timestamp or any longer number containing those digits disabled a live account
// and took every call down with it. A five digit run is not a code.
func TestDigitsInsideLargerNumbersAreNotADeadSession(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"timestamp", `{"code":401,"msg":"unauthorized","timestamp":"1789725121539"}`},
		{"request id", `{"code":401,"requestId":"8c1896ed-12153-4c5a"}`},
		{"larger code", `{"code":121530}`},
		{"code as string", `{"code":"12153"}`},
		{"nested code", `{"data":{"code":12153}}`},
		{"byte count", `{"code":401,"msg":"read 12153 bytes"}`},
		{"port", `{"code":401,"msg":"dial 10.0.0.1:12153"}`},
	}
	for _, tc := range cases {
		if refreshFailureIsTerminal(http.StatusUnauthorized, []byte(tc.body)) {
			t.Fatalf("%s: must not disable the credential (body=%s)", tc.name, tc.body)
		}
	}
}

// Everything that is not an authentication answer stays retryable, whatever it
// says, because a later refresh can still succeed.
func TestNonAuthFailuresStayRetryable(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"transport failure", 0, ``},
		{"server error", http.StatusInternalServerError, `{"code":12153}`},
		{"bad gateway", http.StatusBadGateway, `<html>502</html>`},
		{"rate limited", http.StatusTooManyRequests, `{"code":12153}`},
		{"plain unauthorized", http.StatusUnauthorized, `{"code":401}`},
		{"empty unauthorized", http.StatusUnauthorized, ``},
		{"not json", http.StatusUnauthorized, `<html>login</html>`},
	}
	for _, tc := range cases {
		if refreshFailureIsTerminal(tc.status, []byte(tc.body)) {
			t.Fatalf("%s: must stay retryable (status=%d body=%s)", tc.name, tc.status, tc.body)
		}
	}
}

// The phrase list is policy: asserted so a removal is deliberate, since dropping
// one turns a dead credential into an endlessly retried one.
func TestSessionDeadPhrasesArePresent(t *testing.T) {
	for _, want := range []string{"Offline user session not found", "SESSION_EXPIRED"} {
		found := false
		for _, phrase := range sessionDeadPhrases {
			if phrase == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("phrase %q is missing from sessionDeadPhrases", want)
		}
	}
	if sessionDeadCode != 12153 {
		t.Fatalf("sessionDeadCode = %d, want 12153", sessionDeadCode)
	}
}
