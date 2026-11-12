package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestTaskDecisions(t *testing.T) {
	tests := []struct {
		reason    string
		task      growthTask
		accepts   bool
		claimable bool
	}{
		{reason: "progress met is claimable and no longer pending", task: growthTask{TaskCode: "a", Current: 5, Target: 5}, accepts: false, claimable: true},
		{reason: "progress short is pending", task: growthTask{TaskCode: "a", Current: 2, Target: 5}, accepts: true, claimable: false},
		{reason: "already claimed is neither", task: growthTask{TaskCode: "a", Current: 5, Target: 5, Claimed: true}, accepts: false, claimable: false},
		{reason: "locked is neither", task: growthTask{TaskCode: "a", Current: 5, Target: 5, Locked: true}, accepts: false, claimable: false},
		{reason: "an accepted task is not accepted twice", task: growthTask{TaskCode: "a", Current: 1, Target: 5, AcceptStatus: "accepted"}, accepts: false, claimable: false},
		{reason: "an upstream complete status counts as progress", task: growthTask{TaskCode: "a", Status: "completed"}, accepts: false, claimable: true},
	}
	for _, test := range tests {
		if got := taskAcceptsPending(test.task); got != test.accepts {
			t.Errorf("%s: taskAcceptsPending = %v, want %v", test.reason, got, test.accepts)
		}
		if got := taskClaimable(test.task); got != test.claimable {
			t.Errorf("%s: taskClaimable = %v, want %v", test.reason, got, test.claimable)
		}
	}
}

// A sweep must accept what is waiting, claim what is finished, and leave locked
// and already-claimed tasks alone.
func TestRunAutoTasksOneAcceptsAndClaims(t *testing.T) {
	var mu sync.Mutex
	var accepts [][]string
	var claimed []string
	reportCalls := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/activity/growth/tasks":
			_, _ = w.Write([]byte(`{"code":0,"data":{"tasks":[` +
				`{"task_code":"pending","current":1,"target":5},` +
				`{"task_code":"done","current":5,"target":5},` +
				`{"task_code":"locked","current":5,"target":5,"locked":true},` +
				`{"task_code":"paid","current":5,"target":5,"claimed":true}]}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v2/activity/growth/tasks/accept":
			var body struct {
				TaskCodes []string `json:"task_codes"`
			}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			accepts = append(accepts, body.TaskCodes)
			_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/activity/growth/tasks/done/claim":
			claimed = append(claimed, "done")
			_, _ = w.Write([]byte(`{"code":0,"data":{"credit":100,"energy":1}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v2/report":
			reportCalls++
			_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
		default:
			_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
		}
	}))
	defer srv.Close()
	stubRegion(t, srv.URL)

	previousList, previousCredential := authListSource, credentialForSweep
	defer func() { authListSource, credentialForSweep = previousList, previousCredential }()
	authListSource = func() ([]hostAuthFileEntry, error) {
		return []hostAuthFileEntry{{AuthIndex: "auth-1", Provider: providerID}}, nil
	}
	credentialForSweep = func(string) []byte {
		return []byte(`{"access_token":"at","uid":"u1","region":"cn"}`)
	}

	result := runAutoTasksOne(autoTaskCredential{AuthIndex: "auth-1", Auth: workbuddyAuth{AccessToken: "at", UID: "u1", Region: "cn"}})

	if result.Error != "" {
		t.Fatalf("sweep error: %s", result.Error)
	}
	if len(accepts) != 1 || len(accepts[0]) != 1 || accepts[0][0] != "pending" {
		t.Fatalf("accepted = %v, want only the pending task", accepts)
	}
	if len(claimed) != 1 || claimed[0] != "done" {
		t.Fatalf("claimed = %v, want only the finished task", claimed)
	}
	if result.Accepted != 1 || result.Claimed != 1 {
		t.Fatalf("counters = %+v, want one accept and one claim", result)
	}
	if reportCalls != 0 {
		t.Fatalf("activity reports = %d, want none: no chat task is in the list", reportCalls)
	}
}

// An enterprise credential has no personal growth system and the international
// realm is read-only, so neither may produce a single upstream call.
func TestAutoTaskCredentialsSkipsEnterpriseAndInternational(t *testing.T) {
	previousList, previousCredential := authListSource, credentialForSweep
	defer func() { authListSource, credentialForSweep = previousList, previousCredential }()
	authListSource = func() ([]hostAuthFileEntry, error) {
		return []hostAuthFileEntry{
			{AuthIndex: "personal-cn", Provider: providerID},
			{AuthIndex: "enterprise", Provider: providerID},
			{AuthIndex: "international", Provider: providerID},
			{AuthIndex: "disabled", Provider: providerID, Disabled: true},
		}, nil
	}
	credentialForSweep = func(index string) []byte {
		switch index {
		case "personal-cn":
			return []byte(`{"access_token":"at","region":"cn"}`)
		case "enterprise":
			return []byte(`{"access_token":"at","region":"cn","enterprise_id":"e1"}`)
		case "international":
			return []byte(`{"access_token":"at","region":"intl"}`)
		default:
			return []byte(`{"access_token":"at","region":"cn"}`)
		}
	}

	credentials := autoTaskCredentials()

	if len(credentials) != 1 || credentials[0].AuthIndex != "personal-cn" {
		t.Fatalf("credentials = %+v, want only the personal domestic one", credentials)
	}
}
