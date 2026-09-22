package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// executorWire is what the host actually sends to the executor methods.
//
// The embedded struct is the documented request; StreamID is the host's handle
// for this call's stream, and it is the only way a chunk pushed with
// host.stream.emit reaches the client. It was being discarded here, which left
// the batch response as the only way to answer -- and a batch response cannot be
// sent until the upstream finishes, so a long generation delivered nothing for as
// long as the model took. Whatever idle timeout sits in front of CPA (nginx, the
// panel's proxy, the client itself) then closed the connection first, and CPA
// maps the resulting context.Canceled to 499 with "context canceled".
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

// sseFramer turns a byte stream into events.
//
// The batch splitter cannot be reused here: an event can arrive across several
// reads, so the framer holds the tail of an incomplete event until its
// terminating blank line shows up. Splitting on read boundaries instead would
// emit half a payload, and the client would see truncated JSON.
type sseFramer struct {
	pending []byte
}

// push adds bytes and returns whatever complete events they finished.
func (f *sseFramer) push(chunk []byte) [][]byte {
	if len(chunk) == 0 {
		return nil
	}
	f.pending = append(f.pending, chunk...)
	// Normalize as it accumulates, so a CRLF split across two reads still frames.
	normalized := bytes.ReplaceAll(f.pending, []byte("\r\n"), []byte("\n"))
	last := bytes.LastIndex(normalized, []byte("\n\n"))
	if last < 0 {
		f.pending = normalized
		return nil
	}
	head := normalized[:last]
	f.pending = append([]byte(nil), normalized[last+2:]...)
	events := make([][]byte, 0, 4)
	for _, event := range bytes.Split(head, []byte("\n\n")) {
		if trimmed := bytes.TrimSpace(event); len(trimmed) > 0 {
			events = append(events, trimmed)
		}
	}
	return events
}

// flush returns a trailing event that never got its blank line, which is how a
// stream that ends abruptly still delivers its last payload.
func (f *sseFramer) flush() [][]byte {
	trimmed := bytes.TrimSpace(f.pending)
	f.pending = nil
	if len(trimmed) == 0 {
		return nil
	}
	return [][]byte{trimmed}
}

// executeStreaming proxies the upstream event stream to the client as it arrives.
//
// This is what keeps a long generation alive. Each upstream event is handed to
// the host the moment it is read, so bytes keep moving and no idle timeout in
// front of CPA has anything to time out on. It also gives the plugin the only
// cancellation signal it can get: when the reader goes away the host refuses the
// emit, and that refusal cancels the upstream request instead of finishing a
// response nobody will read.
func executeStreaming(streamID, target, origin, userAgent string, extra map[string]string, body []byte) ([]byte, error) {
	if streamID == "" {
		return nil, errors.New("no stream id for a streaming call")
	}
	ctx, cancel := context.WithTimeout(context.Background(), chatTimeout)
	defer cancel()

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

	// No client timeout: the stream is bounded by chatTimeout and by the emit
	// being refused once the reader is gone.
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

	reader := bufio.NewReaderSize(resp.Body, 64<<10)
	framer := &sseFramer{}
	emitted := 0
	emit := func(event []byte) bool {
		payload := append(append([]byte(nil), event...), '\n', '\n')
		if errEmit := hostStreamEmit(streamID, payload); errEmit != nil {
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
			for _, event := range framer.push(line) {
				if !emit(event) {
					return okEnvelope(streamResponse{Headers: sseHeaderSet()})
				}
			}
		}
		if errRead != nil {
			if !errors.Is(errRead, io.EOF) {
				warn("chat stream read stopped after %d events: %v", emitted, errRead)
			}
			break
		}
	}
	for _, event := range framer.flush() {
		if !emit(event) {
			return okEnvelope(streamResponse{Headers: sseHeaderSet()})
		}
	}

	hostStreamClose(streamID, "")
	return okEnvelope(streamResponse{Headers: sseHeaderSet()})
}

// sseHeaderSet is the response header for a streamed answer.
func sseHeaderSet() http.Header {
	return http.Header{"Content-Type": []string{"text/event-stream"}, "Cache-Control": []string{"no-cache"}}
}

// warn reports a failure the client cannot see. Discovery and streaming have no
// error channel of their own, so the process log is the only place a rejected
// upstream or a refused emit can be read from.
func warn(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[workbuddy] "+format+"\n", args...)
}
