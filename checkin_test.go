package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// stubRegion points the CN cluster at a test server for the duration of one
// test. Meter calls read their base URL from the region profile, so this is the
// seam that makes "how many upstream calls did the refresh hook make"
// observable.
func stubRegion(t *testing.T, url string) {
	t.Helper()
	old := profiles["cn"]
	profiles["cn"] = regionProfile{baseURL: url, origin: "http://workbuddy.test", userAgent: "test-agent", loginPlatform: "CLI"}
	t.Cleanup(func() { profiles["cn"] = old })
}

// stubCheckinOnRefresh sets the refresh-hook switch for one test and restores
// whatever was there before.
func stubCheckinOnRefresh(t *testing.T, value *bool) {
	t.Helper()
	configMu.Lock()
	old := pluginCfg.CheckinOnRefresh
	pluginCfg.CheckinOnRefresh = value
	configMu.Unlock()
	t.Cleanup(func() {
		configMu.Lock()
		pluginCfg.CheckinOnRefresh = old
		configMu.Unlock()
	})
}

func boolPtr(v bool) *bool { return &v }

// meterServer answers every meter call with one body and records the paths it
// was asked for.
func meterServer(t *testing.T, status int, body string) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &paths
}

// The refresh hook runs while the host may be waiting on this plugin to serve a
// request, so it must make one meter call, not two. Probing the status endpoint
// first cannot settle whether the bonus is already taken -- there is no
// read-only source for it -- so the probe was only ever a second round trip in
// front of the claim.
func TestMaybeCheckinClaimsOnceWithoutProbing(t *testing.T) {
	srv, paths := meterServer(t, http.StatusOK, `{"code":10001,"msg":"already checked in","data":{}}`)
	stubRegion(t, srv.URL)
	stubCheckinOnRefresh(t, boolPtr(true))

	auth := workbuddyAuth{AccessToken: "token", Region: "cn"}
	maybeCheckin(&auth)

	if len(*paths) != 1 {
		t.Fatalf("meter calls = %v, want exactly one (the claim, with no status probe)", *paths)
	}
	if (*paths)[0] != "/billing/meter/daily-checkin" {
		t.Fatalf("meter call = %q, want the claim endpoint", (*paths)[0])
	}
	// A repeat claim is the normal case on a day that was already claimed. If it
	// did not record the day, every later refresh would claim again.
	if auth.LastCheckinDay != time.Now().Format(time.DateOnly) {
		t.Fatalf("LastCheckinDay = %q after an already-claimed answer, want today", auth.LastCheckinDay)
	}
}

// A claim that fails must leave the day unrecorded so the next refresh tries
// again; recording it would skip the bonus for the rest of the day.
func TestMaybeCheckinRetriesAfterAFailedClaim(t *testing.T) {
	srv, paths := meterServer(t, http.StatusInternalServerError, `{"code":500,"msg":"boom"}`)
	stubRegion(t, srv.URL)
	stubCheckinOnRefresh(t, boolPtr(true))

	auth := workbuddyAuth{AccessToken: "token", Region: "cn"}
	maybeCheckin(&auth)

	if len(*paths) != 1 {
		t.Fatalf("meter calls = %v, want one attempt", *paths)
	}
	if auth.LastCheckinDay != "" {
		t.Fatalf("LastCheckinDay = %q after a failed claim, want empty so the next refresh retries", auth.LastCheckinDay)
	}
}

func TestMaybeCheckinSkipsWhenDisabled(t *testing.T) {
	srv, paths := meterServer(t, http.StatusOK, `{"code":0,"data":{"credit":50}}`)
	stubRegion(t, srv.URL)
	stubCheckinOnRefresh(t, boolPtr(false))

	auth := workbuddyAuth{AccessToken: "token", Region: "cn"}
	maybeCheckin(&auth)

	if len(*paths) != 0 {
		t.Fatalf("meter calls = %v, want none when checkin_on_refresh is off", *paths)
	}
	if auth.LastCheckinDay != "" {
		t.Fatalf("LastCheckinDay = %q, want empty when the hook is off", auth.LastCheckinDay)
	}
}

func TestMaybeCheckinSkipsWhenAlreadyClaimedToday(t *testing.T) {
	srv, paths := meterServer(t, http.StatusOK, `{"code":0,"data":{"credit":50}}`)
	stubRegion(t, srv.URL)
	stubCheckinOnRefresh(t, boolPtr(true))

	auth := workbuddyAuth{AccessToken: "token", Region: "cn", LastCheckinDay: time.Now().Format(time.DateOnly)}
	maybeCheckin(&auth)

	if len(*paths) != 0 {
		t.Fatalf("meter calls = %v, want none once the day is recorded", *paths)
	}
}

// An operator who never set the key must keep the behaviour from before the
// switch existed, otherwise an existing config silently stops claiming.
func TestCheckinOnRefreshDefaultsToEnabled(t *testing.T) {
	stubCheckinOnRefresh(t, nil)
	if !checkinOnRefreshEnabled() {
		t.Fatal("unset checkin_on_refresh must mean enabled")
	}

	stubCheckinOnRefresh(t, boolPtr(false))
	if checkinOnRefreshEnabled() {
		t.Fatal("explicit false must disable the hook")
	}

	stubCheckinOnRefresh(t, boolPtr(true))
	if !checkinOnRefreshEnabled() {
		t.Fatal("explicit true must enable the hook")
	}
}

// The claim-only path from the refresh hook depends on a repeat claim being
// reported as claimed rather than as a failure; that is what makes dropping the
// status probe safe.
func TestRepeatClaimVerdictIsClaimed(t *testing.T) {
	if got := checkinClaimVerdict(checkinAlreadyClaimedCode, 0); got.State != checkinClaimed || got.FreshlyClaimed {
		t.Fatalf("repeat claim verdict = %+v, want claimed and not freshly claimed", got)
	}
	if got := checkinClaimVerdict(0, 50); got.State != checkinClaimed || !got.FreshlyClaimed {
		t.Fatalf("fresh claim verdict = %+v, want claimed and freshly claimed", got)
	}
}
