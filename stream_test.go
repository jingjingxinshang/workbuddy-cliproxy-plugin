package main

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

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
