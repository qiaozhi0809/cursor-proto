// test-sniff prints every KV blob's first 100 bytes so we can see exactly what
// the assistant terminal blob looks like on the wire.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/router-for-me/cursor-proto/auth"
	"github.com/router-for-me/cursor-proto/executor"
)

func main() {
	acc := loadAcc()
	c := executor.NewClient(acc)
	c.API3 = c.API2

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	events, err := c.RunChat(ctx, &executor.ChatRequest{
		Model:             "composer-2.5",
		UserMessage:       "Say hi in one word",
		AutoStopOnTurnEnd: false, // capture everything
	})
	if err != nil {
		log.Fatal(err)
	}

	i := 0
	for ev := range events {
		i++
		if ev.Server == nil {
			continue
		}
		kv := ev.Server.GetKvServerMessage()
		if kv == nil {
			continue
		}
		sb := kv.GetSetBlobArgs()
		if sb == nil {
			continue
		}
		data := sb.GetBlobData()
		// Look for JSON with role assistant
		hasRoleAsst := strings.Contains(string(data), `"role":"assistant"`)
		hasRoleAsstSp := strings.Contains(string(data), `"role": "assistant"`)
		// First 200 printable
		s := string(data)
		if len(s) > 200 {
			s = s[:200]
		}
		fmt.Printf("[%d] size=%d role_no_sp=%v role_sp=%v\n     head=%q\n",
			i, len(data), hasRoleAsst, hasRoleAsstSp, s)
	}
}

func loadAcc() *auth.Account {
	dbPath := os.Getenv("HOME") + "/Library/Application Support/Cursor/User/globalStorage/state.vscdb"
	db, err := sql.Open("sqlite3", "file:"+dbPath+"?mode=ro")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	var t string
	db.QueryRow(`SELECT value FROM ItemTable WHERE key = 'cursorAuth/accessToken'`).Scan(&t)
	m, _ := auth.GetMachineID()
	mm, _ := auth.GetMacMachineID()
	return &auth.Account{AccessToken: t, MachineID: m, MacMachineID: mm}
}
