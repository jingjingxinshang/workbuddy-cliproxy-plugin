package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// stubIntlRegion points the international cluster at a test server.
func stubIntlRegion(t *testing.T, url string) {
	t.Helper()
	previous := profiles[intlRegion]
	profiles[intlRegion] = regionProfile{baseURL: url, origin: url, userAgent: "test-agent", loginPlatform: "workbuddy-ai"}
	t.Cleanup(func() { profiles[intlRegion] = previous })
}

// stubCredential makes the engine see one credential without a host.
func stubCredential(t *testing.T, auth workbuddyAuth) {
	t.Helper()
	previous := credentialForSweep
	credentialForSweep = func(string) []byte {
		raw, errMarshal := json.Marshal(auth)
		if errMarshal != nil {
			t.Fatalf("marshal stub credential: %v", errMarshal)
		}
		return raw
	}
	t.Cleanup(func() { credentialForSweep = previous })
}

// growthStub answers the two growth endpoints, records every call and keeps the
// bodies of the reports.
func growthStub(t *testing.T, todayScore int, reportStatus int) (*httptest.Server, *[]string, *[]json.RawMessage) {
	t.Helper()
	calls := &[]string{}
	reports := &[]json.RawMessage{}
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		*calls = append(*calls, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPost {
			*reports = append(*reports, json.RawMessage(body))
		}
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == growthHeatmapPath {
			_, _ = w.Write([]byte(fmt.Sprintf(
				`{"code":0,"data":{"cells":[{"date":"2026-10-08","score":2},{"date":"2026-10-09","score":%d}]}}`, todayScore)))
			return
		}
		w.WriteHeader(reportStatus)
		_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
	}))
	t.Cleanup(server.Close)
	return server, calls, reports
}

// The reward for a domestic account is the check-in flow, and its growth family
// lives on another host, so the engine must not touch one at all.
func TestDailyBonusSkipsDomesticAccounts(t *testing.T) {
	server, calls, reports := growthStub(t, 0, http.StatusOK)
	stubIntlRegion(t, server.URL)
	stubCredential(t, workbuddyAuth{AccessToken: "t", Domain: "www.codebuddy.cn"})

	row := runDailyBonusOne("auth-1", dailyBonusRow{AuthIndex: "auth-1"})

	if row.Status != "skipped" {
		t.Fatalf("status = %q, want skipped", row.Status)
	}
	if len(*calls) != 0 || len(*reports) != 0 {
		t.Fatalf("a domestic account produced traffic: calls=%v reports=%d", *calls, len(*reports))
	}
}

// A day that is already lit must cost one read and nothing else. That is what
// makes four scheduled slots a day free.
func TestDailyBonusDoesNotReportALitDay(t *testing.T) {
	server, calls, reports := growthStub(t, 2, http.StatusOK)
	stubIntlRegion(t, server.URL)
	stubCredential(t, workbuddyAuth{AccessToken: "t", Region: intlRegion, Domain: "www.workbuddy.ai"})

	row := runDailyBonusOne("auth-1", dailyBonusRow{AuthIndex: "auth-1"})

	if row.Status != "already-lit" || !row.Lit {
		t.Fatalf("row = %+v, want already-lit and lit", row)
	}
	if len(*reports) != 0 {
		t.Fatalf("reported a day that was already lit (%d report(s))", len(*reports))
	}
	if len(*calls) != 1 || (*calls)[0] != "GET "+growthHeatmapPath {
		t.Fatalf("calls = %v, want a single heatmap read", *calls)
	}
}

// An unlit day gets exactly one beacon, carrying the event the upstream counts.
func TestDailyBonusReportsAnUnlitDayOnce(t *testing.T) {
	server, calls, reports := growthStub(t, 0, http.StatusOK)
	stubIntlRegion(t, server.URL)
	stubCredential(t, workbuddyAuth{AccessToken: "t", Region: intlRegion, Domain: "www.workbuddy.ai", UID: "uid-1"})

	row := runDailyBonusOne("auth-1", dailyBonusRow{AuthIndex: "auth-1"})

	if row.Status != "reported" {
		t.Fatalf("status = %q detail = %q, want reported", row.Status, row.Detail)
	}
	if len(*reports) != 1 {
		t.Fatalf("reports = %d, want exactly one", len(*reports))
	}
	var events []growthChatEvent
	if errUnmarshal := json.Unmarshal((*reports)[0], &events); errUnmarshal != nil {
		t.Fatalf("report body is not the event array: %v (%s)", errUnmarshal, (*reports)[0])
	}
	if len(events) != 1 || events[0].EventCode != "chat_request_send" {
		t.Fatalf("events = %+v, want one chat_request_send", events)
	}
	if events[0].UserID != "uid-1" {
		t.Fatalf("event userId = %q, want the account uid", events[0].UserID)
	}
	if events[0].Mode == "" || events[0].ConversationID == "" || events[0].RequestID != events[0].ConversationID {
		t.Fatalf("event ids are not shaped like a fresh conversation: %+v", events[0])
	}
	// Read, report, then one best-effort re-read for the log.
	if len(*calls) != 3 {
		t.Fatalf("calls = %v, want read + report + read", *calls)
	}
}

// A failed report must never be resent: the event is day-idempotent upstream,
// but a blind resend double-counts the day (score 2 -> 4). Waiting for the next
// slot is the whole retry policy.
func TestDailyBonusNeverRetriesTheReport(t *testing.T) {
	server, calls, reports := growthStub(t, 0, http.StatusInternalServerError)
	stubIntlRegion(t, server.URL)
	stubCredential(t, workbuddyAuth{AccessToken: "t", Region: intlRegion, Domain: "www.workbuddy.ai"})

	row := runDailyBonusOne("auth-1", dailyBonusRow{AuthIndex: "auth-1"})

	if row.Status != "failed" {
		t.Fatalf("status = %q, want failed", row.Status)
	}
	if len(*reports) != 1 {
		t.Fatalf("report attempts = %d, want exactly one and no retry", len(*reports))
	}
	if len(*calls) != 2 {
		t.Fatalf("calls = %v, want the read and the single failed report", *calls)
	}
}

func TestDailyBonusScheduleWindow(t *testing.T) {
	cases := []struct {
		hour int
		min  int
		want bool
	}{
		{7, 59, false},
		{8, 0, true},
		{8, 59, true},
		{9, 0, false},
		{11, 30, false},
		{12, 30, true},
		{16, 5, true},
		{20, 30, true},
		{21, 30, false},
		{0, 0, false},
	}
	for _, tc := range cases {
		now := time.Date(2026, 10, 9, tc.hour, tc.min, 0, 0, time.Local)
		if got := shouldRunDailyBonusNow(now); got != tc.want {
			t.Errorf("shouldRunDailyBonusNow(%02d:%02d) = %v, want %v", tc.hour, tc.min, got, tc.want)
		}
	}
}

// An operator who never set the key keeps the engine: it only ever acts on
// international credentials, which is what they installed the plugin for.
func TestDailyBonusDefaultsToEnabled(t *testing.T) {
	setDailyBonus(t, nil)
	if !dailyBonusEnabled() {
		t.Fatal("unset daily_bonus must mean enabled")
	}
	setDailyBonus(t, boolPtr(false))
	if dailyBonusEnabled() {
		t.Fatal("explicit false must stop the scheduled run")
	}
	setDailyBonus(t, boolPtr(true))
	if !dailyBonusEnabled() {
		t.Fatal("explicit true must allow the scheduled run")
	}
}

func boolPtr(v bool) *bool { return &v }

func setDailyBonus(t *testing.T, value *bool) {
	t.Helper()
	configMu.Lock()
	previous := pluginCfg.DailyBonus
	pluginCfg.DailyBonus = value
	configMu.Unlock()
	t.Cleanup(func() {
		configMu.Lock()
		pluginCfg.DailyBonus = previous
		configMu.Unlock()
	})
}
