// Package history tracks the last-active pane per split per tab and persists it
// atomically per tab. It is the daemon side of herdr-smartnav.
package history

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// TabState is the persisted smart-nav state for one tab.
type TabState struct {
	WorkspaceID        string            `json:"workspace_id"`
	TabID              string            `json:"tab_id"`
	LastActivePerSplit map[string]string `json:"last_active_per_split"`
	LastFocusedPane    string            `json:"last_focused_pane"`
}

// stateDir resolves the plugin state directory.
func stateDir() string {
	if d := os.Getenv("HERDR_PLUGIN_STATE_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("HERDR_PLUGIN_CONFIG_DIR"); d != "" {
		return d
	}
	if home := os.Getenv("HOME"); home != "" {
		return filepath.Join(home, ".local", "share", "herdr", "smartnav")
	}
	return filepath.Join(os.TempDir(), "herdr-smartnav")
}

func statePath(workspaceID, tabID string) string {
	return filepath.Join(stateDir(), "smart."+workspaceID+"."+tabID+".json")
}

// Load reads a tab's state. Returns a zeroed, non-nil state if no file exists.
func Load(workspaceID, tabID string) (*TabState, error) {
	p := statePath(workspaceID, tabID)
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &TabState{WorkspaceID: workspaceID, TabID: tabID, LastActivePerSplit: map[string]string{}}, nil
		}
		return nil, err
	}
	var s TabState
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.LastActivePerSplit == nil {
		s.LastActivePerSplit = map[string]string{}
	}
	return &s, nil
}

// Save writes the state atomically (temp file + rename).
func Save(s *TabState) error {
	dir := stateDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	p := statePath(s.WorkspaceID, s.TabID)
	tmp, err := os.CreateTemp(dir, ".smart.*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// Delete removes a tab's state file (e.g. when a tab closes).
func Delete(workspaceID, tabID string) error {
	err := os.Remove(statePath(workspaceID, tabID))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
