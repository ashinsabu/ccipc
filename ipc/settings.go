package ipc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

	type hookSpec struct {
		event string
		cmd   string
	}
	specs := []hookSpec{
		{"SessionStart", ccipcBin + " register --auto"},
		{"Stop", ccipcBin + " deregister --auto"},
	}

	changed := false
	for _, spec := range specs {
		if hasHookCommand(hooks, spec.event, spec.cmd) {
			fmt.Printf("%s hook already present — skipping.\n", spec.event)
			continue
		}
		entry := map[string]any{
			"hooks": []any{
				map[string]any{
					"type":    "command",
					"command": spec.cmd,
				},
			},
		}
		ss, _ := hooks[spec.event].([]any)
		hooks[spec.event] = append(ss, entry)
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

func hasHookCommand(hooks map[string]any, event, cmd string) bool {
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
			if hm, ok := h.(map[string]any); ok && hm["command"] == cmd {
				return true
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
