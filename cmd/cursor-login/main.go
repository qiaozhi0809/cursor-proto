// cursor-login runs the Cursor OAuth device flow, prompts the user to open a
// URL in their browser, then persists the resulting tokens + local machine
// identifiers to a JSON account file.
//
// Usage:
//
//	cursor-login -email you@example.com -out ~/.cursor-proto
//	cursor-login -no-browser          # print URL only, don't try to open
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"

	"github.com/router-for-me/cursor-proto/auth"
)

func main() {
	email := flag.String("email", "", "email to associate with the account (used for filename)")
	outDir := flag.String("out", defaultOutDir(), "directory to store account json")
	noBrowser := flag.Bool("no-browser", false, "just print the URL; do not try to open a browser")
	timeout := flag.Duration("timeout", 5*time.Minute, "how long to wait for the browser flow")
	interval := flag.Duration("interval", 3*time.Second, "poll interval")
	flag.Parse()

	if *email == "" {
		fmt.Fprintln(os.Stderr, "-email is required")
		os.Exit(2)
	}

	sess, err := auth.StartLogin()
	if err != nil {
		log.Fatalf("start login: %v", err)
	}

	fmt.Println("=====================================================")
	fmt.Println("Open this URL in your browser to authorize:")
	fmt.Println(sess.LoginURL)
	fmt.Println("=====================================================")

	if !*noBrowser {
		if err := openURL(sess.LoginURL); err != nil {
			fmt.Fprintf(os.Stderr, "(couldn't open browser automatically: %v)\n", err)
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	fmt.Printf("Polling every %s, timeout %s...\n", *interval, *timeout)
	result, err := sess.WaitForLogin(ctx, *interval, *timeout)
	if err != nil {
		log.Fatalf("wait: %v", err)
	}

	fmt.Println("✓ Login successful")

	acc, err := auth.NewAccountFromPoll(result, *email)
	if err != nil {
		log.Fatalf("build account: %v", err)
	}
	path, err := auth.SaveAccount(*outDir, acc)
	if err != nil {
		log.Fatalf("save: %v", err)
	}

	fmt.Println("Saved account to:")
	fmt.Println(" ", path)
	fmt.Println()
	fmt.Println("Details:")
	fmt.Printf("  email:            %s\n", acc.Email)
	fmt.Printf("  user_id:          %s\n", acc.UserID)
	fmt.Printf("  auth_type:        %s\n", acc.AuthType)
	fmt.Printf("  machine_id:       %s...\n", head(acc.MachineID, 16))
	fmt.Printf("  mac_machine_id:   %s...\n", head(acc.MacMachineID, 16))
	fmt.Printf("  checksum_sess:    %s...\n", head(acc.ChecksumSession, 24))
}

func defaultOutDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".cursor-proto")
	}
	return "."
}

func openURL(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
