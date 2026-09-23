package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const (
	// WorkBuddy's meter API answers this code once today's bonus is already
	// taken. It is the only authoritative statement of that fact, and it is how
	// a repeat claim is reported (with HTTP 400, not 200).
	checkinAlreadyClaimedCode = 10001

	checkinClaimed   = "claimed"
	checkinUnclaimed = "unclaimed"
	checkinUnknown   = "unknown"

	// checkinPageTimeout bounds one meter call made while answering the page.
	checkinPageTimeout = 15 * time.Second
	// checkinRefreshTimeout bounds the claim made from the token-refresh hook,
	// where the host may be waiting on the plugin to serve a request. The claim
	// is idempotent and best effort, so a budget that is too short costs a
	// retry on the next refresh and nothing else.
	checkinRefreshTimeout = 3 * time.Second
)

// checkinResult is one account's daily-bonus verdict plus the activity detail
// the page shows next to it.
type checkinResult struct {
	State          string `json:"state"`
	Credit         int64  `json:"credit,omitempty"`
	FreshlyClaimed bool   `json:"freshly_claimed,omitempty"`
	StreakDays     int64  `json:"streak_days,omitempty"`
	TodayCredit    int64  `json:"today_credit,omitempty"`
	TotalCredits   int64  `json:"total_credits,omitempty"`
	ActivityName   string `json:"activity_name,omitempty"`
	WeekProgress   []bool `json:"week_progress,omitempty"`
	Error          string `json:"error,omitempty"`
}

// checkinStatusData is the `data` block of /billing/meter/checkin-status.
type checkinStatusData struct {
	Active         bool   `json:"active"`
	TodayCheckedIn bool   `json:"today_checked_in"`
	StreakDays     int64  `json:"streak_days"`
	TodayCredit    int64  `json:"today_credit"`
	TotalCredits   int64  `json:"total_credits"`
	ActivityName   string `json:"activity_name"`
	WeekProgress   []bool `json:"week_progress"`
}

// meterEnvelope is the shape the meter endpoints used here answer with.
type meterEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// checkinStatusVerdict turns a checkin-status payload into a verdict.
//
// `today_checked_in` describes a seasonal check-in ACTIVITY, not the daily
// bonus. With no activity running the gateway zeroes the whole block — that
// flag included — so reading it as "today's bonus is unclaimed" reported "not
// claimed yet" for accounts that had already claimed. An inactive block is
// therefore reported as unknown: there is no read-only source for the daily
// bonus, and only the claim endpoint can settle the question.
func checkinStatusVerdict(code int, data checkinStatusData) checkinResult {
	if code == checkinAlreadyClaimedCode {
		return checkinResult{State: checkinClaimed}
	}
	if code != 0 {
		return checkinResult{State: checkinUnknown, Error: fmt.Sprintf("code=%d", code)}
	}
	if !data.Active {
		return checkinResult{State: checkinUnknown}
	}
	result := checkinResult{
		State:        checkinUnclaimed,
		StreakDays:   data.StreakDays,
		TodayCredit:  data.TodayCredit,
		TotalCredits: data.TotalCredits,
		ActivityName: data.ActivityName,
		WeekProgress: data.WeekProgress,
	}
	if data.TodayCheckedIn {
		result.State = checkinClaimed
	}
	return result
}

// checkinClaimVerdict turns a daily-checkin payload into a verdict.
func checkinClaimVerdict(code int, credit int64) checkinResult {
	switch code {
	case 0:
		return checkinResult{State: checkinClaimed, FreshlyClaimed: true, Credit: credit}
	case checkinAlreadyClaimedCode:
		return checkinResult{State: checkinClaimed}
	default:
		return checkinResult{State: checkinUnknown, Error: fmt.Sprintf("code=%d", code)}
	}
}

// meterCall posts an empty JSON body to one of the region's meter endpoints.
//
// Check-in uses the same browser-shaped request as the quota read: the gateway
// rejects the CLI user agent on /billing/*, and the account's region decides
// which cluster answers.
func meterCall(auth workbuddyAuth, path string, budget time.Duration) (int, []byte, error) {
	profile := profiles[regionOf(auth)]
	return upstreamWithin(budget, "POST", profile.baseURL+path, profile.origin, billingUserAgent, billingHeaders(auth), []byte("{}"), false)
}

// fetchCheckinStatus reads one account's check-in state. It never claims, so it
// is safe to call while rendering.
func fetchCheckinStatus(auth workbuddyAuth, budget time.Duration) checkinResult {
	status, body, errCall := meterCall(auth, "/billing/meter/checkin-status", budget)
	if errCall != nil {
		return checkinResult{State: checkinUnknown, Error: truncate(errCall.Error(), 120)}
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return checkinResult{State: checkinUnknown, Error: "AUTH_REQUIRED"}
	}
	var env meterEnvelope
	if errUnmarshal := json.Unmarshal(body, &env); errUnmarshal != nil {
		return checkinResult{State: checkinUnknown, Error: "NON_JSON_RESPONSE"}
	}
	var data checkinStatusData
	_ = json.Unmarshal(env.Data, &data)
	return checkinStatusVerdict(env.Code, data)
}

// claimCheckin claims today's bonus.
//
// Claiming is idempotent: an account that already took the bonus answers
// checkinAlreadyClaimedCode, which is reported as claimed rather than as a
// failure.
func claimCheckin(auth workbuddyAuth, budget time.Duration) checkinResult {
	status, body, errCall := meterCall(auth, "/billing/meter/daily-checkin", budget)
	if errCall != nil {
		return checkinResult{State: checkinUnknown, Error: truncate(errCall.Error(), 120)}
	}
	// A repeat claim is HTTP 400 with code 10001, so the body decides.
	var env meterEnvelope
	if errUnmarshal := json.Unmarshal(body, &env); errUnmarshal != nil {
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			return checkinResult{State: checkinUnknown, Error: "AUTH_REQUIRED"}
		}
		return checkinResult{State: checkinUnknown, Error: "NON_JSON_RESPONSE"}
	}
	var data struct {
		Credit int64 `json:"credit"`
	}
	_ = json.Unmarshal(env.Data, &data)
	return checkinClaimVerdict(env.Code, data.Credit)
}

// ensureCheckin claims unless the gateway confirms the bonus is already taken.
//
// Anything short of "claimed" falls through to the claim endpoint, including an
// inconclusive status read — which is the normal case, since no read-only source
// exists. Treating "unknown" as "nothing to do" would mean never claiming.
func ensureCheckin(auth workbuddyAuth, budget time.Duration) checkinResult {
	status := fetchCheckinStatus(auth, budget)
	if status.State == checkinClaimed {
		return status
	}
	claim := claimCheckin(auth, budget)
	if claim.State != checkinClaimed {
		return claim
	}
	// The claim payload only carries the credits it just granted, so the
	// activity detail comes from the read whenever there was one.
	claim.StreakDays = status.StreakDays
	claim.TotalCredits = status.TotalCredits
	claim.ActivityName = status.ActivityName
	claim.WeekProgress = status.WeekProgress
	if claim.TodayCredit == 0 {
		claim.TodayCredit = status.TodayCredit
	}
	return claim
}

// maybeCheckin claims the daily bonus at most once per local day.
//
// The host refreshes a credential shortly before its access token expires, and
// that is the only hook this plugin has which fires without someone opening its
// page. It is best effort: claiming is idempotent, and a credential that cannot
// claim is still usable, so a refresh must never fail because of this.
//
// It claims directly rather than probing first. The status read cannot settle
// the question -- there is no read-only source for the daily bonus -- so it only
// ever put a second round trip in front of the claim, and this hook runs while
// the host may be waiting on the plugin to serve a request. A repeat claim
// answers checkinAlreadyClaimedCode, which the claim verdict already reports as
// claimed, so the outcome is unchanged for one call instead of up to two.
func maybeCheckin(auth *workbuddyAuth) {
	if auth == nil || auth.AccessToken == "" {
		return
	}
	if !checkinOnRefreshEnabled() {
		return
	}
	day := time.Now().Format(time.DateOnly)
	if auth.LastCheckinDay == day {
		return
	}
	if claimCheckin(*auth, checkinRefreshTimeout).State != checkinClaimed {
		return
	}
	auth.LastCheckinDay = day
}

// accountView is one stored WorkBuddy credential as the accounts page needs it.
type accountView struct {
	AuthIndex    string         `json:"auth_index"`
	Name         string         `json:"name,omitempty"`
	Label        string         `json:"label,omitempty"`
	Nickname     string         `json:"nickname,omitempty"`
	UID          string         `json:"uid,omitempty"`
	EnterpriseID string         `json:"enterprise_id,omitempty"`
	Region       string         `json:"region,omitempty"`
	ExpiresAt    int64          `json:"expires_at,omitempty"`
	TokenState   string         `json:"token_state,omitempty"`
	Checkin      *checkinResult `json:"checkin,omitempty"`
	Error        string         `json:"error,omitempty"`
}

// tokenState tells the page whether the stored access token is still usable, so
// an upstream failure can be explained without another round trip.
func tokenState(auth workbuddyAuth) string {
	if auth.AccessToken == "" {
		return "missing"
	}
	if auth.ExpiresAt > 0 && auth.ExpiresAt <= time.Now().UnixMilli() {
		return "expired"
	}
	return "active"
}

// workbuddyAccounts lists the stored WorkBuddy credentials the host knows about.
func workbuddyAccounts() ([]hostAuthFileEntry, error) {
	result, errCall := hostCall(pluginabi.MethodHostAuthList, map[string]any{})
	if errCall != nil {
		return nil, fmt.Errorf("cannot list credentials: %w", errCall)
	}
	var payload struct {
		Files []hostAuthFileEntry `json:"files"`
	}
	if errUnmarshal := json.Unmarshal(result, &payload); errUnmarshal != nil {
		return nil, fmt.Errorf("credential list is not readable: %w", errUnmarshal)
	}
	entries := make([]hostAuthFileEntry, 0, len(payload.Files))
	for _, file := range payload.Files {
		if !strings.EqualFold(strings.TrimSpace(file.Provider), providerID) {
			continue
		}
		if strings.TrimSpace(file.AuthIndex) == "" {
			continue
		}
		entries = append(entries, file)
	}
	return entries, nil
}

// credentialJSONForAuthIndex reads one stored credential from the host.
func credentialJSONForAuthIndex(authIndex string) []byte {
	authIndex = strings.TrimSpace(authIndex)
	if authIndex == "" {
		return nil
	}
	result, errCall := hostCall(pluginabi.MethodHostAuthGet, map[string]string{"auth_index": authIndex})
	if errCall != nil {
		return nil
	}
	var payload struct {
		JSON json.RawMessage `json:"json"`
	}
	if json.Unmarshal(result, &payload) != nil {
		return nil
	}
	return payload.JSON
}

// accountsView answers GET /v0/management/workbuddy/accounts.
//
// CPA's panel renders quota for its built-in providers only, so this page is
// the one place an operator sees every WorkBuddy account at once: the identity
// CPA stored, the token state and the daily-bonus verdict.
func accountsView(raw []byte) pluginapi.ManagementResponse {
	entries, errList := workbuddyAccounts()
	if errList != nil {
		return errorManagementResponse(http.StatusBadGateway, errList.Error())
	}
	wanted := strings.ToLower(strings.TrimSpace(managementQueryValue(raw, "name")))
	accounts := make([]accountView, 0, len(entries))
	auths := make([]workbuddyAuth, 0, len(entries))
	for _, entry := range entries {
		if wanted != "" && strings.ToLower(strings.TrimSpace(entry.Name)) != wanted {
			continue
		}
		account := accountView{
			AuthIndex: strings.TrimSpace(entry.AuthIndex),
			Name:      strings.TrimSpace(entry.Name),
			Label:     strings.TrimSpace(entry.Label),
		}
		var auth workbuddyAuth
		credential := credentialJSONForAuthIndex(account.AuthIndex)
		switch {
		case len(credential) == 0:
			account.Error = "credential unavailable"
		case json.Unmarshal(credential, &auth) != nil:
			account.Error = "stored credential is not readable"
		default:
			account.Nickname = auth.Nickname
			account.UID = auth.UID
			account.EnterpriseID = auth.EnterpriseID
			account.Region = regionOf(auth)
			account.ExpiresAt = auth.ExpiresAt
			account.TokenState = tokenState(auth)
			if account.Label == "" {
				account.Label = auth.Nickname
			}
		}
		accounts = append(accounts, account)
		auths = append(auths, auth)
	}

	// The status reads are upstream HTTP, not host callbacks, so they run
	// concurrently. Sequentially a slow cluster would cost one poll timeout per
	// account and make the page look hung.
	verdicts := make([]checkinResult, len(auths))
	var wait sync.WaitGroup
	for index := range auths {
		if auths[index].AccessToken == "" {
			continue
		}
		wait.Add(1)
		go func(position int) {
			defer wait.Done()
			verdicts[position] = fetchCheckinStatus(auths[position], checkinPageTimeout)
		}(index)
	}
	wait.Wait()
	for index := range accounts {
		if auths[index].AccessToken == "" {
			continue
		}
		verdict := verdicts[index]
		accounts[index].Checkin = &verdict
	}

	body, errMarshal := json.Marshal(map[string]any{"accounts": accounts})
	if errMarshal != nil {
		return errorManagementResponse(http.StatusInternalServerError, "failed to encode accounts")
	}
	return jsonManagementResponse(body)
}

// checkinOutcome is one credential's result in a check-in sweep.
type checkinOutcome struct {
	AuthIndex string `json:"auth_index"`
	Name      string `json:"name,omitempty"`
	Label     string `json:"label,omitempty"`
	checkinResult
}

// checkinRouteResponse answers POST /v0/management/workbuddy/checkin.
//
// Passing no auth_index claims for every WorkBuddy credential, which is what the
// page's manual sweep and its automatic one both use.
func checkinRouteResponse(raw []byte) pluginapi.ManagementResponse {
	entries, errList := workbuddyAccounts()
	if errList != nil {
		return errorManagementResponse(http.StatusBadGateway, errList.Error())
	}
	explicit := strings.TrimSpace(firstNonEmpty(managementQueryValue(raw, "auth_index"), managementQueryValue(raw, "authIndex")))
	wanted := strings.ToLower(strings.TrimSpace(managementQueryValue(raw, "name")))
	outcomes := make([]checkinOutcome, 0, len(entries))
	auths := make([]workbuddyAuth, 0, len(entries))
	for _, entry := range entries {
		if explicit != "" && strings.TrimSpace(entry.AuthIndex) != explicit {
			continue
		}
		if wanted != "" && strings.ToLower(strings.TrimSpace(entry.Name)) != wanted {
			continue
		}
		var auth workbuddyAuth
		if credential := credentialJSONForAuthIndex(entry.AuthIndex); len(credential) > 0 {
			_ = json.Unmarshal(credential, &auth)
		}
		outcome := checkinOutcome{
			AuthIndex: strings.TrimSpace(entry.AuthIndex),
			Name:      strings.TrimSpace(entry.Name),
			Label:     strings.TrimSpace(entry.Label),
		}
		if auth.AccessToken == "" {
			outcome.checkinResult = checkinResult{State: checkinUnknown, Error: "credential unavailable"}
		}
		outcomes = append(outcomes, outcome)
		auths = append(auths, auth)
	}
	if len(outcomes) == 0 {
		return errorManagementResponse(http.StatusNotFound, "no WorkBuddy credential matched the request")
	}

	var wait sync.WaitGroup
	for index := range auths {
		if auths[index].AccessToken == "" {
			continue
		}
		wait.Add(1)
		go func(position int) {
			defer wait.Done()
			result := ensureCheckin(auths[position], checkinPageTimeout)
			outcomes[position].checkinResult = result
		}(index)
	}
	wait.Wait()

	body, errMarshal := json.Marshal(map[string]any{"results": outcomes})
	if errMarshal != nil {
		return errorManagementResponse(http.StatusInternalServerError, "failed to encode check-in results")
	}
	return jsonManagementResponse(body)
}
