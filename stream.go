package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const (
	// firstByteTimeout bounds how long the gateway may take to produce the FIRST
	// byte of a stream. A gateway that has not started talking by then is not
	// going to, and reporting that is better than holding the request open.
	firstByteTimeout = 60 * time.Second
	// streamCeiling is a safety net, not an idle timeout: it bounds a stream that
	// never ends at all, and is deliberately far longer than any generation.
	// Nothing fires between bytes, because an idle timeout is what kills a long
	// answer mid-thought.
	streamCeiling = 30 * time.Minute
)

// executorWire is what the host actually sends to the executor methods.
//
// The embedded struct is the documented request; StreamID is the host's handle
// for this call's stream, and it is the only way a chunk pushed with
// host.stream.emit reaches the client. It used to be discarded here, which left
// the batch response as the only way to answer -- and a batch response cannot be
// sent until the upstream finishes, so a long generation delivered nothing for as
// long as the model took. Whatever idle timeout sits in front of CPA then closed
// the connection, and CPA maps the resulting context.Canceled to 499.
type executorWire struct {
	pluginapi.ExecutorRequest
	StreamID       string `json:"stream_id,omitempty"`
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

// streamEmitRequest and streamCloseRequest mirror the host's rpcStreamEmitRequest
// and rpcStreamCloseRequest: a stream is addressed by id and nothing else.
type streamEmitRequest struct {
	StreamID string `json:"stream_id"`
	Payload  []byte `json:"payload,omitempty"`
	Error    string `json:"error,omitempty"`
}

type streamCloseRequest struct {
	StreamID string `json:"stream_id"`
	Error    string `json:"error,omitempty"`
}

func hostStreamEmit(streamID string, payload []byte) error {
	if streamID == "" || len(payload) == 0 {
		return nil
	}
	_, err := hostCall("host.stream.emit", streamEmitRequest{StreamID: streamID, Payload: payload})
	return err
}

// hostStreamClose ends the stream. An empty message means it finished; anything
// else is reported to the client as the stream's error.
func hostStreamClose(streamID, message string) {
	if streamID == "" {
		return
	}
	if _, err := hostCall("host.stream.close", streamCloseRequest{StreamID: streamID, Error: message}); err != nil {
		warn("stream close failed: %v", err)
	}
}

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

// executeStreaming proxies the upstream event stream to the client as it arrives.
//
// This is what keeps a long generation alive. Each upstream event is handed to
// the host the moment it is complete, so bytes keep moving and no idle timeout in
// front of CPA has anything to time out on. It also gives the plugin the only
// cancellation signal it can get: when the reader is gone the host refuses the
// emit, and that refusal cancels the upstream request instead of finishing a
// response nobody will read.
func executeStreaming(streamID, target, origin, userAgent string, extra map[string]string, body []byte) ([]byte, error) {
	if streamID == "" {
		return nil, errors.New("no stream id for a streaming call")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Only the first byte is timed. After that the stream runs until it ends, the
	// ceiling is reached, or the reader disappears -- an idle deadline in between
	// would cut off a long answer while it is still being written.
	var firstByteSeen atomic.Bool
	firstByteTimer := time.AfterFunc(firstByteTimeout, func() {
		if !firstByteSeen.Load() {
			warn("chat stream produced no first byte within %s (%s)", firstByteTimeout, target)
			cancel()
		}
	})
	defer firstByteTimer.Stop()
	ceilingTimer := time.AfterFunc(streamCeiling, func() {
		warn("chat stream reached the %s ceiling (%s)", streamCeiling, target)
		cancel()
	})
	defer ceilingTimer.Stop()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		hostStreamClose(streamID, err.Error())
		return errorEnvelope("upstream_error", err.Error(), http.StatusBadGateway), nil
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")
	req.Header.Set("User-Agent", userAgent)
	for key, value := range extra {
		req.Header.Set(key, value)
	}

	// No client timeout on the http.Client: the stream is bounded by the context
	// above, so a long generation is not cut off by a transport default.
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		warn("chat stream failed: %v (%s)", err, target)
		hostStreamClose(streamID, err.Error())
		return errorEnvelope("upstream_error", err.Error(), http.StatusBadGateway), nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		warn("chat stream rejected: status=%d url=%s body=%s", resp.StatusCode, target, truncate(string(payload), 600))
		hostStreamClose(streamID, string(payload))
		return errorEnvelope("upstream_error", string(payload), resp.StatusCode), nil
	}

	acc := &sseAccumulator{}
	reader := bufio.NewReaderSize(resp.Body, 64<<10)
	emitted := 0
	emit := func(event []byte) bool {
		if errEmit := hostStreamEmit(streamID, event); errEmit != nil {
			warn("stream emit refused after %d events (client gone?): %v", emitted, errEmit)
			cancel()
			return false
		}
		emitted++
		return true
	}

	for {
		line, errRead := reader.ReadBytes('\n')
		if len(line) > 0 {
			if !firstByteSeen.Swap(true) {
				firstByteTimer.Stop()
			}
			for _, event := range acc.push(line) {
				if !emit(event) {
					return okEnvelope(streamResponse{Headers: sseHeaderSet()})
				}
				if acc.done {
					hostStreamClose(streamID, "")
					return okEnvelope(streamResponse{Headers: sseHeaderSet()})
				}
			}
		}
		if errRead != nil {
			if !errors.Is(errRead, io.EOF) && ctx.Err() == nil {
				warn("chat stream read stopped after %d events: %v", emitted, errRead)
			}
			break
		}
	}
	for _, event := range acc.flush() {
		if !emit(event) {
			break
		}
	}

	// A cancelled context is a reader that went away or a timeout we already
	// logged; either way the client is told, and the upstream request is already
	// torn down with it.
	if errCtx := ctx.Err(); errCtx != nil {
		hostStreamClose(streamID, errCtx.Error())
		return okEnvelope(streamResponse{Headers: sseHeaderSet()})
	}
	hostStreamClose(streamID, "")
	return okEnvelope(streamResponse{Headers: sseHeaderSet()})
}

// sseHeaderSet is the response header for a streamed answer.
func sseHeaderSet() http.Header {
	return http.Header{"Content-Type": []string{"text/event-stream"}, "Cache-Control": []string{"no-cache"}}
}

// warn reports a failure the client cannot see. Streaming has no error channel of
// its own, so the process log is the only place a refused emit or a dead upstream
// can be read from.
func warn(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[workbuddy] "+format+"\n", args...)
}

// unusedJSON keeps the encoding/json import honest: the wire types are described
// by their json tags, and the envelope helpers live in main.go.
var _ = json.Marshal
