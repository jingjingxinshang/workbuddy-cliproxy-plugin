package main

// Multi-account failover for upstream rate limits (HTTP 429 / CodeBuddy code
// 6004 "usage exceeds frequency limit").
//
// The host's own cooldown and retry only apply to executors it runs in-process.
// A c-shared plugin performs its own upstream requests, so a rate limit never
// reaches the host's selector and nothing fails over: the request just fails.
// This keeps a per-account cooldown and retries the same request on another
// enabled account before giving up.
//
// The whole loop shares one chatTimeout budget instead of granting it per
// attempt. A rate-limited account answers immediately, so retrying costs almost
// nothing, but a hanging attempt must not be able to multiply the request's
// worst case by the number of accounts.
//
// Not every rate limit is account-wide. CodeBuddy answers code 6004, with a
// "will reset at ..." wall clock, for one model while the account stays usable
// for every other model -- so that window is recorded against the model, and a
// request for a different model still finds the account healthy.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// defaultCooldown mirrors the host's own 30-minute cooldown and is used when
	// the upstream names no reset time.
	defaultCooldown = 30 * time.Minute
	// httpStatusPaymentRequired is the upstream's "out of credits" status. It is
	// a hard failure: another account would only be billed for the same request.
	httpStatusPaymentRequired = 402
)

type authCooldown struct {
	NextRetryAt time.Time
	Reason      string
	// Model is the model this window applies to. Empty means the whole account:
	// a limit that names no model is throttling everything the account sends.
	Model string
}

var (
	cooldownMu sync.Mutex
	cooldowns  = map[string]authCooldown{}
)

// cooldownKey scopes one window to an account, optionally to a single model.
func cooldownKey(authIndex, model string) string {
	return strings.TrimSpace(authIndex) + "\x00" + strings.ToLower(strings.TrimSpace(model))
}

// markAuthCooldownForModel records that one account reached a rate limit, scoped
// to one model when the upstream named one. A zero or already-past reset time
// falls back to the default window.
func markAuthCooldownForModel(authIndex, model string, resetAt time.Time) {
	authIndex = strings.TrimSpace(authIndex)
	if authIndex == "" {
		return
	}
	if resetAt.IsZero() || !resetAt.After(time.Now()) {
		resetAt = time.Now().Add(defaultCooldown)
	}
	model = strings.ToLower(strings.TrimSpace(model))
	cooldownMu.Lock()
	cooldowns[cooldownKey(authIndex, model)] = authCooldown{NextRetryAt: resetAt, Reason: "429", Model: model}
	cooldownMu.Unlock()
}

// authOnCooldown reports whether one account is inside a window that applies to
// this request: either the whole account, or the model it asks for.
func authOnCooldown(authIndex, model string) bool {
	authIndex = strings.TrimSpace(authIndex)
	if authIndex == "" {
		return false
	}
	model = strings.ToLower(strings.TrimSpace(model))
	cooldownMu.Lock()
	defer cooldownMu.Unlock()
	// The whole-account window first: it covers every model.
	return cooldownActiveLocked(authIndex, "") || cooldownActiveLocked(authIndex, model)
}

// cooldownActiveLocked reports whether one keyed window is still live, dropping
// it when it has expired. The caller holds the mutex.
func cooldownActiveLocked(authIndex, model string) bool {
	key := cooldownKey(authIndex, model)
	entry, present := cooldowns[key]
	if !present {
		return false
	}
	if time.Now().Before(entry.NextRetryAt) {
		return true
	}
	delete(cooldowns, key)
	return false
}

// clearAuthCooldown forgets every window for one account after it answers
// normally, model-scoped ones included.
func clearAuthCooldown(authIndex string) {
	authIndex = strings.TrimSpace(authIndex)
	if authIndex == "" {
		return
	}
	prefix := authIndex + "\x00"
	cooldownMu.Lock()
	for key := range cooldowns {
		if strings.HasPrefix(key, prefix) {
			delete(cooldowns, key)
		}
	}
	cooldownMu.Unlock()
}

// rateLimitResetPattern matches the reset instant CodeBuddy embeds in a 429
// body, for example
// "usage exceeds frequency limit, please try again later, reset at 2026-09-06 06:01:58 UTC+8".
// Only UTC±H offsets are understood; anything else falls back to the default
// window rather than guessing.
var rateLimitResetPattern = regexp.MustCompile(`reset\s+at\s+(\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2})\s*(UTC(?:\+|-)\d{1,2})?`)

// parseRateLimitReset extracts the reset instant from a 429 body, or the zero
// time when there is nothing parseable.
func parseRateLimitReset(body string) time.Time {
	groups := rateLimitResetPattern.FindStringSubmatch(body)
	if len(groups) < 2 {
		return time.Time{}
	}
	// The upstream reports UTC+8 when it names no offset.
	location := time.FixedZone("UTC+8", 8*60*60)
	if len(groups) > 2 && groups[2] != "" {
		offset := 0
		if hours, errParse := strconv.Atoi(strings.TrimPrefix(strings.TrimPrefix(groups[2], "UTC+"), "UTC-")); errParse == nil {
			if strings.HasPrefix(groups[2], "UTC-") {
				offset = -hours
			} else {
				offset = hours
			}
		}
		location = time.FixedZone(groups[2], offset*60*60)
	}
	reset, errParse := time.ParseInLocation("2006-01-02 15:04:05", groups[1], location)
	if errParse != nil {
		return time.Time{}
	}
	return reset
}

// hardCreditMarkers are the upstream wordings that mean the account is out of
// credits rather than throttled. Case-insensitive substrings; the Chinese forms
// are also scanned verbatim because lowercasing them is not meaningful.
var hardCreditMarkers = []string{
	"insufficient credit",
	"insufficient credits",
	"no credit",
	"no credits",
	"credit exhausted",
	"credits exhausted",
	"out of credit",
	"out of credits",
	"credit not enough",
	"not enough credit",
	"quota exceeded",
	"quota exhaust",
	"payment required",
	"积分不足",
	"额度不足",
	"余额不足",
	"积分用完",
	"额度用尽",
	"没有积分",
}

// isHardCreditError reports an out-of-credits failure. These must never fail
// over: the same request would be charged to a second account and still fail.
func isHardCreditError(status int, body string) bool {
	if status == httpStatusPaymentRequired {
		return true
	}
	lower := strings.ToLower(body)
	for _, marker := range hardCreditMarkers {
		if strings.Contains(lower, strings.ToLower(marker)) || strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

// isRateLimitResponse reports a soft rate limit, which is the only failure worth
// retrying on another account.
func isRateLimitResponse(status int, body string) bool {
	if isHardCreditError(status, body) {
		return false
	}
	if status == http.StatusTooManyRequests {
		return true
	}
	lower := strings.ToLower(body)
	return strings.Contains(lower, "6004") ||
		strings.Contains(lower, "frequency limit") ||
		strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "too many requests")
}

// code6004Marker is the upstream business code for a per-model usage limit:
// "usage exceeds frequency limit" for one model, with a reset wall clock. The
// account itself stays usable for other models.
const code6004Marker = "6004"

// isModelScopedRateLimit reports whether a rate limit applies to one model
// rather than to the account as a whole.
func isModelScopedRateLimit(body string) bool {
	return strings.Contains(body, code6004Marker)
}

// errEmptyStream marks an upstream that answered 2xx and produced nothing. It is
// an upstream or request problem, never an account problem, so the failover loop
// stops: replaying it elsewhere would bill a second account for the same failed
// request.
var errEmptyStream = errors.New("empty_stream: upstream returned no payload")

type failoverCandidate struct {
	AuthIndex string
	Auth      workbuddyAuth
}

// authListSource and authCredentialSource are the two host reads the failover
// needs. They are variables so a test can drive the loop without a host.
var (
	authListSource       = workbuddyAccounts
	authCredentialSource = credentialJSONForAuthIndex
)

// chatCandidates lists the accounts one request may be retried on: the routed
// account first, then every other enabled WorkBuddy credential that is not on
// cooldown for this model.
//
// When the host credential list is unavailable there is nothing to fail over to,
// and trying only the routed account is the honest answer rather than inventing
// a retry.
func chatCandidates(routedAuthIndex string, routedAuth workbuddyAuth, model string) []failoverCandidate {
	candidates := []failoverCandidate{{AuthIndex: strings.TrimSpace(routedAuthIndex), Auth: routedAuth}}
	entries, errList := authListSource()
	if errList != nil {
		return candidates
	}
	for _, entry := range entries {
		index := strings.TrimSpace(entry.AuthIndex)
		if index == "" || index == strings.TrimSpace(routedAuthIndex) {
			continue
		}
		if entry.Disabled || strings.EqualFold(strings.TrimSpace(entry.Status), "disabled") {
			continue
		}
		if authOnCooldown(index, model) {
			continue
		}
		credential := authCredentialSource(index)
		if len(credential) == 0 {
			continue
		}
		var auth workbuddyAuth
		if json.Unmarshal(credential, &auth) != nil || auth.AccessToken == "" {
			continue
		}
		candidates = append(candidates, failoverCandidate{AuthIndex: index, Auth: auth})
	}
	return candidates
}

// chatAttempt performs one chat-completions round trip on one account.
func chatAttempt(auth workbuddyAuth, payload []byte, stream bool, budget time.Duration) (int, []byte, error) {
	profile := profiles[regionOf(auth)]
	headers := chatHeaders(auth, profile)
	return upstreamWithin(budget, "POST", profile.baseURL+"/v2/chat/completions", profile.origin, authUserAgent(auth, profile), headers, payload, stream)
}

// executeChat runs one request, retrying it on other accounts while the failure
// is a rate limit.
//
// A failure that is not a rate limit stops the loop immediately: a transport
// error or a rejected request is not the account's fault, and replaying it
// elsewhere only risks a second charge.
func executeChat(routedAuthIndex string, routedAuth workbuddyAuth, model string, payload []byte, stream bool) (int, []byte, error) {
	deadline := time.Now().Add(chatTimeout)
	lastStatus, lastBody := 0, []byte(nil)
	lastErr := error(nil)
	routedIndex := strings.TrimSpace(routedAuthIndex)
	attempted := false
	for _, candidate := range chatCandidates(routedIndex, routedAuth, model) {
		budget := time.Until(deadline)
		if budget <= 0 {
			break
		}
		attempted = true
		status, body, errCall := chatAttempt(candidate.Auth, payload, stream, budget)
		if errCall == nil && status < 400 {
			if len(body) == 0 {
				return status, body, errEmptyStream
			}
			clearAuthCooldown(candidate.AuthIndex)
			return status, body, nil
		}
		lastStatus, lastBody, lastErr = status, body, errCall
		if !isRateLimitResponse(status, string(body)) {
			return status, body, errCall
		}
		// 6004 is a model-level limit, so only that model is parked; the account
		// still serves every other model. Anything else that reads as a rate limit
		// parks the account.
		reset := parseRateLimitReset(string(body))
		if isModelScopedRateLimit(string(body)) {
			markAuthCooldownForModel(candidate.AuthIndex, model, reset)
		} else {
			markAuthCooldownForModel(candidate.AuthIndex, "", reset)
		}
		if candidate.AuthIndex == routedIndex {
			// The routed account is the first candidate, so everything after it is
			// a fallback. Saying so keeps the log honest about what happened.
			if isModelScopedRateLimit(string(body)) {
				warn("account %s is rate limited for model %s; retrying the request on another account", candidate.AuthIndex, model)
			} else {
				warn("account %s is rate limited; retrying the request on another account", candidate.AuthIndex)
			}
		}
	}
	if lastErr != nil {
		return lastStatus, lastBody, lastErr
	}
	if !attempted {
		return 0, nil, errors.New("no attemptable WorkBuddy account")
	}
	return lastStatus, lastBody, fmt.Errorf("every eligible WorkBuddy account is rate limited (HTTP %d)", lastStatus)
}
