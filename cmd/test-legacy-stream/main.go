// test-legacy-stream probes Cursor's older ChatService streaming endpoints to
// find out whether any of them emits real per-token text_delta chunks (in
// contrast to agent.v1.AgentService/RunSSE, which only trickles sparse
// formatting fragments plus one terminal JSON blob).
//
// The probe hand-encodes StreamUnifiedChatWithToolsRequest per the schema
// captured in reference/cursor-2.3.41.proto (fields 527..596) because
// gen/cursor/cursor.pb.go only ships the agent.v1 half of the 3.10 schema.
package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/router-for-me/cursor-proto/auth"
	"github.com/router-for-me/cursor-proto/executor"
)

// Candidate endpoints we want to probe. All live under
// /aiserver.v1.ChatService/* on api2. Order matters — SSE first (task's
// primary target), then non-SSE fallbacks.
var candidates = []string{
	"aiserver.v1.ChatService/StreamUnifiedChatWithToolsSSE",
	"aiserver.v1.ChatService/StreamUnifiedChatWithTools",
	"aiserver.v1.ChatService/StreamUnifiedChatWithToolsPoll",
	"aiserver.v1.ChatService/StreamUnifiedChat",
	"aiserver.v1.ChatService/WarmStreamUnifiedChatWithTools",
}

func main() {
	only := flag.String("endpoint", "", "run only this endpoint suffix (e.g. StreamUnifiedChatWithToolsSSE)")
	model := flag.String("model", "claude-4.5-sonnet", "model to request")
	msg := flag.String("msg", "Write a 200-word essay about the invention of the printing press.", "user message")
	timeout := flag.Duration("timeout", 90*time.Second, "per-endpoint timeout")
	logPath := flag.String("log", "", "write timestamped frame log to this file")
	agentic := flag.Bool("agentic", false, "set isAgentic=true on the request (3.10 defaults to false for the pure chat endpoint)")
	flag.Parse()

	acc := loadAccountFromIDE()
	acc.FillSessionDefaults(time.Now())

	var logSink io.Writer = io.Discard
	if *logPath != "" {
		f, err := os.Create(*logPath)
		if err != nil {
			log.Fatalf("open log: %v", err)
		}
		defer f.Close()
		logSink = f
	}

	for _, path := range candidates {
		if *only != "" && !strings.HasSuffix(path, *only) {
			continue
		}
		fmt.Printf("\n========================================\n")
		fmt.Printf("Probing %s\n", path)
		fmt.Printf("========================================\n")
		fmt.Fprintf(logSink, "\n========================================\n")
		fmt.Fprintf(logSink, "Probing %s\n", path)
		fmt.Fprintf(logSink, "========================================\n")

		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		err := probe(ctx, acc, path, *model, *msg, *agentic, logSink)
		cancel()
		if err != nil {
			fmt.Printf("  RESULT: error: %v\n", err)
			fmt.Fprintf(logSink, "  RESULT: error: %v\n", err)
		}
	}
}

// probe POSTs a StreamUnifiedChatWithToolsRequest to the given endpoint and
// pretty-prints every frame it receives with a monotonic timestamp.
func probe(ctx context.Context, acc *auth.Account, path, model, msg string, agentic bool, logSink io.Writer) error {
	reqBody, convID := buildRequest(msg, model, agentic)
	fmt.Printf("  wire body: %d bytes, conversationId=%s\n", len(reqBody), convID)
	fmt.Fprintf(logSink, "  wire body: %d bytes, conversationId=%s\n", len(reqBody), convID)

	// Try both known wire framings the bidiClient used against different
	// endpoints. The SSE / connect+proto path uses a 5-byte Connect envelope;
	// the pure "application/proto" path uses a magic-byte + 4-byte length
	// header that Cursor's legacy chat decoder in reference/js-src/utils.js
	// consumes. We just try the two most-plausible content-types in order.
	// If both fail with the same non-200 status we'll surface the second body.
	framings := []framing{
		{"application/connect+proto", true},
		{"application/proto", false},
	}
	var lastErr error
	for _, f := range framings {
		fmt.Printf("\n-- attempt content-type=%s envelope=%v\n", f.contentType, f.envelope)
		fmt.Fprintf(logSink, "\n-- attempt content-type=%s envelope=%v\n", f.contentType, f.envelope)
		err := doOne(ctx, acc, path, reqBody, f, logSink)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isRetryable(err) {
			return err
		}
	}
	return lastErr
}

type framing struct {
	contentType string
	envelope    bool
}

func doOne(ctx context.Context, acc *auth.Account, path string, body []byte, f framing, logSink io.Writer) error {
	var wireBody []byte
	if f.envelope {
		wireBody = addConnectEnvelope(body, false)
	} else {
		wireBody = body
	}
	host := os.Getenv("PROBE_HOST")
	if host == "" {
		host = "api2.cursor.sh"
	}
	url := "https://" + host + "/" + path
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(wireBody))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", f.contentType)
	executor.ApplyCommonHeaders(req, acc, auth.GenerateRequestID())

	start := time.Now()
	cli := &http.Client{Timeout: 0}
	resp, err := cli.Do(req)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer resp.Body.Close()
	fmt.Printf("     +%s http %d (%s)\n", elapsed(start), resp.StatusCode, resp.Header.Get("content-type"))
	fmt.Fprintf(logSink, "     +%s http %d (%s)\n", elapsed(start), resp.StatusCode, resp.Header.Get("content-type"))
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		fmt.Printf("     body: %s\n", truncate(string(raw), 400))
		fmt.Fprintf(logSink, "     body: %s\n", string(raw))
		return fmt.Errorf("http %d", resp.StatusCode)
	}

	// Frame reader. We support two framings:
	//  * Connect envelope: [flags:1][length:4 BE] (Connect / SSE stream)
	//  * Cursor legacy:     [magic:1][length:4 BE] where magic ∈ {0,1,2,3}
	// The byte layouts are identical, so a single splitter works for both;
	// the difference is only how we interpret the leading flags byte later.
	return readFrames(resp.Body, start, logSink)
}

// readFrames pulls Connect-style frames off the stream and logs each one
// with a wall-clock delta from the request start.
func readFrames(body io.Reader, start time.Time, logSink io.Writer) error {
	buf := make([]byte, 0, 8192)
	tmp := make([]byte, 8192)
	frameNo := 0
	textDeltas := 0
	textBytes := 0
	otherFrames := 0
	for {
		n, err := body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			for {
				if len(buf) < 5 {
					break
				}
				flags := buf[0]
				length := binary.BigEndian.Uint32(buf[1:5])
				if uint32(len(buf)-5) < length {
					break
				}
				payload := append([]byte(nil), buf[5:5+length]...)
				buf = append(buf[:0], buf[5+int(length):]...)
				frameNo++
				kind, note, isTextDelta, textLen := classify(flags, payload)
				line := fmt.Sprintf("  [%03d] +%s flags=0x%02x len=%d %s %s\n",
					frameNo, elapsed(start), flags, length, kind, note)
				fmt.Print(line)
				fmt.Fprint(logSink, line)
				if isTextDelta {
					textDeltas++
					textBytes += textLen
				} else {
					otherFrames++
				}
				// Trailer (flags & 0x80). Stream ends here.
				if flags&0x80 != 0 {
					summary := fmt.Sprintf("  SUMMARY textDeltas=%d textBytes=%d otherFrames=%d totalTime=%s\n",
						textDeltas, textBytes, otherFrames, elapsed(start))
					fmt.Print(summary)
					fmt.Fprint(logSink, summary)
					return nil
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				summary := fmt.Sprintf("  SUMMARY (EOF) textDeltas=%d textBytes=%d otherFrames=%d totalTime=%s\n",
					textDeltas, textBytes, otherFrames, elapsed(start))
				fmt.Print(summary)
				fmt.Fprint(logSink, summary)
				return nil
			}
			return err
		}
	}
}

// classify inspects a frame payload and returns a human-readable label. Uses
// a manual proto scan targeted at the StreamUnifiedChatWithToolsResponse
// shape (see reference/cursor-2.3.41.proto lines 602..642).
func classify(flags byte, payload []byte) (kind, note string, isTextDelta bool, textLen int) {
	if flags&0x80 != 0 {
		return "TRAILER", string(payload), false, 0
	}
	// Legacy chat stream: some captures showed the frame body is gzipped.
	// The connect+proto path never sets flags=1 by itself (server compresses
	// with a different mechanism), but the legacy framing may. Try to
	// gunzip transparently.
	body := payload
	if flags&0x01 != 0 || (len(payload) >= 2 && payload[0] == 0x1f && payload[1] == 0x8b) {
		if gz, err := gzip.NewReader(bytes.NewReader(payload)); err == nil {
			if raw, err := io.ReadAll(gz); err == nil {
				body = raw
			}
			gz.Close()
		}
	}
	fields := scanFields(body)
	// Heuristics on StreamUnifiedChatWithToolsResponse shape.
	if raw := getStringField(fields, 1); raw != "" {
		return "DATA", fmt.Sprintf("text[legacy]=%q", truncate(raw, 120)), true, len(raw)
	}
	if msgBytes := getBytesField(fields, 2); len(msgBytes) > 0 {
		msgFields := scanFields(msgBytes)
		if content := getStringField(msgFields, 1); content != "" {
			return "DATA", fmt.Sprintf("message.content=%q", truncate(content, 120)), true, len(content)
		}
		if thinkBytes := getBytesField(msgFields, 25); len(thinkBytes) > 0 {
			thinkFields := scanFields(thinkBytes)
			if content := getStringField(thinkFields, 1); content != "" {
				return "DATA", fmt.Sprintf("thinking=%q", truncate(content, 120)), false, 0
			}
		}
	}
	if thinkBytes := getBytesField(fields, 25); len(thinkBytes) > 0 {
		thinkFields := scanFields(thinkBytes)
		if content := getStringField(thinkFields, 1); content != "" {
			return "DATA", fmt.Sprintf("thinking=%q", truncate(content, 120)), false, 0
		}
	}
	if tc := getBytesField(fields, 36); len(tc) > 0 {
		return "DATA", fmt.Sprintf("tool_call_v2 (%d bytes)", len(tc)), false, 0
	}
	if bubble := getStringField(fields, 22); bubble != "" {
		return "DATA", fmt.Sprintf("server_bubble_id=%s", bubble), false, 0
	}
	if len(body) == 0 {
		return "DATA", "empty", false, 0
	}
	// If it looks like JSON (Cursor returns Connect error frames as JSON with
	// magic flags=0x02), surface the full text.
	if len(body) > 0 && body[0] == '{' {
		return "DATA", fmt.Sprintf("json=%s", string(body)), false, 0
	}
	return "DATA", fmt.Sprintf("unknown fields=%v raw=%s", fieldNums(fields), hexPreview(body, 200)), false, 0
}

// ---- request builder ----

func buildRequest(msg, model string, agentic bool) (body []byte, convID string) {
	convID = auth.GenerateSessionID()
	messageID := auth.GenerateRequestID()

	// Build inner Message:
	//   1: content (string)
	//   2: role (int32) 1=user 2=assistant
	//   13: messageId (string)
	//   47: chatModeEnum 1=ask 2=agent 3=edit
	var innerMsg []byte
	innerMsg = appendStringField(innerMsg, 1, msg)
	innerMsg = appendInt64Field(innerMsg, 2, 1) // role=user
	innerMsg = appendStringField(innerMsg, 13, messageID)
	if agentic {
		innerMsg = appendInt64Field(innerMsg, 47, 2) // agent mode
	} else {
		innerMsg = appendInt64Field(innerMsg, 47, 1) // ask mode
	}

	// Build Model: { name=1, empty=4 }
	var modelMsg []byte
	modelMsg = appendStringField(modelMsg, 1, model)

	// Instruction: { instruction=1 }
	var instrMsg []byte
	instrMsg = appendStringField(instrMsg, 1, "")

	// Metadata: { os=1, arch=2, version=3, path=4, timestamp=5 }
	var metaMsg []byte
	metaMsg = appendStringField(metaMsg, 1, "darwin")
	metaMsg = appendStringField(metaMsg, 2, "arm64")
	metaMsg = appendStringField(metaMsg, 3, "23.6.0")
	metaMsg = appendStringField(metaMsg, 4, "/tmp/probe")
	metaMsg = appendStringField(metaMsg, 5, time.Now().UTC().Format(time.RFC3339))

	// MessageId: { messageId=1, summaryId=2, role=3 }
	var msgIDMsg []byte
	msgIDMsg = appendStringField(msgIDMsg, 1, messageID)
	msgIDMsg = appendInt64Field(msgIDMsg, 3, 1)

	// Request layout (field numbers per reference/cursor-2.3.41.proto):
	//  1  repeated Message messages
	//  2  int32 unknown2 (1)
	//  3  Instruction instruction
	//  4  int32 unknown4 (1)
	//  5  Model model
	//  13 int32 unknown13 (1)
	//  19 int32 unknown19 (1)
	//  22 int32 unknown22 (1)
	//  23 string conversationId
	//  26 Metadata metadata
	//  27 bool  isAgentic
	//  30 repeated MessageId messageIds
	//  35 int32 largeContext (1)
	//  38 int32 unknown38 (0)
	var request []byte
	request = appendMessageField(request, 1, innerMsg)
	request = appendInt64Field(request, 2, 1)
	request = appendMessageField(request, 3, instrMsg)
	request = appendInt64Field(request, 4, 1)
	request = appendMessageField(request, 5, modelMsg)
	if os.Getenv("PROBE_STRIP_LEGACY") == "" {
		request = appendInt64Field(request, 13, 1)
		request = appendInt64Field(request, 19, 1)
		request = appendInt64Field(request, 22, 1)
	}
	request = appendStringField(request, 23, convID)
	request = appendMessageField(request, 26, metaMsg)
	if agentic {
		request = appendInt64Field(request, 27, 1)
	}
	request = appendMessageField(request, 30, msgIDMsg)
	request = appendInt64Field(request, 35, 1)
	request = appendInt64Field(request, 38, 0)

	// Outer StreamUnifiedChatWithToolsRequest: { request=1 }
	body = appendMessageField(nil, 1, request)
	return body, convID
}

// ---- proto wire helpers (duplicated from executor/chat.go so this probe
// stays self-contained; keep in sync if the layout changes) ----

func appendVarint(buf []byte, v uint64) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v)|0x80)
		v >>= 7
	}
	return append(buf, byte(v))
}

func appendTag(buf []byte, field int, wire int) []byte {
	return appendVarint(buf, uint64(field)<<3|uint64(wire))
}

func appendStringField(buf []byte, field int, s string) []byte {
	buf = appendTag(buf, field, 2)
	buf = appendVarint(buf, uint64(len(s)))
	return append(buf, s...)
}

func appendMessageField(buf []byte, field int, msg []byte) []byte {
	buf = appendTag(buf, field, 2)
	buf = appendVarint(buf, uint64(len(msg)))
	return append(buf, msg...)
}

func appendInt64Field(buf []byte, field int, v int64) []byte {
	buf = appendTag(buf, field, 0)
	return appendVarint(buf, uint64(v))
}

func addConnectEnvelope(data []byte, compressed bool) []byte {
	frame := make([]byte, 5+len(data))
	if compressed {
		frame[0] = 1
	}
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(data)))
	copy(frame[5:], data)
	return frame
}

// ---- proto scanner (varint + length-delimited only) ----

type scannedField struct {
	wire int
	val  []byte
	num  int
	// varint value pre-decoded for wire type 0.
	varint uint64
}

func scanFields(buf []byte) map[int][]scannedField {
	out := map[int][]scannedField{}
	for len(buf) > 0 {
		tag, n := readVarint(buf)
		if n <= 0 {
			return out
		}
		buf = buf[n:]
		field := int(tag >> 3)
		wire := int(tag & 7)
		switch wire {
		case 0:
			v, m := readVarint(buf)
			if m <= 0 {
				return out
			}
			buf = buf[m:]
			out[field] = append(out[field], scannedField{wire: wire, num: field, varint: v})
		case 2:
			length, m := readVarint(buf)
			if m <= 0 || int(length) > len(buf)-m {
				return out
			}
			buf = buf[m:]
			out[field] = append(out[field], scannedField{wire: wire, num: field, val: append([]byte(nil), buf[:length]...)})
			buf = buf[length:]
		case 1:
			if len(buf) < 8 {
				return out
			}
			buf = buf[8:]
		case 5:
			if len(buf) < 4 {
				return out
			}
			buf = buf[4:]
		default:
			return out
		}
	}
	return out
}

func readVarint(buf []byte) (uint64, int) {
	var v uint64
	var shift uint
	for i := 0; i < len(buf); i++ {
		b := buf[i]
		v |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return v, i + 1
		}
		shift += 7
		if shift > 63 {
			return 0, -1
		}
	}
	return 0, -1
}

func getStringField(fields map[int][]scannedField, num int) string {
	if fs, ok := fields[num]; ok && len(fs) > 0 && fs[0].wire == 2 {
		return string(fs[0].val)
	}
	return ""
}

func getBytesField(fields map[int][]scannedField, num int) []byte {
	if fs, ok := fields[num]; ok && len(fs) > 0 && fs[0].wire == 2 {
		return fs[0].val
	}
	return nil
}

func fieldNums(fields map[int][]scannedField) []int {
	out := make([]int, 0, len(fields))
	for k := range fields {
		out = append(out, k)
	}
	return out
}

// ---- misc ----

func elapsed(start time.Time) string {
	d := time.Since(start)
	return fmt.Sprintf("%7.3fs", d.Seconds())
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func hexPreview(b []byte, n int) string {
	if len(b) < n {
		return hex.EncodeToString(b)
	}
	return hex.EncodeToString(b[:n]) + "..."
}

func isRetryable(err error) bool {
	return strings.Contains(err.Error(), "http 4") || strings.Contains(err.Error(), "http 5")
}

// ---- account loader (macOS default IDE path) ----

func loadAccountFromIDE() *auth.Account {
	dbPath := os.Getenv("HOME") + "/Library/Application Support/Cursor/User/globalStorage/state.vscdb"
	db, err := sql.Open("sqlite3", "file:"+dbPath+"?mode=ro")
	if err != nil {
		log.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	var access, email string
	if err := db.QueryRow(`SELECT value FROM ItemTable WHERE key = 'cursorAuth/accessToken'`).Scan(&access); err != nil {
		log.Fatalf("no accessToken: %v", err)
	}
	_ = db.QueryRow(`SELECT value FROM ItemTable WHERE key = 'cursorAuth/cachedEmail'`).Scan(&email)
	machineID, _ := auth.GetMachineID()
	macID, _ := auth.GetMacMachineID()
	return &auth.Account{
		Email:        email,
		AccessToken:  access,
		MachineID:    machineID,
		MacMachineID: macID,
	}
}

// silence unused-import complaints while iterating.
var _ = json.Marshal
