package main

// Request shaping for the upstream channel check.
//
// WorkBuddy rejects a chat request whose first message is not a system prompt:
// the upstream answers HTTP 400 with business code 11-128, "request illegal",
// and a display tip that reads closer to "this call did not pass channel
// verification, it may not have been sent by the official client". A first
// message of role developer is rejected on the same check with a different
// message ("unapproved channel").
//
// That failure is about the shape of the request, not about the account: the
// credential, its balance and its token are all still valid, and the same
// account serves requests from a client that starts with a system prompt. So
// the fix belongs here, on the outgoing payload, and it must not disturb a
// request that already has the shape the upstream wants.
//
// Two message shapes are repaired:
//   - a developer first message becomes system, keeping its content verbatim;
//   - any other first message gets one system message in front of it, and every
//     existing message is left exactly as it was.
//
// A payload without messages is left alone: there is no conversation to repair,
// and inventing one would change what the caller asked for.

import "encoding/json"

// channelCheckSystemPrompt is the system message inserted when the request has
// none. It is deliberately plain: it only exists to satisfy the channel check,
// and it must not steer the model away from the caller's own instructions.
const channelCheckSystemPrompt = "You are a helpful assistant. Respond in the user's language, follow the user's instructions, and be direct and concise."

// messageRoles is the smallest shape needed to judge a payload: only the role of
// each message. Decoding into this instead of a full map keeps the common case
// (first message already system) cheap, because nothing has to be re-encoded.
type messageRoles struct {
	Messages []struct {
		Role string `json:"role"`
	} `json:"messages"`
}

// firstMessageRole reports the role of the first message, or "" when the payload
// carries no message or cannot be read.
func firstMessageRole(payload []byte) string {
	var envelope messageRoles
	if json.Unmarshal(payload, &envelope) != nil || len(envelope.Messages) == 0 {
		return ""
	}
	return envelope.Messages[0].Role
}

// ensureSystemFirst returns a payload whose first message satisfies the upstream
// channel check. It returns the input unchanged when nothing needs repairing, so
// a well-shaped request pays for nothing but the role read.
func ensureSystemFirst(payload []byte) []byte {
	switch firstMessageRole(payload) {
	case "":
		// No messages, or unreadable: leave the upstream to report it.
		return payload
	case "system":
		return payload
	}

	var document map[string]any
	if json.Unmarshal(payload, &document) != nil {
		return payload
	}
	messages, okMessages := document["messages"].([]any)
	if !okMessages || len(messages) == 0 {
		return payload
	}

	// A developer first message is the same rejection under another name, so it
	// is promoted in place rather than duplicated: the content stays verbatim and
	// the caller's instructions keep their position.
	if first, okFirst := messages[0].(map[string]any); okFirst {
		if role, _ := first["role"].(string); role == "developer" {
			first["role"] = "system"
			if rewritten, errMarshal := json.Marshal(document); errMarshal == nil {
				return rewritten
			}
			return payload
		}
	}

	withSystem := make([]any, 0, len(messages)+1)
	withSystem = append(withSystem, map[string]any{
		"role":    "system",
		"content": channelCheckSystemPrompt,
	})
	withSystem = append(withSystem, messages...)
	document["messages"] = withSystem
	rewritten, errMarshal := json.Marshal(document)
	if errMarshal != nil {
		return payload
	}
	return rewritten
}
