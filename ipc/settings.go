package ipc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func settingsPath() string {
	return filepath.Join(os.Getenv("HOME"), ".claude", "settings.json")
}

// PatchSettings adds SessionStart and Stop hooks for ccipc register/deregister.
// Safe to call multiple times — skips any hook already present.
func PatchSettings(ccipcBin string) error {
	path := settingsPath()

	var raw map[string]any
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read settings: %w", err)
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("parse settings: %w", err)
		}
	} else {
		raw = map[string]any{}
	}

	hooks, _ := raw["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}

	type hookEntry struct {
		event string
		hook  map[string]any
		check string // substring to detect existing entry
	}
	entries := []hookEntry{
		{
			event: "SessionStart",
			hook:  map[string]any{"type": "command", "command": ccipcBin + " register --auto"},
			check: ccipcBin + " register",
		},
		{
			// Deregister on final session end (non-async so it runs before process exits).
			event: "Stop",
			hook:  map[string]any{"type": "command", "command": ccipcBin + " deregister --auto"},
			check: ccipcBin + " deregister",
		},
		{
			// Wake hook: after every turn, check inbox and wake Claude if messages arrived.
			event: "Stop",
			hook: map[string]any{
				"type":        "command",
				"command":     ccipcBin + " _wake-hook",
				"async":       true,
				"asyncRewake": true,
			},
			check: ccipcBin + " _wake-hook",
		},
		{
			// Prompt hook: inject pending inbox messages at the start of every user turn.
			event: "UserPromptSubmit",
			hook:  map[string]any{"type": "command", "command": ccipcBin + " _prompt-hook"},
			check: ccipcBin + " _prompt-hook",
		},
	}

	changed := false
	for _, e := range entries {
		if hasHookCommand(hooks, e.event, e.check) {
			fmt.Printf("%s hook (%s) already present — skipping.\n", e.event, e.check)
			continue
		}
		wrapper := map[string]any{
			"hooks": []any{e.hook},
		}
		ss, _ := hooks[e.event].([]any)
		hooks[e.event] = append(ss, wrapper)
		changed = true
	}

	if !changed {
		return nil
	}

	raw["hooks"] = hooks
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0644)
}

// hasHookCommand returns true if any hook in the event list contains substr in its command.
func hasHookCommand(hooks map[string]any, event, substr string) bool {
	entries, ok := hooks[event].([]any)
	if !ok {
		return false
	}
	for _, entry := range entries {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		for _, h := range toSlice(m["hooks"]) {
			if hm, ok := h.(map[string]any); ok {
				if cmd, _ := hm["command"].(string); strings.Contains(cmd, substr) {
					return true
				}
			}
		}
	}
	return false
}

func toSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}
