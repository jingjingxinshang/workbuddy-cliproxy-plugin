package main

// International daily credit reward.
//
// An international account has no daily check-in. The domestic bonus action is a
// desktop-application deep link, so the check-in status endpoint answers
// inactive forever and such an account looks like it has no daily income at all.
//
// The international realm does serve the growth-centre family, and the income
// path is the same activity beacon the official client sends on every chat: one
// chat_request_send event to POST /v2/report lights the day, and a day with a
// non-zero score in GET /activity/growth/heatmap is paid out as a 30-credit
// Bonus Pack on the following China-time day.
//
// Two properties make running it several times a day safe:
//
//   - "today" is the heatmap's own last cell, never the local clock, so there is
//     no timezone arithmetic and no day-boundary surprise.
//   - the report is never retried. The event is idempotent per day upstream, but
//     a blind resend double-counts it (score 2 -> 4), so a failed send waits for
//     the next scheduled slot instead.
//
// Verified against zidanefaqih/codebuddy-intl-cpa, which runs this in
// production: six of seven accounts lit by this report alone were paid out the
// following morning.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const (
	// growthHeatmapPath is the read-only "is this day lit" probe. The growth
	// family is served off the realm host without the /v2 prefix.
	growthHeatmapPath = "/activity/growth/heatmap"
	// growthReportPath is the activity beacon, which does live under /v2.
	growthReportPath = "/v2/report"

	// The beacon mirrors what the official client sends. These model fields take
	// part in the upstream "experience this model" judgement, so a fixed default
	// is used rather than whatever the operator happens to be chatting with.
	growthReportModelID   = "deepseek-v4-flash"
	growthReportModelName = "DeepSeek V4 Flash"
	growthReportInputLen  = 12

	// dailyBonusTick is how often the scheduler wakes. A slot is an hour wide and
	// a run is idempotent per account per day, so the tick only decides how soon
	// after the hour starts the beacon goes out.
	dailyBonusTick = 15 * time.Minute

	// dailyBonusWorkers bounds how many accounts are processed at once.
	dailyBonusWorkers = 4
)

// dailyBonusHours is the local-time schedule. Four slots: a run skips days that
// are already lit, so the extra slots cost one heatmap read per account and
// cover a slot that fails -- the network not being up yet right after boot, for
// instance.
var dailyBonusHours = []int{8, 12, 16, 20}

var (
	dailyBonusMu   sync.RWMutex
	lastDailyBonus *dailyBonusSummary
)

type dailyBonusSummary struct {
	When    time.Time       `json:"when"`
	Results []dailyBonusRow `json:"results"`
}

type dailyBonusRow struct {
	AuthIndex string   `json:"auth_index"`
	Nickname  string   `json:"nickname,omitempty"`
	Region    string   `json:"region,omitempty"`
	Day       string   `json:"day,omitempty"`
	Score     int      `json:"score,omitempty"`
	Lit       bool     `json:"lit"`
	LitDates  []string `json:"lit_dates,omitempty"`
	Status    string   `json:"status"` // reported | already-lit | skipped | failed
	Detail    string   `json:"detail,omitempty"`
}

// dailyBonusEnabled reports whether the scheduler may run the reward engine.
//
// An absent setting means enabled: this engine only ever touches international
// credentials, and an operator who installed the plugin for an international
// account wants the reward without having to ask for it a second time.
func dailyBonusEnabled() bool {
	configMu.RLock()
	enabled := pluginCfg.DailyBonus
	configMu.RUnlock()
	return enabled == nil || *enabled
}

// growthChatEvent is one entry of the POST /v2/report array. The field names
// match the upstream JSON exactly, and the slices are typed []any rather than
// omitted so the zero values are still present in the payload.
type growthChatEvent struct {
	EventCode            string `json:"eventCode"`
	Timestamp            int64  `json:"timestamp"`
	Mode                 string `json:"mode"`
	ConversationID       string `json:"conversationId"`
	RequestID            string `json:"requestId"`
	InputLength          int    `json:"inputLength"`
	RequestModelID       string `json:"requestModelId"`
	RequestModelName     string `json:"requestModelName"`
	MentionContexts      []any  `json:"mentionContexts"`
	KnowledgeID          []any  `json:"knowledgeId"`
	KnowledgeName        []any  `json:"knowledgeName"`
	PresentAt            int64  `json:"presentAt"`
	RootRequestID        string `json:"rootRequestId"`
	ParentConversationID string `json:"parentConversationId"`
	AgentName            string `json:"agentName"`
	AgentType            string `json:"agentType"`
	UserID               string `json:"userId"`
}

type growthHeatmapCell struct {
	Date  string `json:"date"`
	Score int    `json:"score"`
}

type growthHeatmap struct {
	Cells []growthHeatmapCell `json:"cells"`
}

// growthToken mints a UUID-shaped conversation id. The event carries
// caller-generated ids, so each report looks like a fresh conversation rather
// than a replay of an earlier one.
func growthToken() string {
	raw := make([]byte, 16)
	if _, errRead := rand.Read(raw); errRead != nil {
		// A failure here is not recoverable and not worth failing the run over:
		// a time-derived id is unique enough for an idempotency token.
		return fmt.Sprintf("wb-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(raw[0:4]),
		hex.EncodeToString(raw[4:6]),
		hex.EncodeToString(raw[6:8]),
		hex.EncodeToString(raw[8:10]),
		hex.EncodeToString(raw[10:16]))
}

// growthCall performs one growth-centre request on the credential's realm host.
func growthCall(auth workbuddyAuth, method, path string, body []byte) (json.RawMessage, error) {
	profile := profiles[regionOf(auth)]
	status, raw, errCall := upstreamWithin(checkinPageTimeout, method, profile.baseURL+path, profile.origin, billingUserAgent, billingHeaders(auth), body, false)
	if errCall != nil {
		return nil, errCall
	}
	if status >= 500 {
		return nil, fmt.Errorf("http %d from %s", status, path)
	}
	var env meterEnvelope
	if errUnmarshal := json.Unmarshal(raw, &env); errUnmarshal != nil {
		// A non-JSON body here usually means the gateway answered an HTML error
		// page (dead session, or the wrong realm host), so report that instead of
		// a bare parse failure.
		return nil, fmt.Errorf("parse failed: %w", errUnmarshal)
	}
	if env.Code != 0 {
		return nil, fmt.Errorf("code=%d msg=%s", env.Code, truncate(env.Msg, 120))
	}
	return env.Data, nil
}

// fetchGrowthToday returns the endpoint's own current day plus every lit date.
func fetchGrowthToday(auth workbuddyAuth) (growthHeatmapCell, []string, error) {
	data, errFetch := growthCall(auth, http.MethodGet, growthHeatmapPath, nil)
	if errFetch != nil {
		return growthHeatmapCell{}, nil, errFetch
	}
	var heatmap growthHeatmap
	if errUnmarshal := json.Unmarshal(data, &heatmap); errUnmarshal != nil {
		return growthHeatmapCell{}, nil, fmt.Errorf("heatmap decode: %w", errUnmarshal)
	}
	if len(heatmap.Cells) == 0 {
		return growthHeatmapCell{}, nil, fmt.Errorf("heatmap is empty")
	}
	var lit []string
	for _, cell := range heatmap.Cells {
		if cell.Score > 0 {
			lit = append(lit, cell.Date)
		}
	}
	return heatmap.Cells[len(heatmap.Cells)-1], lit, nil
}

// sendGrowthActivity posts one chat_request_send event.
func sendGrowthActivity(auth workbuddyAuth) error {
	conversation := "wb-" + growthToken()
	now := time.Now().UnixMilli()
	event := growthChatEvent{
		EventCode:            "chat_request_send",
		Timestamp:            now,
		Mode:                 "craft",
		ConversationID:       conversation,
		RequestID:            conversation,
		InputLength:          growthReportInputLen,
		RequestModelID:       growthReportModelID,
		RequestModelName:     growthReportModelName,
		MentionContexts:      []any{},
		KnowledgeID:          []any{},
		KnowledgeName:        []any{},
		PresentAt:            now,
		RootRequestID:        conversation,
		ParentConversationID: conversation,
		AgentName:            "default",
		AgentType:            "conversation",
		UserID:               auth.UID,
	}
	payload, errMarshal := json.Marshal([]growthChatEvent{event})
	if errMarshal != nil {
		return errMarshal
	}
	_, errSend := growthCall(auth, http.MethodPost, growthReportPath, payload)
	return errSend
}

// credentialForSweep is the engine's read of one stored credential. It is a
// variable so a test can drive the engine without a host.
var credentialForSweep = credentialJSONForAuthIndex

// runDailyBonusOne lights one account's day when it is not lit yet.
func runDailyBonusOne(authIndex string, row dailyBonusRow) dailyBonusRow {
	var auth workbuddyAuth
	if credential := credentialForSweep(authIndex); len(credential) > 0 {
		_ = json.Unmarshal(credential, &auth)
	}
	row.Nickname = auth.Nickname
	row.Region = regionOf(auth)

	// International accounts only. The domestic reward is the check-in flow and
	// its growth family lives on a different host, which this engine does not
	// target.
	if row.Region != intlRegion {
		row.Status = "skipped"
		row.Detail = "not an international account"
		return row
	}
	if auth.AccessToken == "" {
		row.Status = "failed"
		row.Detail = "credential has no access token"
		return row
	}

	today, lit, errToday := fetchGrowthToday(auth)
	if errToday != nil {
		row.Status = "failed"
		row.Detail = "heatmap: " + truncate(errToday.Error(), 160)
		return row
	}
	row.Day, row.Score, row.LitDates = today.Date, today.Score, lit
	if today.Score > 0 {
		row.Lit = true
		row.Status = "already-lit"
		return row
	}
	if errSend := sendGrowthActivity(auth); errSend != nil {
		row.Status = "failed"
		row.Detail = "report: " + truncate(errSend.Error(), 160)
		return row
	}
	// The heatmap is aggregated and lags a successful report by a few seconds, so
	// one immediate re-read can still show 0. It is for the log only: the day
	// counts as done either way, and the next slot re-reads it.
	if cell, _, errReread := fetchGrowthToday(auth); errReread == nil {
		row.Score = cell.Score
		row.Lit = cell.Score > 0
	}
	row.Status = "reported"
	return row
}

// runDailyBonus sweeps every WorkBuddy credential.
func runDailyBonus() *dailyBonusSummary {
	summary := &dailyBonusSummary{When: time.Now()}
	entries, errList := workbuddyAccounts()
	if errList != nil {
		summary.Results = append(summary.Results, dailyBonusRow{Status: "failed", Detail: truncate(errList.Error(), 160)})
		recordDailyBonus(summary)
		return summary
	}
	results := make([]dailyBonusRow, len(entries))
	var wait sync.WaitGroup
	slots := make(chan struct{}, dailyBonusWorkers)
	for index, entry := range entries {
		wait.Add(1)
		go func(position int, item hostAuthFileEntry) {
			defer wait.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			authIndex := strings.TrimSpace(item.AuthIndex)
			results[position] = runDailyBonusOne(authIndex, dailyBonusRow{AuthIndex: authIndex})
		}(index, entry)
	}
	wait.Wait()
	summary.Results = results
	recordDailyBonus(summary)
	return summary
}

func recordDailyBonus(summary *dailyBonusSummary) {
	dailyBonusMu.Lock()
	lastDailyBonus = summary
	dailyBonusMu.Unlock()
}

func recordedDailyBonus() *dailyBonusSummary {
	dailyBonusMu.RLock()
	defer dailyBonusMu.RUnlock()
	return lastDailyBonus
}

// shouldRunDailyBonusNow reports whether now falls inside the hour after a
// scheduled slot. The window is an hour wide so a tick that is late, or a boot
// that happens mid-window, still runs.
func shouldRunDailyBonusNow(now time.Time) bool {
	for _, hour := range dailyBonusHours {
		start := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
		if !now.Before(start) && now.Before(start.Add(time.Hour)) {
			return true
		}
	}
	return false
}

// runDailyBonusSafely runs a sweep without letting a panic escape.
//
// This goroutine is not entered through the host's RPC path, so nothing above it
// would recover a panic, and an escaping one would take the host process down.
func runDailyBonusSafely() {
	defer func() {
		if recovered := recover(); recovered != nil {
			recordDailyBonus(&dailyBonusSummary{
				When:    time.Now(),
				Results: []dailyBonusRow{{Status: "failed", Detail: fmt.Sprintf("panic: %v", recovered)}},
			})
		}
	}()
	runDailyBonus()
}

// dailyBonusLoop is the plugin's only timer. Nothing else here is scheduled, so
// it keeps its own goroutine rather than sharing a tick.
//
// It is deliberately never stopped. The host loads this library, calls into it,
// and unloads it on exit, and the reference implementation is explicit that
// touching Go runtime state from the shutdown callback -- mutexes, a channel
// close, goroutine synchronization -- is what produced SIGSEGVs in cgo. This
// goroutine holds nothing that outlives the process.
func dailyBonusLoop() {
	ticker := time.NewTicker(dailyBonusTick)
	defer ticker.Stop()
	for range ticker.C {
		if !dailyBonusEnabled() || !shouldRunDailyBonusNow(time.Now()) {
			continue
		}
		runDailyBonusSafely()
	}
}

func init() { go dailyBonusLoop() }

// dailyBonusRouteResponse answers POST /v0/management/workbuddy/dailybonus.
//
// A manual run ignores the scheduled toggle, the same way the check-in button
// does: the toggle gates unattended runs, not an operator asking for one.
func dailyBonusRouteResponse(raw []byte) pluginapi.ManagementResponse {
	explicit := strings.TrimSpace(firstNonEmpty(managementQueryValue(raw, "auth_index"), managementQueryValue(raw, "authIndex")))
	var results []dailyBonusRow
	if explicit == "" {
		results = runDailyBonus().Results
	} else {
		results = []dailyBonusRow{runDailyBonusOne(explicit, dailyBonusRow{AuthIndex: explicit})}
	}
	body, errMarshal := json.Marshal(map[string]any{"results": results})
	if errMarshal != nil {
		return errorManagementResponse(http.StatusInternalServerError, "failed to encode daily-bonus results")
	}
	return jsonManagementResponse(body)
}

// dailyBonusStatusResponse answers GET /v0/management/workbuddy/dailybonus/status.
//
// The last run is kept in memory only: it is diagnostic, and a restart is
// expected to start with no run recorded rather than with a stale one.
func dailyBonusStatusResponse() pluginapi.ManagementResponse {
	body, errMarshal := json.Marshal(map[string]any{
		"enabled":  dailyBonusEnabled(),
		"hours":    dailyBonusHours,
		"last_run": recordedDailyBonus(),
	})
	if errMarshal != nil {
		return errorManagementResponse(http.StatusInternalServerError, "failed to encode daily-bonus status")
	}
	return jsonManagementResponse(body)
}
