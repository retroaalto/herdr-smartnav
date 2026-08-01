// Package nav implements the smart direction-focus action invoked per keypress.
package nav

import (
	"fmt"
	"os"

	"herdr-smartnav/internal/herdr"
	"herdr-smartnav/internal/history"
	"herdr-smartnav/internal/layout"
)

// RunAction resolves the smart direction target and focuses it, with graceful
// fallback to herdr's default pane.focus_direction when smart nav doesn't apply.
func RunAction(direction string) error {
	paneID := os.Getenv("HERDR_PANE_ID")
	tabID := os.Getenv("HERDR_TAB_ID")
	workspaceID := os.Getenv("HERDR_WORKSPACE_ID")
	client := herdr.NewClient()

	// If the action runs without the injected pane id, fall back to default nav.
	if paneID == "" || tabID == "" || workspaceID == "" {
		return focusDirection(client, direction)
	}

	res, err := client.Call("pane.layout", map[string]string{"pane_id": paneID})
	if err != nil {
		return focusDirection(client, direction)
	}
	l, err := layout.ParseLayout(res)
	if err != nil {
		return focusDirection(client, direction)
	}

	// Zoomed tab: herdr's focus_direction handles the hidden layout correctly.
	if l.Zoomed {
		return focusDirection(client, direction)
	}

	tree := layout.BuildTree(l)
	if tree == nil {
		return focusDirection(client, direction)
	}

	// Resolve the sibling subtree to move into in the requested direction.
	// Moves only across splits on the requested axis where the current pane is
	// on the side opposite the movement (deepest such split wins).
	sibling := layout.MoveTarget(tree, paneID, direction)
	if sibling == nil {
		return focusDirection(client, direction)
	}

	if sibling.IsPane() {
		// Single-pane neighbor is exactly the default target.
		return focusPane(client, sibling.PaneID)
	}

	// Sibling is a split: look up the last active pane inside it.
	st, err := history.Load(workspaceID, tabID)
	if err != nil {
		return focusPane(client, layout.FirstLeafPane(sibling))
	}
	last := st.LastActivePerSplit[sibling.SplitID]
	if last != "" && layout.ContainsPane(sibling, last) {
		return focusPane(client, last)
	}
	return focusPane(client, layout.FirstLeafPane(sibling))
}

func focusPane(c *herdr.Client, paneID string) error {
	if paneID == "" {
		return fmt.Errorf("no target pane")
	}
	_, err := c.Call("pane.focus", map[string]string{"pane_id": paneID})
	return err
}

func focusDirection(c *herdr.Client, direction string) error {
	_, err := c.Call("pane.focus_direction", map[string]string{"direction": direction})
	return err
}
