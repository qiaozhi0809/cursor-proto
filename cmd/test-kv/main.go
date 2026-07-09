// test-kv streams a chat and dumps every KV blob's payload so we can see what
// Cursor is putting in there.
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
)

var printable = regexp.MustCompile(`[\x20-\x7e]{4,}`)

func main() {
	model := flag.String("model", "composer-2.5", "model")
	msg := flag.String("msg", "Say hi in exactly two words.", "user message")
	flag.Parse()

	acc := loadAccountFromIDE()
	c := executor.NewClient(acc)
	c.API3 = c.API2

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
			fmt.Printf("[%d] TRAILER\n", i)
			continue
		}
		if ev.Server == nil {
			fmt.Printf("[%d] RAW (%d bytes) %s\n", i, len(ev.Raw), hex.EncodeToString(ev.Raw[:min(40, len(ev.Raw))]))
			continue
		}
		if iu := ev.Server.GetInteractionUpdate(); iu != nil {
			fmt.Printf("[%d] IU  %s\n", i, iu.String()[:min(150, len(iu.String()))])
			continue
		}
		if kv := ev.Server.GetKvServerMessage(); kv != nil {
			if sb := kv.GetSetBlobArgs(); sb != nil {
				payload := sb.GetBlobData()
				strs := printable.FindAll(payload, -1)
				fmt.Printf("[%d] KV.set blob_id=%x  data=%d bytes\n", i, sb.GetBlobId()[:8], len(payload))
				for _, s := range strs {
					if len(s) > 8 {
						fmt.Printf("     text: %s\n", string(s))
					}
				}
			}
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func loadAccountFromIDE() *auth.Account {
	dbPath := os.Getenv("HOME") + "/Library/Application Support/Cursor/User/globalStorage/state.vscdb"
	db, err := sql.Open("sqlite3", "file:"+dbPath+"?mode=ro")
	if err != nil {
		log.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	var access string
	if err := db.QueryRow(`SELECT value FROM ItemTable WHERE key = 'cursorAuth/accessToken'`).Scan(&access); err != nil {
		log.Fatal(err)
	}
	machineID, _ := auth.GetMachineID()
	macID, _ := auth.GetMacMachineID()
	return &auth.Account{
		AccessToken:  access,
		MachineID:    machineID,
		MacMachineID: macID,
	}
}
