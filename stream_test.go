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

// An event can arrive across several reads, so an incomplete payload is held
// until its braces balance rather than emitted as half an object.
func TestSSEAccumulatorHoldsIncompleteJSON(t *testing.T) {
	acc := &sseAccumulator{}
	if events := acc.push([]byte("data: {\"a\":")); len(events) != 0 {
		t.Fatalf("incomplete payload produced %d events: %q", len(events), events)
	}
	events := acc.push([]byte("1}\n\n"))
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if string(events[0]) != "data: {\"a\":1}\n\n" {
		t.Fatalf("unexpected event: %q", events[0])
	}
}

// A payload the gateway pretty-prints across lines, with a blank line inside it,
// is one event. Framing on the blank line is what split it before.
func TestSSEAccumulatorReassemblesMultiLinePayload(t *testing.T) {
	acc := &sseAccumulator{}
	events := acc.push([]byte("data: {\n\ndata: \"x\": [1,\ndata: 2]\n\ndata: }\n\n"))
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1: %q", len(events), events)
	}
	if string(events[0]) != "data: {\n\"x\": [1,\n2]\n}\n\n" {
		t.Fatalf("payload not reassembled: %q", events[0])
	}
}

// A brace inside a JSON string does not close the payload, and [DONE] ends the
// stream without waiting for more.
func TestSSEAccumulatorIsStringAwareAndStopsAtDone(t *testing.T) {
	acc := &sseAccumulator{}
	events := acc.push([]byte("data: {\"text\":\"a } b\",\"ok\":true}\n\ndata: [DONE]\n\n"))
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2: %q", len(events), events)
	}
	if string(events[0]) != "data: {\"text\":\"a } b\",\"ok\":true}\n\n" {
		t.Fatalf("string-aware depth failed: %q", events[0])
	}
	if string(events[1]) != "data: [DONE]\n\n" {
		t.Fatalf("unexpected terminator: %q", events[1])
	}
	if !acc.done {
		t.Fatal("[DONE] did not end the stream")
	}
	// Nothing after the terminator is forwarded, and the stream is not reopened.
	if extra := acc.push([]byte("data: late\n\n")); len(extra) != 0 {
		t.Fatalf("payload after [DONE] forwarded: %q", extra)
	}
}

// The one line that can arrive without its newline is the last one at EOF, and
// flush is what delivers it rather than dropping it.
func TestSSEAccumulatorFlushDeliversTrailingEvent(t *testing.T) {
	acc := &sseAccumulator{}
	if events := acc.push([]byte(`data: {"a":1}`)); len(events) != 0 {
		t.Fatalf("a line without its newline framed early: %q", events)
	}
	events := acc.flush()
	if len(events) != 1 || string(events[0]) != `data: {"a":1}`+"\n\n" {
		t.Fatalf("flush dropped the trailing event: %q", events)
	}
}

// A payload whose braces never balance is not an event, so it is withheld rather
// than handed over as half an object -- not at flush either.
func TestSSEAccumulatorWithholdsUnbalancedPayload(t *testing.T) {
	acc := &sseAccumulator{}
	if events := acc.push([]byte(`data: {"a":`)); len(events) != 0 {
		t.Fatalf("unbalanced payload emitted: %q", events)
	}
	if events := acc.flush(); len(events) != 0 {
		t.Fatalf("unbalanced payload emitted by flush: %q", events)
	}
}

// The event name is preserved when the gateway sends one, so a client that
// switches on it still sees it.
func TestSSEAccumulatorKeepsEventName(t *testing.T) {
	acc := &sseAccumulator{}
	events := acc.push([]byte("event: message\nid: 7\ndata: {\"a\":1}\n\n"))
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1: %q", len(events), events)
	}
	if string(events[0]) != "event: message\ndata: {\"a\":1}\n\n" {
		t.Fatalf("event name or payload wrong: %q", events[0])
	}
}

// The executor methods are the ones the host streams through, so the registration
// has to keep advertising them.
func TestExecutorStreamMethodIsTheOneWeStream(t *testing.T) {
	if pluginabi.MethodExecutorExecuteStream != "executor.execute_stream" {
		t.Fatalf("streaming method renamed: %s", pluginabi.MethodExecutorExecuteStream)
	}
}
