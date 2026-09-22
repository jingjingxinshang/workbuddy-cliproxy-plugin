package main

import (
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

// The stream id is the only way a chunk reaches the client, and it arrives as a
// top-level field beside the documented request. Reading it is what makes real
// streaming possible at all, so it is asserted rather than assumed.
func TestExecutorWireCarriesStreamID(t *testing.T) {
	wire := executorWire{}
	// Payload is a []byte field, so the host sends it base64-encoded inside the
	// JSON envelope; that is also why the executor decodes it before use.
	raw, err := json.Marshal(map[string]any{
		"stream_id":        "stream-42",
		"host_callback_id": "cb-7",
		"model":            "workbuddy",
		"payload":          []byte(`{"messages":[]}`),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal wire: %v", err)
	}
	if wire.StreamID != "stream-42" {
		t.Fatalf("stream id = %q, want stream-42", wire.StreamID)
	}
	if wire.HostCallbackID != "cb-7" {
		t.Fatalf("callback id = %q, want cb-7", wire.HostCallbackID)
	}
	if wire.Model != "workbuddy" {
		t.Fatalf("embedded request lost its model: %q", wire.Model)
	}

	// A host that does not stream sends no stream id, and the batch path stays
	// the answer for that call.
	var plain executorWire
	if err := json.Unmarshal([]byte(`{"model":"m"}`), &plain); err != nil {
		t.Fatalf("unmarshal plain: %v", err)
	}
	if plain.StreamID != "" {
		t.Fatalf("stream id = %q, want empty", plain.StreamID)
	}
}

// An event can arrive across several reads, so the framer has to hold an
// incomplete event back instead of emitting half of it.
func TestSSEFramerHoldsIncompleteEvents(t *testing.T) {
	framer := &sseFramer{}

	// The first read ends mid-event.
	if events := framer.push([]byte("data: {\"a\":")); len(events) != 0 {
		t.Fatalf("incomplete event produced %d events: %q", len(events), events)
	}
	// Completing it, with the blank line, releases exactly one event.
	events := framer.push([]byte("1}\n\n"))
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if string(events[0]) != `data: {"a":1}` {
		t.Fatalf("unexpected event: %q", events[0])
	}
}

// A multi-line event and its fields belong to one event, and a keep-alive
// comment is kept because forwarding it is what holds a long stream open.
func TestSSEFramerKeepsEventShape(t *testing.T) {
	framer := &sseFramer{}
	events := framer.push([]byte("event: message\nid: 7\ndata: first\ndata: second\n\n: keep-alive\n\n"))
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2: %q", len(events), events)
	}
	if string(events[0]) != "event: message\nid: 7\ndata: first\ndata: second" {
		t.Fatalf("multi-line event not preserved: %q", events[0])
	}
	if string(events[1]) != ": keep-alive" {
		t.Fatalf("keep-alive block not preserved: %q", events[1])
	}
}

// CRLF framing is normalized even when the boundary falls between two reads.
func TestSSEFramerNormalizesSplitCRLF(t *testing.T) {
	framer := &sseFramer{}
	if events := framer.push([]byte("data: ok\r")); len(events) != 0 {
		t.Fatalf("split CRLF framed early: %q", events)
	}
	events := framer.push([]byte("\n\r\n"))
	if len(events) != 1 || string(events[0]) != "data: ok" {
		t.Fatalf("got %q, want one normalized event", events)
	}
}

// A stream that stops without its final blank line still delivers its last
// payload rather than dropping it.
func TestSSEFramerFlushDeliversTrailingEvent(t *testing.T) {
	framer := &sseFramer{}
	if events := framer.push([]byte("data: [DONE]")); len(events) != 0 {
		t.Fatalf("trailing event framed early: %q", events)
	}
	events := framer.flush()
	if len(events) != 1 || string(events[0]) != "data: [DONE]" {
		t.Fatalf("flush returned %q", events)
	}
	if again := framer.flush(); len(again) != 0 {
		t.Fatalf("flush is not idempotent: %q", again)
	}
}

// The executor methods are the ones the host streams through, so the registration
// has to keep advertising them.
func TestExecutorStreamMethodIsTheOneWeStream(t *testing.T) {
	if pluginabi.MethodExecutorExecuteStream != "executor.execute_stream" {
		t.Fatalf("streaming method renamed: %s", pluginabi.MethodExecutorExecuteStream)
	}
}
