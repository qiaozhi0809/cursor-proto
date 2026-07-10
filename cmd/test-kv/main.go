// test-kv streams a chat and dumps every server message, so we can see where
// Cursor is putting the assistant text for a given model.
package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/router-for-me/cursor-proto/auth"
	"github.com/router-for-me/cursor-proto/executor"
)

var printable = regexp.MustCompile(`[\x20-\x7e]{4,}`)

func main() {
	model := flag.String("model", "claude-4.5-sonnet", "model")
	msg := flag.String("msg", "reply with exactly: HELLO-KV", "user message")
	acctFile := flag.String("account", "", "path to account JSON")
	flag.Parse()

	if *acctFile == "" {
		log.Fatal("-account is required")
	}
	acc, err := auth.LoadAccount(*acctFile)
	if err != nil {
		log.Fatalf("load account: %v", err)
	}
	c := executor.NewClient(acc)
	c.API3 = c.API2

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	events, err := c.RunChat(ctx, &executor.ChatRequest{
		Model: *model, UserMessage: *msg,
	})
	if err != nil {
		log.Fatal(err)
	}

	i := 0
	for ev := range events {
		i++
		if ev.Trailer {
			fmt.Printf("[%d] TRAILER  (%d bytes)  %s\n", i, len(ev.Raw), hex.EncodeToString(ev.Raw))
			continue
		}
		if ev.Server == nil {
			// Dump the full payload so we can spot mangled bytes.
			fmt.Printf("[%d] RAW  (%d bytes)  full=%s\n", i, len(ev.Raw), hex.EncodeToString(ev.Raw))
			tryDecodeShortClaudeDelta(i, ev.Raw)
			continue
		}
		describe(i, ev)
	}
}

// tryDecodeShortClaudeDelta looks for the Claude-family short delta shape:
//
//	0a 04 42 02 08 XX <utf-8 text bytes>
//
// If the bytes after the fixed prefix are valid UTF-8, print them as a probable
// text delta. Otherwise dump for later analysis.
func tryDecodeShortClaudeDelta(i int, raw []byte) {
	if len(raw) < 6 {
		return
	}
	if !(raw[0] == 0x0a && raw[1] == 0x04 && raw[2] == 0x42 && raw[3] == 0x02 && raw[4] == 0x08) {
		return
	}
	tail := raw[6:]
	if utf8.Valid(tail) && !containsNonPrintable(tail) {
		fmt.Printf("     ↑ probable text delta: <%s>\n", string(tail))
	} else {
		fmt.Printf("     ↑ short-shape but tail not printable utf-8\n")
	}
}

func containsNonPrintable(b []byte) bool {
	for _, c := range b {
		if c < 0x20 && c != '\n' && c != '\r' && c != '\t' {
			return true
		}
	}
	return false
}

func describe(i int, ev executor.ChatEvent) {
	m := ev.Server
	if iu := m.GetInteractionUpdate(); iu != nil {
		s := iu.String()
		if len(s) > 300 {
			s = s[:300] + "…"
		}
		fmt.Printf("[%d] InteractionUpdate  %s\n", i, s)
		return
	}
	if kv := m.GetKvServerMessage(); kv != nil {
		if sb := kv.GetSetBlobArgs(); sb != nil {
			payload := sb.GetBlobData()
			bid := sb.GetBlobId()
			preview := bid
			if len(preview) > 8 {
				preview = preview[:8]
			}
			fmt.Printf("[%d] KV.SetBlob blob_id=%x  data=%d bytes\n", i, preview, len(payload))
			fmt.Printf("     head: %s\n", hexPreview(payload, 80))
			for _, s := range printable.FindAll(payload, -1) {
				if len(s) > 8 {
					fmt.Printf("     text: %s\n", string(s))
				}
			}
			return
		}
		fmt.Printf("[%d] KV.other  %s\n", i, truncate(kv.String(), 200))
		return
	}
	if es := m.GetExecServerMessage(); es != nil {
		s := es.String()
		if len(s) > 400 {
			s = s[:400] + "…"
		}
		fmt.Printf("[%d] ExecServer  %s\n", i, s)
		return
	}
	fmt.Printf("[%d] Unknown  raw=%s\n", i, hexPreview(ev.Raw, 60))
}

func hexPreview(b []byte, n int) string {
	if len(b) < n {
		n = len(b)
	}
	return hex.EncodeToString(b[:n])
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
