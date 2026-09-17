package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
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
	raw, errMarshal := json.Marshal(map[string]any{
		"method": "GET",
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
		{"quota page", "/v0/resource/plugins/workbuddy/quota", "<title>WorkBuddy 额度</title>"},
		{"quota page with trailing slash", "/v0/resource/plugins/workbuddy/quota/", "<title>WorkBuddy 额度</title>"},
		{"login page root", "/v0/resource/plugins/workbuddy/", "<title>WorkBuddy 登录</title>"},
		{"login page without slash", "/v0/resource/plugins/workbuddy", "<title>WorkBuddy 登录</title>"},
		// The host derives the id from the library file name, so the resource
		// base is not necessarily the provider id.
		{"id derived from file name", "/v0/resource/plugins/workbuddy-v0.2.2/quota", "<title>WorkBuddy 额度</title>"},
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
