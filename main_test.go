package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

func TestRegionFallback(t *testing.T) {
	if got := regionOf(workbuddyAuth{Region: "intl"}); got != "intl" {
		t.Fatalf("regionOf(intl) = %q", got)
	}
	if got := regionOf(workbuddyAuth{Region: "unknown"}); got != "cn" {
		t.Fatalf("regionOf(unknown) = %q", got)
	}
}

func TestSplitSSE(t *testing.T) {
	chunks := splitSSE([]byte("data: {\"a\":1}\n\n\ndata: [DONE]\n\n"))
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks", len(chunks))
	}
	if string(chunks[0].Payload) != "data: {\"a\":1}\n\n" {
		t.Fatalf("unexpected first chunk: %q", chunks[0].Payload)
	}
	if string(chunks[1].Payload) != "data: [DONE]\n\n" {
		t.Fatalf("unexpected last chunk: %q", chunks[1].Payload)
	}

	// The gateway can pretty-print one payload across several lines, and a blank
	// line can sit inside it. Framing on the blank line handed the client half a
	// JSON object, which is what "the tool call disappeared and the stream just
	// stopped" looks like from the other side.
	pretty := "data: {\n\ndata: \"a\": 1,\ndata: \"b\": {\"c\": 2}\n\ndata: }\n\n"
	chunks = splitSSE([]byte(pretty))
	if len(chunks) != 1 {
		t.Fatalf("pretty-printed payload split into %d chunks: %q", len(chunks), chunks)
	}
	if string(chunks[0].Payload) != "data: {\n\"a\": 1,\n\"b\": {\"c\": 2}\n}\n\n" {
		t.Fatalf("payload not reassembled: %q", chunks[0].Payload)
	}

	// A brace inside a string must not close the payload early, and a payload
	// with no braces at all is complete on arrival rather than held.
	tricky := splitSSE([]byte("data: {\"text\":\"a } b\"}\n\ndata: [DONE]\n\n"))
	if len(tricky) != 2 {
		t.Fatalf("brace in a string confused the depth: %q", tricky)
	}

	// A keep-alive comment is dropped rather than turned into an empty event, and
	// the prefix survives being layered by a proxy.
	layered := splitSSE([]byte(": keep-alive\n\ndata: data: {\"a\":1}\n\n"))
	if len(layered) != 1 || string(layered[0].Payload) != "data: {\"a\":1}\n\n" {
		t.Fatalf("layered prefix or comment mishandled: %q", layered)
	}

	// A body that is not an event stream at all is passed through untouched, so a
	// plain JSON error still reaches the client.
	plain := splitSSE([]byte(`{"error":"nope"}`))
	if len(plain) != 1 || string(plain[0].Payload) != `{"error":"nope"}` {
		t.Fatalf("plain body not passed through: %q", plain)
	}
}

func TestCatalogTags(t *testing.T) {
	if !hasTag([]string{"lite", "internal"}, "lite") {
		t.Fatal("expected lite tag")
	}
	if hasTag([]string{"chat"}, "lite") {
		t.Fatal("did not expect lite tag")
	}
}

// managementRequestFor builds the JSON envelope the host sends to
// management.handle for one request path.
func managementRequestFor(path string) []byte {
	return managementRequestWithMethod("GET", path)
}

// managementRequestWithMethod is managementRequestFor with an explicit method,
// because the host dispatches plugin routes by method and path together.
func managementRequestWithMethod(method, path string) []byte {
	raw, errMarshal := json.Marshal(map[string]any{
		"method": method,
		"path":   path,
		"query":  map[string]any{},
	})
	if errMarshal != nil {
		panic(errMarshal)
	}
	return raw
}

// The two path spaces overlap by suffix, which is exactly how the quota menu
// ended up showing the JSON payload instead of the page.
func TestResourcePageRouting(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{"accounts page", "/v0/resource/plugins/workbuddy/quota", "<title>WorkBuddy 账号</title>"},
		{"accounts page with trailing slash", "/v0/resource/plugins/workbuddy/quota/", "<title>WorkBuddy 账号</title>"},
		// The host derives the id from the library file name, so the resource
		// base is not necessarily the provider id.
		{"id derived from file name", "/v0/resource/plugins/workbuddy-v0.2.2/quota", "<title>WorkBuddy 账号</title>"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			resp := handleManagement(managementRequestFor(testCase.path))

			if got := resp.Headers.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
				t.Fatalf("content type = %q, want text/html", got)
			}
			if body := string(resp.Body); !strings.Contains(body, testCase.want) {
				t.Fatalf("body does not contain %q", testCase.want)
			}
		})
	}
}

// The resource path ends in "/workbuddy/quota" too, so it must never be
// answered by the JSON route.
func TestResourceQuotaPageIsNotTheJSONRoute(t *testing.T) {
	body := string(handleManagement(managementRequestFor("/v0/resource/plugins/workbuddy/quota")).Body)

	if strings.HasPrefix(strings.TrimSpace(body), "{") {
		t.Fatalf("resource page answered with a JSON payload: %s", truncate(body, 80))
	}
	if !strings.Contains(body, "<!doctype html") {
		t.Fatal("resource page is not an HTML document")
	}
}

func TestManagementRouteStillAnswersJSON(t *testing.T) {
	resp := handleManagement(managementRequestFor("/v0/management/workbuddy/quota"))

	if got := resp.Headers.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("content type = %q, want application/json", got)
	}
	if strings.Contains(string(resp.Body), "<!doctype html") {
		t.Fatal("management route returned a page instead of a payload")
	}
}

func TestUnknownManagementPathIsNotFound(t *testing.T) {
	resp := handleManagement(managementRequestFor("/v0/management/workbuddy/nope"))

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

// Login is not a plugin page any more: the panel's OAuth page starts the host's
// generic plugin auth flow itself. The retired URL must answer 404 rather than
// serve a page or fall through to the quota JSON route.
func TestLoginResourcePageIsGone(t *testing.T) {
	for _, path := range []string{
		"/v0/resource/plugins/workbuddy/",
		"/v0/resource/plugins/workbuddy",
	} {
		resp := handleManagement(managementRequestFor(path))

		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want %d", path, resp.StatusCode, http.StatusNotFound)
		}
		if body := string(resp.Body); strings.Contains(body, "<!doctype html") {
			t.Fatalf("%s: answered with an HTML page", path)
		}
	}
}

// The plugin must not advertise a login menu again, because that menu was what
// forced operators to re-enter the management key inside the panel.
func TestManagementRegistrationAdvertisesTheAccountsPage(t *testing.T) {
	raw, errHandle := handleMethod(pluginabi.MethodManagementRegister, nil)
	if errHandle != nil {
		t.Fatalf("handleMethod() error = %v", errHandle)
	}

	body := string(raw)
	if !strings.Contains(body, "/quota") {
		t.Fatal("accounts resource route is missing")
	}
	if strings.Contains(body, "登录") {
		t.Fatalf("registration still advertises a login page: %s", truncate(body, 160))
	}

	// The host dispatches plugin routes by exact method and path, so a route that
	// is handled but not declared (or declared with the wrong method) is a 404.
	var envelope struct {
		Result managementRegistration `json:"result"`
	}
	if errUnmarshal := json.Unmarshal(raw, &envelope); errUnmarshal != nil {
		t.Fatalf("registration is not readable: %v", errUnmarshal)
	}
	// Routes are declared relative to the management prefix, which the host adds
	// when it registers them.
	want := map[string]string{
		"/workbuddy/accounts": "GET",
		"/workbuddy/quota":    "GET",
		"/workbuddy/checkin":  "POST",
	}
	got := map[string]string{}
	for _, route := range envelope.Result.Routes {
		got[route.Path] = strings.ToUpper(route.Method)
	}
	for path, method := range want {
		if got[path] != method {
			t.Fatalf("route %s = %q, want %s", path, got[path], method)
		}
	}
}

// lifecycleRequestFor builds the plugin.register payload the host sends: the
// plugin config encoded as YAML inside the JSON request.
func lifecycleRequestFor(configYAML string) []byte {
	raw, errMarshal := json.Marshal(lifecycleRequest{ConfigYAML: []byte(configYAML), SchemaVersion: pluginabi.SchemaVersion})
	if errMarshal != nil {
		panic(errMarshal)
	}
	return raw
}

// Region is the only setting WorkBuddy login needs, and the panel's plugin
// config editor is the only place it is written.
func TestConfigureReadsDefaultRegion(t *testing.T) {
	t.Cleanup(func() { _ = configure(nil) })

	if errConfigure := configure(lifecycleRequestFor("enabled: true\npriority: 0\ndefault_region: intl\n")); errConfigure != nil {
		t.Fatalf("configure() error = %v", errConfigure)
	}
	if got := configuredRegion(); got != "intl" {
		t.Fatalf("configuredRegion() = %q, want intl", got)
	}
	if got := loginRegion(nil); got != "intl" {
		t.Fatalf("loginRegion(nil) = %q, want intl", got)
	}
	// The host turns the query string of the auth URL into this metadata, so an
	// explicit region still overrides the configured default.
	if got := loginRegion(map[string]any{"region": "cn"}); got != "cn" {
		t.Fatalf("loginRegion(region=cn) = %q, want cn", got)
	}
}

func TestConfigureFallsBackOnUnusableRegion(t *testing.T) {
	t.Cleanup(func() { _ = configure(nil) })

	if errConfigure := configure(lifecycleRequestFor("default_region: jp\n")); errConfigure != nil {
		t.Fatalf("configure() error = %v", errConfigure)
	}
	if got := configuredRegion(); got != "cn" {
		t.Fatalf("configuredRegion() = %q, want the cn fallback", got)
	}
}

func TestConfigureWithoutConfigKeepsDefaultRegion(t *testing.T) {
	t.Cleanup(func() { _ = configure(nil) })

	if errConfigure := configure(lifecycleRequestFor("")); errConfigure != nil {
		t.Fatalf("configure() error = %v", errConfigure)
	}
	if got := configuredRegion(); got != "cn" {
		t.Fatalf("configuredRegion() = %q, want cn", got)
	}
}

// The status endpoint reports a seasonal ACTIVITY, not the daily bonus: with no
// activity running it zeroes the whole block, `today_checked_in` included. A
// zeroed block therefore means "cannot tell", and reporting it as "unclaimed"
// is the bug this pins down — it claimed accounts had not signed in when they
// had, which upstream then answered with code 10001.
func TestCheckinStatusVerdictTreatsInactiveActivityAsUnknown(t *testing.T) {
	got := checkinStatusVerdict(0, checkinStatusData{Active: false, TodayCheckedIn: false})
	if got.State != checkinUnknown {
		t.Fatalf("inactive activity verdict = %q, want unknown", got.State)
	}

	if got := checkinStatusVerdict(0, checkinStatusData{Active: true, TodayCheckedIn: true, StreakDays: 4}); got.State != checkinClaimed {
		t.Fatalf("active+checked verdict = %q, want claimed", got.State)
	}
	if got := checkinStatusVerdict(0, checkinStatusData{Active: true}); got.State != checkinUnclaimed {
		t.Fatalf("active+unchecked verdict = %q, want unclaimed", got.State)
	}
	if got := checkinStatusVerdict(checkinAlreadyClaimedCode, checkinStatusData{}); got.State != checkinClaimed {
		t.Fatalf("10001 verdict = %q, want claimed", got.State)
	}
	if got := checkinStatusVerdict(500, checkinStatusData{Active: true}); got.State != checkinUnknown || got.Error != "code=500" {
		t.Fatalf("error verdict = %#v, want unknown with the code", got)
	}
}

// An active activity must carry its detail through, so the page can show the
// streak without a second call.
func TestCheckinStatusVerdictKeepsActivityDetail(t *testing.T) {
	got := checkinStatusVerdict(0, checkinStatusData{
		Active:         true,
		TodayCheckedIn: true,
		StreakDays:     7,
		TodayCredit:    300,
		TotalCredits:   2100,
		ActivityName:   "秋日签到",
		WeekProgress:   []bool{true, true, false},
	})

	if got.StreakDays != 7 || got.TodayCredit != 300 || got.TotalCredits != 2100 || got.ActivityName != "秋日签到" {
		t.Fatalf("activity detail dropped: %#v", got)
	}
	if len(got.WeekProgress) != 3 {
		t.Fatalf("week progress dropped: %#v", got.WeekProgress)
	}
}

func TestCheckinClaimVerdict(t *testing.T) {
	if got := checkinClaimVerdict(0, 300); got.State != checkinClaimed || !got.FreshlyClaimed || got.Credit != 300 {
		t.Fatalf("fresh claim = %#v, want claimed with 300 credits", got)
	}
	// A repeat claim is HTTP 400 with code 10001: already claimed, not a failure.
	if got := checkinClaimVerdict(checkinAlreadyClaimedCode, 0); got.State != checkinClaimed || got.FreshlyClaimed {
		t.Fatalf("repeat claim = %#v, want claimed without freshly_claimed", got)
	}
	if got := checkinClaimVerdict(40301, 0); got.State != checkinUnknown || got.Error != "code=40301" {
		t.Fatalf("failed claim = %#v, want unknown with the code", got)
	}
}

// The refresh hook must not touch a credential it cannot use, and must tolerate
// a nil pointer: it runs inside the host's refresh worker.
func TestMaybeCheckinSkipsUnusableCredential(t *testing.T) {
	maybeCheckin(nil)

	auth := workbuddyAuth{}
	maybeCheckin(&auth)
	if auth.LastCheckinDay != "" {
		t.Fatalf("last check-in day = %q, want empty", auth.LastCheckinDay)
	}
}

// A route method the host never sends (it dispatches by exact method and path)
// must not be answered as if it were the real one.
func TestCheckinRouteRejectsNonPost(t *testing.T) {
	resp := handleManagement(managementRequestWithMethod(http.MethodGet, "/v0/management/workbuddy/checkin"))
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET checkin status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
	resp = handleManagement(managementRequestWithMethod(http.MethodPost, "/v0/management/workbuddy/accounts"))
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST accounts status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
}
