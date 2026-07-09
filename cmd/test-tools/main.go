// test-tools posts an OpenAI Chat Completion request with tools[] to the
// running cursor-proxy and reports whether a tool_calls chunk arrives.
//
// Usage:
//
//	cursor-proxy &                              # start the proxy separately
//	go run ./cmd/test-tools -addr 127.0.0.1:8317 -model composer-2.5
//
// This binary does NOT spawn the proxy itself; it just drives it end-to-end.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8317", "proxy address (host:port)")
	model := flag.String("model", "composer-2.5", "model to send")
	stream := flag.Bool("stream", true, "use streaming mode")
	timeout := flag.Duration("timeout", 60*time.Second, "overall request timeout")
	flag.Parse()

	url := fmt.Sprintf("http://%s/v1/chat/completions", *addr)

	body := map[string]any{
		"model":  *model,
		"stream": *stream,
		"messages": []map[string]string{{
			"role":    "user",
			"content": "What is the weather in Paris? Use the get_weather tool.",
		}},
		"tools": []map[string]any{{
			"type": "function",
			"function": map[string]any{
				"name":        "get_weather",
				"description": "Fetch the current weather for a city.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"location": map[string]any{
							"type":        "string",
							"description": "City name",
						},
					},
					"required": []string{"location"},
				},
			},
		}},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		log.Fatalf("marshal: %v", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(buf))
	if err != nil {
		log.Fatalf("new request: %v", err)
	}
	req.Header.Set("content-type", "application/json")

	cli := &http.Client{Timeout: *timeout}
	resp, err := cli.Do(req)
	if err != nil {
		log.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		out, _ := io.ReadAll(resp.Body)
		log.Fatalf("http %d: %s", resp.StatusCode, string(out))
	}

	if !*stream {
		out, _ := io.ReadAll(resp.Body)
		fmt.Println(string(out))
		return
	}

	sawToolCall := false
	sawText := false
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}
		if strings.Contains(payload, `"tool_calls"`) {
			sawToolCall = true
			fmt.Printf("tool_calls: %s\n", payload)
		}
		if strings.Contains(payload, `"content":"`) {
			sawText = true
			fmt.Printf("text: %s\n", payload)
		}
	}
	if err := scanner.Err(); err != nil {
		log.Fatalf("scan: %v", err)
	}

	fmt.Printf("\nresult: sawToolCall=%v sawText=%v\n", sawToolCall, sawText)
	if !sawToolCall {
		fmt.Println("WARNING: no tool_calls chunk seen — the model may have replied with text instead of invoking the tool.")
		// Not a hard failure: the model is free to answer without calling the tool.
		os.Exit(0)
	}
}
