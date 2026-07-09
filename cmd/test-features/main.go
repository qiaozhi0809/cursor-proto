// test-features exercises SystemPrompt / PureMode / AutoStopOnTurnEnd against
// the real Cursor backend so we can see what the server actually returns for
// each toggle.
package main

import (
	"context"
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/router-for-me/cursor-proto/auth"
	"github.com/router-for-me/cursor-proto/executor"
	"github.com/router-for-me/cursor-proto/translator"
)

var printable = regexp.MustCompile(`[\x20-\x7e]{4,}`)

func main() {
	sysPrompt := flag.String("system", "", "custom system prompt (empty=default)")
	pure := flag.Bool("pure", false, "PureMode (strip IDE env)")
	harness := flag.String("harness", "", "override harness (empty=default per pure)")
	autoStop := flag.Bool("stop", true, "AutoStopOnTurnEnd")
	msg := flag.String("msg", "say hi in one sentence", "user message")
	model := flag.String("model", "composer-2.5", "model")
	flag.Parse()

	acc := loadAccountFromIDE()
	c := executor.NewClient(acc)
	c.API3 = c.API2

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fmt.Printf("=== toggles: system=%q pure=%v stop=%v ===\n",
		*sysPrompt, *pure, *autoStop)

	events, err := c.RunChat(ctx, &executor.ChatRequest{
		Model:             *model,
		UserMessage:       *msg,
		SystemPrompt:      *sysPrompt,
		Harness:           *harness,
		PureMode:          *pure,
		AutoStopOnTurnEnd: *autoStop,
	})
	if err != nil {
		log.Fatal(err)
	}

	total, assistantText := 0, ""
	for ev := range events {
		total++
		if ev.Trailer {
			fmt.Printf("[%d] TRAILER  %s\n", total, string(ev.Raw))
			continue
		}
		if ev.Server == nil {
			continue
		}
		if iu := ev.Server.GetInteractionUpdate(); iu != nil {
			s := iu.String()
			if len(s) > 120 {
				s = s[:120] + "..."
			}
			fmt.Printf("[%d] IU  %s\n", total, s)
		}
		if blob := translator.FromKvBlob(ev.Server); blob != nil && blob.AssistantText != "" {
			assistantText = blob.AssistantText
			fmt.Printf("[%d] ASSISTANT_TEXT  %q\n", total, assistantText)
		}
		if kv := ev.Server.GetKvServerMessage(); kv != nil {
			if sb := kv.GetSetBlobArgs(); sb != nil {
				strs := printable.FindAll(sb.GetBlobData(), -1)
				if len(strs) > 0 && len(sb.GetBlobData()) < 500 {
					fmt.Printf("[%d] KV.small(%dB)\n", total, len(sb.GetBlobData()))
					for _, s := range strs {
						if len(s) > 8 {
							fmt.Printf("       %s\n", string(s))
						}
					}
				} else {
					fmt.Printf("[%d] KV.large(%dB) blob=%s\n", total,
						len(sb.GetBlobData()), hex.EncodeToString(sb.GetBlobId())[:16])
				}
			}
		}
	}
	fmt.Printf("\n=== summary ===\n")
	fmt.Printf("total_events: %d\n", total)
	fmt.Printf("assistant:    %q\n", assistantText)
}

func loadAccountFromIDE() *auth.Account {
	dbPath := os.Getenv("HOME") + "/Library/Application Support/Cursor/User/globalStorage/state.vscdb"
	db, err := sql.Open("sqlite3", "file:"+dbPath+"?mode=ro")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	var access string
	if err := db.QueryRow(`SELECT value FROM ItemTable WHERE key = 'cursorAuth/accessToken'`).Scan(&access); err != nil {
		log.Fatal(err)
	}
	machineID, _ := auth.GetMachineID()
	macID, _ := auth.GetMacMachineID()
	return &auth.Account{AccessToken: access, MachineID: machineID, MacMachineID: macID}
}
