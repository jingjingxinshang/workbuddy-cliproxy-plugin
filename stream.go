package main

// stream.go owns the answer path: how an upstream event stream is turned into
// chunks for the client, and nothing else.
//
// It used to also emit chunks to the host as they arrived. That is removed: it put
// a host callback on the request path for every streaming call, with no timeout on
// the call itself, so a host that did not answer promptly left the request waiting
// until the caller gave up -- which is what every request failing looked like. The
// host now receives the finished chunks in the response, which is the path that
// worked before.

import (
	"bytes"
	"fmt"
	"os"
	"strings"
)

// sseAccumulator turns a byte stream into complete SSE events.
//
// It deliberately does NOT frame on blank lines. That is what the specification
// says, and it is what this code did, but it assumes one event is one line of
// data and the gateway does not always send that: a payload can be pretty-printed
// across several lines, and a blank line can appear inside one. Splitting on the
// delimiter then hands the client half a JSON object -- which is what "the tool
// call disappeared and the stream just stopped" looks like from the other side.
//
// A payload is therefore accumulated and released once its braces balance, with
// string and escape awareness so a "}" inside a JSON string cannot close it early.
// The same rule was arrived at independently by the workbuddy-anywhere client,
// which documents the same symptom.
type sseAccumulator struct {
	pending   []byte // bytes of an incomplete trailing line
	payload   []byte // accumulated data payload of the current event
	hasData   bool
	eventName string
	depth     int
	inString  bool
	escaped   bool
	done      bool
}

// push adds bytes and returns the events they completed, formatted for the client.
func (a *sseAccumulator) push(chunk []byte) [][]byte {
	if a.done {
		return nil
	}
	a.pending = append(a.pending, chunk...)
	var events [][]byte
	for {
		index := bytes.IndexByte(a.pending, '\n')
		if index < 0 {
			break
		}
		line := a.pending[:index]
		a.pending = append([]byte(nil), a.pending[index+1:]...)
		if event := a.line(strings.TrimRight(string(line), "\r")); event != nil {
			events = append(events, event)
		}
	}
	return events
}

// flush releases a payload that was complete but never followed by anything else,
// which is how a stream that stops abruptly still delivers its last event.
func (a *sseAccumulator) flush() [][]byte {
	if a.done {
		return nil
	}
	line := strings.TrimRight(string(a.pending), "\r")
	a.pending = nil
	if event := a.line(line); event != nil {
		return [][]byte{event}
	}
	if event := a.emit(); event != nil {
		return [][]byte{event}
	}
	return nil
}

// line consumes one physical line and returns a formatted event when it completes
// one.
func (a *sseAccumulator) line(raw string) []byte {
	trimmed := strings.TrimSpace(raw)
	// Blank lines and comments do not end a payload here: both can appear inside
	// one, and treating them as delimiters is the bug this type exists to avoid.
	if trimmed == "" || strings.HasPrefix(trimmed, ":") {
		return nil
	}
	if !a.hasData {
		if name, ok := strings.CutPrefix(trimmed, "event:"); ok {
			a.eventName = strings.TrimSpace(name)
			return nil
		}
		// id/retry fields carry no payload; they are not forwarded because the
		// client is being handed normalized events, not the gateway's framing.
		if strings.HasPrefix(trimmed, "id:") || strings.HasPrefix(trimmed, "retry:") {
			return nil
		}
	}
	if strings.HasPrefix(trimmed, "data:") {
		// Proxies layer the prefix, so strip every one of them.
		for strings.HasPrefix(trimmed, "data:") {
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
		}
		if !a.hasData {
			a.hasData = true
		} else {
			a.payload = append(a.payload, '\n')
		}
		a.payload = append(a.payload, trimmed...)
		a.scanBraceDepth(trimmed)
		return a.emit()
	}
	if a.hasData {
		// A continuation fragment of a payload that spans lines.
		a.payload = append(a.payload, '\n')
		a.payload = append(a.payload, trimmed...)
		a.scanBraceDepth(trimmed)
		return a.emit()
	}
	return nil
}

// scanBraceDepth tracks brace balance in text, ignoring braces inside strings.
func (a *sseAccumulator) scanBraceDepth(text string) {
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if a.escaped {
			a.escaped = false
			continue
		}
		switch ch {
		case '\\':
			if a.inString {
				a.escaped = true
			}
		case '"':
			a.inString = !a.inString
		case '{':
			if !a.inString {
				a.depth++
			}
		case '}':
			if !a.inString {
				a.depth--
			}
		}
	}
}

// emit returns the formatted event when the accumulated payload is complete.
func (a *sseAccumulator) emit() []byte {
	if !a.hasData {
		return nil
	}
	payload := strings.TrimSpace(string(a.payload))
	if payload == "" {
		return nil
	}
	// A payload with no braces (a plain token, or [DONE]) is complete as soon as
	// it arrives; one with braces waits until they balance.
	if a.depth > 0 {
		return nil
	}
	eventName := a.eventName
	done := payload == "[DONE]"
	a.payload = nil
	a.hasData = false
	a.eventName = ""
	a.depth = 0
	a.inString = false
	a.escaped = false
	if done {
		a.done = true
	}
	var out bytes.Buffer
	if eventName != "" {
		out.WriteString("event: ")
		out.WriteString(eventName)
		out.WriteByte('\n')
	}
	out.WriteString("data: ")
	out.WriteString(payload)
	out.WriteString("\n\n")
	return out.Bytes()
}

func warn(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[workbuddy] "+format+"\n", args...)
}
