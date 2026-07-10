package translator

import (
	"encoding/json"
	"strings"

	cursorpb "github.com/router-for-me/cursor-proto/gen/cursor"
)

// BlobEvent is derived from AgentV1_KvServerMessage.SetBlobArgs. Cursor
// commits the model's final assistant message to KV blobs alongside internal
// reasoning; the outermost JSON-shaped blob with role="assistant" is what
// the user should see.
type BlobEvent struct {
	// AssistantText is the final assistant message text, or "" if the blob
	// did not contain one.
	AssistantText string
	// ThoughtText is the model's internal reasoning shown when the blob is a
	// bare short string (Cursor sometimes leaks these; may be discarded).
	ThoughtText string
}

// FromKvBlob attempts to extract an AssistantText / ThoughtText out of a
// KV SetBlobArgs payload. Returns nil if the blob is not text-shaped.
//
// Cursor's assistant-message blob has this shape (from live capture):
//
//	{
//	  "role": "assistant",
//	  "content": [
//	    { "type": "redacted-reasoning", "data": "..." },
//	    { "type": "text", "text": "Hello! How can I help you today?" }
//	  ],
//	  "id": "1"
//	}
//
// Other blobs are internal (system prompt, reasoning, arbitrary text). We
// only surface the assistant-role one.
func FromKvBlob(m *cursorpb.AgentV1_AgentServerMessage) *BlobEvent {
	if m == nil {
		return nil
	}
	kv := m.GetKvServerMessage()
	if kv == nil {
		return nil
	}
	sb := kv.GetSetBlobArgs()
	if sb == nil {
		return nil
	}
	data := sb.GetBlobData()
	if len(data) == 0 {
		return nil
	}

	// Try JSON parse first (this catches the assistant message blob).
	if txt := extractAssistantTextFromJSON(data); txt != "" {
		return &BlobEvent{AssistantText: txt}
	}
	// Claude family emits a compact protobuf-encoded blob for the final
	// assistant text: `0a XX 0a YY <text bytes>` (two nested LEN wrappers).
	// Try to peel those and treat the innermost bytes as text.
	if txt := extractAssistantTextFromProto(data); txt != "" {
		return &BlobEvent{AssistantText: txt}
	}
	return nil
}

// extractAssistantTextFromProto peels the two-level LEN wrapper Cursor uses
// for compact Claude assistant blobs and returns the inner text if it is
// clean UTF-8 without control bytes.
func extractAssistantTextFromProto(data []byte) string {
	// Outer: 0a <varint len> <inner>
	i := 0
	if i >= len(data) || data[i] != 0x0a {
		return ""
	}
	i++
	outerLen, n := readVarint(data[i:])
	if n <= 0 {
		return ""
	}
	i += n
	if i+int(outerLen) > len(data) {
		return ""
	}
	inner := data[i : i+int(outerLen)]
	// Inner: 0a <varint len> <text>
	if len(inner) < 2 || inner[0] != 0x0a {
		return ""
	}
	textLen, n2 := readVarint(inner[1:])
	if n2 <= 0 {
		return ""
	}
	off := 1 + n2
	if off+int(textLen) > len(inner) {
		return ""
	}
	text := inner[off : off+int(textLen)]
	// Reject non-textual bytes.
	for _, c := range text {
		if c < 0x20 && c != '\n' && c != '\r' && c != '\t' {
			return ""
		}
	}
	if !utf8Valid(text) {
		return ""
	}
	return string(text)
}

func readVarint(b []byte) (val uint64, n int) {
	var x uint64
	var s uint
	for i, c := range b {
		if i >= 10 {
			return 0, 0
		}
		if c < 0x80 {
			return x | uint64(c)<<s, i + 1
		}
		x |= uint64(c&0x7f) << s
		s += 7
	}
	return 0, 0
}

func utf8Valid(b []byte) bool {
	for i := 0; i < len(b); {
		c := b[i]
		if c < 0x80 {
			i++
			continue
		}
		var need int
		switch {
		case c&0xe0 == 0xc0:
			need = 2
		case c&0xf0 == 0xe0:
			need = 3
		case c&0xf8 == 0xf0:
			need = 4
		default:
			return false
		}
		if i+need > len(b) {
			return false
		}
		for j := 1; j < need; j++ {
			if b[i+j]&0xc0 != 0x80 {
				return false
			}
		}
		i += need
	}
	return true
}

// extractAssistantTextFromJSON parses `data` as an AI-SDK-style assistant
// message and returns the concatenated `type: text` content.
func extractAssistantTextFromJSON(data []byte) string {
	// Blobs may have leading garbage bytes; find the first '{' and try from there.
	start := -1
	for i, b := range data {
		if b == '{' {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	var obj struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data[start:], &obj); err != nil {
		return ""
	}
	if !strings.EqualFold(obj.Role, "assistant") {
		return ""
	}
	var sb strings.Builder
	for _, c := range obj.Content {
		if c.Type == "text" && c.Text != "" {
			sb.WriteString(c.Text)
		}
	}
	return sb.String()
}
