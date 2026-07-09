// test-connect validates the executor.Client end-to-end against Cursor's real
// servers using the accessToken saved by Cursor IDE.
package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/mattn/go-sqlite3"

	"github.com/router-for-me/cursor-proto/auth"
	"github.com/router-for-me/cursor-proto/executor"
)

func main() {
	acc := loadAccountFromIDE()

	c := executor.NewClient(acc)
	fmt.Printf("✓ Client initialised\n")
	fmt.Printf("  session_id      = %s\n", acc.SessionID)
	fmt.Printf("  client_key      = %s\n", acc.ClientKey)
	fmt.Printf("  checksum_sess   = %s\n\n", acc.ChecksumSession)

	// 1. ListModels
	models, err := c.ListModels()
	if err != nil {
		log.Fatalf("ListModels: %v", err)
	}
	fmt.Printf("✓ AvailableModels → %d models\n", len(models.Models))
	for i, m := range models.Models {
		if i >= 5 {
			fmt.Printf("  ... plus %d more\n", len(models.Models)-5)
			break
		}
		fmt.Printf("  - %s (default_on=%v, supports_agent=%v)\n",
			m.GetName(), m.GetDefaultOn(), m.GetSupportsAgent())
	}

	// 2. GetDefaultModel
	def, err := c.GetDefaultModel()
	if err != nil {
		log.Fatalf("GetDefaultModel: %v", err)
	}
	fmt.Printf("\n✓ GetDefaultModel response (proto struct):\n  %+v\n", def)
}

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
