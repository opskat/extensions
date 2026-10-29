package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	opskat "github.com/opskat/opskat/pkg/extsdk"
)

// The page's console tabs, saved per asset in the extension's KV store so that
// reopening the asset brings them back. They are page state, not an operation on
// the cluster, so they are page-only tools — only the extension's own page calls
// them; AI and opsctl never see them through exec.
//
// The page names no asset in the arguments: the host scopes the call to the
// page's asset (ctx.Asset), and that is the key.

// savedConsole is one console tab: its number (its label) and its editor text.
type savedConsole struct {
	Number int    `json:"number"`
	Text   string `json:"text"`
}

// savedConsoles is console.load's result and, as JSON text, console.save's
// "consoles" argument: a tool parameter is a scalar or a list of strings, so the
// nested tabs travel as text. The stored form is unchanged.
type savedConsoles struct {
	Consoles []savedConsole `json:"consoles"`
}

func consoleKey(asset opskat.Asset) (string, error) {
	if asset.ID <= 0 {
		return "", errors.New("console tabs are saved per asset, and this call names none")
	}
	return "console:" + strconv.FormatInt(asset.ID, 10), nil
}

// decodeStrict decodes the tabs a page sends, refusing unknown fields and
// trailing data: the page is the only caller, so anything unexpected is a bug to
// surface, not something to ignore.
func decodeStrict(raw json.RawMessage, out any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("parse arguments: %w", err)
	}
	if dec.More() {
		return errors.New("parse arguments: trailing data")
	}
	return nil
}

type consoleLoadArgs struct{}

type consoleSaveArgs struct {
	Consoles string `json:"consoles" desc:"The console tabs as JSON text: [{\"number\":1,\"text\":\"...\"}]"`
}

func handleConsoleLoad(ctx *opskat.ToolContext, _ consoleLoadArgs) (any, error) {
	key, err := consoleKey(ctx.Asset)
	if err != nil {
		return nil, err
	}
	raw, err := opskat.KVGet(key)
	if err != nil {
		return nil, fmt.Errorf("read saved console tabs: %w", err)
	}
	out := savedConsoles{Consoles: []savedConsole{}}
	if len(raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode saved console tabs: %w", err)
	}
	return out, nil
}

func handleConsoleSave(ctx *opskat.ToolContext, args consoleSaveArgs) (any, error) {
	key, err := consoleKey(ctx.Asset)
	if err != nil {
		return nil, err
	}
	if args.Consoles == "" {
		return nil, errors.New("consoles is required")
	}
	var list *[]struct {
		Number *int    `json:"number"`
		Text   *string `json:"text"`
	}
	if err := decodeStrict(json.RawMessage(args.Consoles), &list); err != nil {
		return nil, err
	}
	if list == nil {
		return nil, errors.New("consoles must be a list")
	}
	state := savedConsoles{Consoles: make([]savedConsole, 0, len(*list))}
	seen := map[int]bool{}
	for i, c := range *list {
		if c.Number == nil || c.Text == nil {
			return nil, fmt.Errorf("consoles[%d]: number and text are required", i)
		}
		if *c.Number < 1 {
			return nil, fmt.Errorf("consoles[%d]: number must be at least 1, got %d", i, *c.Number)
		}
		if seen[*c.Number] {
			return nil, fmt.Errorf("consoles[%d]: number %d appears twice", i, *c.Number)
		}
		seen[*c.Number] = true
		state.Consoles = append(state.Consoles, savedConsole{Number: *c.Number, Text: *c.Text})
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	if err := opskat.KVSet(key, raw); err != nil {
		return nil, fmt.Errorf("save console tabs: %w", err)
	}
	return struct{}{}, nil
}
