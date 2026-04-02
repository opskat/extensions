package main

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/opskat/extensions/sdk/go/opskat"
)

func init() {
	opskat.RegisterTool("echo", handleEcho)
	opskat.RegisterAction("echo_stream", handleEchoStream)
	opskat.RegisterPolicy(checkPolicy)
}

func main() {
	opskat.Run()
}

func handleEcho(ctx *opskat.ToolContext) (any, error) {
	var args struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(ctx.Args, &args); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}

	// Increment a counter in KV storage to demonstrate host function usage
	counterBytes, _ := opskat.KVGet("echo_counter")
	counter := 0
	if len(counterBytes) > 0 {
		counter, _ = strconv.Atoi(string(counterBytes))
	}
	counter++
	opskat.KVSet("echo_counter", []byte(strconv.Itoa(counter)))

	opskat.Log("info", fmt.Sprintf("echo tool called: %s (count=%d)", args.Message, counter))

	return map[string]any{
		"echo":    args.Message,
		"counter": counter,
	}, nil
}

func handleEchoStream(ctx *opskat.ActionContext) (any, error) {
	var args struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(ctx.Args, &args); err != nil {
		return nil, fmt.Errorf("parse args: %w", err)
	}

	// Emit one event per character to demonstrate streaming
	for i, ch := range args.Message {
		ctx.Events.Send("echo", map[string]any{
			"char":  string(ch),
			"index": i,
		})
	}

	return map[string]string{"status": "done"}, nil
}

func checkPolicy(tool string, args json.RawMessage) (string, string) {
	return "echo", "*"
}
