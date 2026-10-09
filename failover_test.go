package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// emptySuccessStatus makes the stub answer 2xx with no body at all.
const emptySuccessStatus = http.StatusNoContent

// rateLimitBody is the upstream's wording, including the reset instant.
const rateLimitBody = `{"code":6004,"msg":"usage exceeds frequency limit, please try again later, reset at 2026-10-09 12:00:00 UTC+8"}`

func clearAllCooldowns() {
	cooldownMu.Lock()
	cooldowns = map[string]authCooldown{}
	cooldownMu.Unlock()
}

// stubAccounts makes the failover see an ordered credential list.
func stubAccounts(t *testing.T, entries []hostAuthFileEntry, accounts map[string]workbuddyAuth) {
	t.Helper()
	previousList, previousCredential := authListSource, authCredentialSource
	authListSource = func() ([]hostAuthFileEntry, error) { return entries, nil }
	authCredentialSource = func(authIndex string) []byte {
		auth, present := accounts[strings.TrimSpace(authIndex)]
		if !present {
			return nil
		}
		raw, errMarshal := json.Marshal(auth)
		if errMarshal != nil {
			t.Fatalf("marshal stub credential: %v", errMarshal)
		}
		return raw
	}
	t.Cleanup(func() {
		authListSource, authCredentialSource = previousList, previousCredential
	})
}

// chatStub answers chat completions according to the bearer token, so a test can
// give each account its own upstream behaviour. It records the tokens it saw, in
// order.
func chatStub(t *testing.T, byToken map[string]int) (*httptest.Server, *[]string) {
	t.Helper()
	seen := &[]string{}
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		mu.Lock()
		*seen = append(*seen, token)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		status := byToken[token]
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		switch {
		case status == emptySuccessStatus:
			// 2xx with no payload.
		case status == http.StatusTooManyRequests || status == http.StatusBadRequest:
			_, _ = w.Write([]byte(rateLimitBody))
		case status >= 400:
			// A neutral failure: only the wording decides a rate limit, so a plain
			// 5xx must not carry it.
			_, _ = w.Write([]byte(`{"code":500,"msg":"internal error"}`))
		default:
			_, _ = w.Write([]byte("data: {\"ok\":true}\n\n"))
		}
	}))
	t.Cleanup(server.Close)
	return server, seen
}

// oneAccountList is the credential list for a single fallback account.
func oneAccountList(fallbackIndex string, fallback workbuddyAuth) ([]hostAuthFileEntry, map[string]workbuddyAuth) {
	return []hostAuthFileEntry{
		{AuthIndex: "auth-1", Provider: providerID},
		{AuthIndex: fallbackIndex, Provider: providerID},
	}, map[string]workbuddyAuth{fallbackIndex: fallback}
}

// A rate-limited account must not end the request: the same request is retried
// on another enabled account, and the throttled one is parked so the next
// request does not walk into it again.
func TestFailoverRetriesARateLimitedAccountOnAnother(t *testing.T) {
	clearAllCooldowns()
	server, seen := chatStub(t, map[string]int{"t1": http.StatusTooManyRequests})
	stubIntlRegion(t, server.URL)
	entries, accounts := oneAccountList("auth-2", workbuddyAuth{AccessToken: "t2", Region: intlRegion})
	stubAccounts(t, entries, accounts)

	routed := workbuddyAuth{AccessToken: "t1", Region: intlRegion}
	status, body, errCall := executeChat("auth-1", routed, []byte(`{"messages":[]}`), false)

	if errCall != nil {
		t.Fatalf("executeChat returned %v, want a fallback success", errCall)
	}
	if status != http.StatusOK || len(body) == 0 {
		t.Fatalf("status = %d body = %q, want a real answer", status, body)
	}
	if len(*seen) != 2 || (*seen)[0] != "t1" || (*seen)[1] != "t2" {
		t.Fatalf("attempts = %v, want the routed account then the fallback", *seen)
	}
	if !authOnCooldown("auth-1") {
		t.Fatal("the rate-limited account was not parked, so the next request repeats the 429")
	}
	if authOnCooldown("auth-2") {
		t.Fatal("the account that answered was left on cooldown")
	}
}

// An out-of-credits failure must never fail over: the same request would be
// charged to a second account and still fail.
func TestFailoverDoesNotRetryAHardCreditError(t *testing.T) {
	clearAllCooldowns()
	server, seen := chatStub(t, map[string]int{"t1": httpStatusPaymentRequired})
	stubIntlRegion(t, server.URL)
	entries, accounts := oneAccountList("auth-2", workbuddyAuth{AccessToken: "t2", Region: intlRegion})
	stubAccounts(t, entries, accounts)

	routed := workbuddyAuth{AccessToken: "t1", Region: intlRegion}
	status, _, errCall := executeChat("auth-1", routed, []byte(`{}`), false)

	if errCall != nil {
		t.Fatalf("executeChat returned %v, want the upstream status passed through", errCall)
	}
	if status != httpStatusPaymentRequired {
		t.Fatalf("status = %d, want %d", status, httpStatusPaymentRequired)
	}
	if len(*seen) != 1 {
		t.Fatalf("attempts = %v, want exactly one: a credit error is not the account's fault to route around", *seen)
	}
	if authOnCooldown("auth-1") {
		t.Fatal("an out-of-credits account was parked as if it were throttled")
	}
}

// Any other failure is not a rate limit, so the loop stops instead of replaying
// the request somewhere else.
func TestFailoverDoesNotRetryANonRateLimitFailure(t *testing.T) {
	clearAllCooldowns()
	server, seen := chatStub(t, map[string]int{"t1": http.StatusInternalServerError})
	stubIntlRegion(t, server.URL)
	entries, accounts := oneAccountList("auth-2", workbuddyAuth{AccessToken: "t2", Region: intlRegion})
	stubAccounts(t, entries, accounts)

	routed := workbuddyAuth{AccessToken: "t1", Region: intlRegion}
	status, _, errCall := executeChat("auth-1", routed, []byte(`{}`), false)

	if errCall != nil {
		t.Fatalf("executeChat returned %v", errCall)
	}
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status)
	}
	if len(*seen) != 1 {
		t.Fatalf("attempts = %v, want exactly one", *seen)
	}
}

// A 2xx that carries nothing is an upstream or request problem, never an account
// problem: replaying it would bill a second account for the same failed request.
func TestFailoverDoesNotRetryAnEmptySuccess(t *testing.T) {
	clearAllCooldowns()
	server, seen := chatStub(t, map[string]int{"t1": emptySuccessStatus})
	stubIntlRegion(t, server.URL)
	entries, accounts := oneAccountList("auth-2", workbuddyAuth{AccessToken: "t2", Region: intlRegion})
	stubAccounts(t, entries, accounts)

	routed := workbuddyAuth{AccessToken: "t1", Region: intlRegion}
	_, _, errCall := executeChat("auth-1", routed, []byte(`{}`), false)

	if errCall == nil {
		t.Fatal("an empty 2xx was accepted as a result")
	}
	if len(*seen) != 1 {
		t.Fatalf("attempts = %v, want exactly one and no double billing", *seen)
	}
}

// When every eligible account is throttled the request fails, and each account
// that was throttled is parked.
func TestFailoverParksEveryThrottledAccount(t *testing.T) {
	clearAllCooldowns()
	server, seen := chatStub(t, map[string]int{"t1": http.StatusTooManyRequests, "t2": http.StatusTooManyRequests})
	stubIntlRegion(t, server.URL)
	entries, accounts := oneAccountList("auth-2", workbuddyAuth{AccessToken: "t2", Region: intlRegion})
	stubAccounts(t, entries, accounts)

	routed := workbuddyAuth{AccessToken: "t1", Region: intlRegion}
	_, _, errCall := executeChat("auth-1", routed, []byte(`{}`), false)

	if errCall == nil {
		t.Fatal("expected a failure when every account is rate limited")
	}
	if len(*seen) != 2 {
		t.Fatalf("attempts = %v, want both accounts tried", *seen)
	}
	if !authOnCooldown("auth-1") || !authOnCooldown("auth-2") {
		t.Fatal("a throttled account was left usable")
	}
}

// An account already parked must not be tried again until its window expires.
func TestFailoverSkipsAnAccountOnCooldown(t *testing.T) {
	clearAllCooldowns()
	server, seen := chatStub(t, map[string]int{"t1": http.StatusTooManyRequests})
	stubIntlRegion(t, server.URL)
	entries, accounts := oneAccountList("auth-2", workbuddyAuth{AccessToken: "t2", Region: intlRegion})
	stubAccounts(t, entries, accounts)

	markAuthCooldown("auth-2", time.Now().Add(time.Hour))

	routed := workbuddyAuth{AccessToken: "t1", Region: intlRegion}
	if _, _, errCall := executeChat("auth-1", routed, []byte(`{}`), false); errCall == nil {
		t.Fatal("expected a failure: the only fallback is parked")
	}
	if len(*seen) != 1 || (*seen)[0] != "t1" {
		t.Fatalf("attempts = %v, want only the routed account", *seen)
	}
}

// A disabled account must not be used as a fallback.
func TestFailoverSkipsDisabledAccounts(t *testing.T) {
	clearAllCooldowns()
	server, seen := chatStub(t, map[string]int{"t1": http.StatusTooManyRequests})
	stubIntlRegion(t, server.URL)
	entries := []hostAuthFileEntry{
		{AuthIndex: "auth-1", Provider: providerID},
		{AuthIndex: "auth-2", Provider: providerID, Disabled: true},
	}
	stubAccounts(t, entries, map[string]workbuddyAuth{"auth-2": {AccessToken: "t2", Region: intlRegion}})

	routed := workbuddyAuth{AccessToken: "t1", Region: intlRegion}
	if _, _, errCall := executeChat("auth-1", routed, []byte(`{}`), false); errCall == nil {
		t.Fatal("expected a failure: the only fallback is disabled")
	}
	if len(*seen) != 1 {
		t.Fatalf("attempts = %v, want only the routed account", *seen)
	}
}

func TestParseRateLimitReset(t *testing.T) {
	// The upstream's own wording, with the offset it normally reports.
	reset := parseRateLimitReset("reset at 2026-10-09 12:00:00 UTC+8")
	if reset.IsZero() {
		t.Fatal("no reset parsed from the upstream wording")
	}
	if _, offset := reset.Zone(); offset != 8*60*60 {
		t.Fatalf("offset = %d, want UTC+8", offset)
	}
	if reset.UTC().Unix() != time.Date(2026, 10, 9, 4, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("reset = %s, want 2026-10-09 04:00 UTC", reset.UTC())
	}

	// A named negative offset is honoured.
	if got := parseRateLimitReset("reset at 2026-10-09 12:00:00 UTC-5"); got.UTC().Unix() != time.Date(2026, 10, 9, 17, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("UTC-5 reset = %s, want 17:00 UTC", got.UTC())
	}

	// No offset means the default the upstream uses.
	if got := parseRateLimitReset("reset at 2026-10-09 12:00:00"); got.UTC().Unix() != time.Date(2026, 10, 9, 4, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("offset-less reset = %s, want 04:00 UTC", got.UTC())
	}

	// Nothing parseable leaves the caller on the default window.
	for _, body := range []string{"", "rate limit exceeded", "reset at tomorrow"} {
		if got := parseRateLimitReset(body); !got.IsZero() {
			t.Errorf("parseRateLimitReset(%q) = %s, want the zero time", body, got)
		}
	}
}

func TestIsRateLimitResponse(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"plain 429", http.StatusTooManyRequests, "", true},
		{"code 6004", http.StatusBadRequest, `{"code":6004,"msg":"usage exceeds frequency limit"}`, true},
		{"upstream wording", http.StatusBadRequest, "usage exceeds frequency limit", true},
		{"plain wording", http.StatusOK, "rate limit exceeded", true},
		{"too many requests", http.StatusBadRequest, "Too Many Requests", true},
		{"ordinary failure", http.StatusInternalServerError, "", false},
		{"ordinary 400", http.StatusBadRequest, "invalid model", false},
		{"payment required", httpStatusPaymentRequired, "", false},
		{"out of credits", http.StatusBadRequest, "insufficient credit", false},
		{"out of credits despite 429", http.StatusTooManyRequests, "insufficient credits", false},
		{"quota exhausted", http.StatusForbidden, "quota exceeded", false},
	}
	for _, tc := range cases {
		if got := isRateLimitResponse(tc.status, tc.body); got != tc.want {
			t.Errorf("%s: isRateLimitResponse(%d, %q) = %v, want %v", tc.name, tc.status, tc.body, got, tc.want)
		}
	}
}
