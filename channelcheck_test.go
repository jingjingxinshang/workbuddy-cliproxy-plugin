package main

import (
	"encoding/json"
	"testing"
)

func decodeMessages(t *testing.T, payload []byte) []map[string]any {
	t.Helper()
	var document struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatalf("payload is not readable: %v (%s)", err, payload)
	}
	return document.Messages
}

// A request that already starts with a system prompt must be forwarded byte for
// byte: reshaping it would reorder or re-encode a payload the caller built.
func TestEnsureSystemFirstLeavesAWellShapedRequestAlone(t *testing.T) {
	original := []byte(`{"model":"m","messages":[{"role":"system","content":"be terse"},{"role":"user","content":"hi"}],"temperature":0.2}`)

	got := ensureSystemFirst(original)

	if string(got) != string(original) {
		t.Fatalf("payload changed:\n got %s\nwant %s", got, original)
	}
}

// The reported failure: the caller sent no system message, so the upstream
// answered the channel check. One system message is inserted and the caller's
// messages keep their order and content.
func TestEnsureSystemFirstInsertsASystemMessage(t *testing.T) {
	got := ensureSystemFirst([]byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))

	messages := decodeMessages(t, got)
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want the original plus one system message", len(messages))
	}
	if messages[0]["role"] != "system" {
		t.Fatalf("first role = %v, want system", messages[0]["role"])
	}
	if messages[0]["content"] == "" {
		t.Fatal("the inserted system message carries no content")
	}
	if messages[1]["role"] != "user" || messages[1]["content"] != "hi" {
		t.Fatalf("caller message changed: %+v", messages[1])
	}
}

// A developer first message is the same rejection, so its role is promoted in
// place: the content is neither dropped nor duplicated.
func TestEnsureSystemFirstPromotesADeveloperMessage(t *testing.T) {
	got := ensureSystemFirst([]byte(`{"messages":[{"role":"developer","content":"follow the house rules"},{"role":"user","content":"hi"}]}`))

	messages := decodeMessages(t, got)
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want the payload untouched in count", len(messages))
	}
	if messages[0]["role"] != "system" || messages[0]["content"] != "follow the house rules" {
		t.Fatalf("developer message not promoted in place: %+v", messages[0])
	}
}

// A payload with no conversation is not a payload to invent one for.
func TestEnsureSystemFirstDoesNotInventAConversation(t *testing.T) {
	for _, payload := range []string{`{"model":"m"}`, `{"model":"m","messages":[]}`, `not json`} {
		if got := ensureSystemFirst([]byte(payload)); string(got) != payload {
			t.Fatalf("payload %s changed to %s", payload, got)
		}
	}
}
