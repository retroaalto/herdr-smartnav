package history

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"herdr-smartnav/internal/herdr"
	"herdr-smartnav/internal/layout"
)

type tabKey struct {
	WorkspaceID string
	TabID       string
}

func (k tabKey) String() string { return k.WorkspaceID + ":" + k.TabID }

// Daemon is the long-lived smart-nav state tracker.
type Daemon struct {
	client    *herdr.Client
	mu        sync.Mutex
	paneToTab map[string]tabKey
	tabPanes  map[string][]string // tabKey -> pane ids known to belong to it
}

// RunDaemon acquires the lock, bootstraps from the snapshot, and runs the event
// loop until the socket closes. Exits non-nil on fatal error.
func RunDaemon() error {
	dir := stateDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("state dir: %w", err)
	}
	release, err := acquireLock(dir)
	if err != nil {
		return err
	}
	defer release()

	logf := newLogger(dir)
	logf.Printf("smartnav daemon started (herdr %s)", os.Getenv("HERDR_SOCKET_PATH"))

	d := &Daemon{
		client:    herdr.NewClient(),
		paneToTab: map[string]tabKey{},
		tabPanes:  map[string][]string{},
	}

	if err := d.bootstrap(logf); err != nil {
		logf.Printf("bootstrap error: %v", err)
	}

	subs := []string{"pane.focused", "layout.updated", "pane.closed", "pane.moved"}
	attempt := 0
	for {
		logf.Printf("subscribing to %v", subs)
		err := herdr.Subscribe(os.Getenv("HERDR_SOCKET_PATH"), subs, func(ev herdr.Event) bool {
			d.handle(ev, logf)
			return true
		})
		if err != nil {
			attempt++
			sleep := time.Duration(min(attempt, 30)) * time.Second
			logf.Printf("subscribe ended: %v — reconnecting in %v", err, sleep)
			time.Sleep(sleep)
		} else {
			attempt = 0
		}
	}
}

func (d *Daemon) bootstrap(logf *log.Logger) error {
	res, err := d.client.Call("session.snapshot", map[string]any{})
	if err != nil {
		return err
	}
	var wrap struct {
		Snapshot struct {
			Layouts []layout.Layout `json:"layouts"`
		} `json:"snapshot"`
	}
	if err := json.Unmarshal(res, &wrap); err != nil {
		return err
	}
	for i := range wrap.Snapshot.Layouts {
		l := &wrap.Snapshot.Layouts[i]
		d.resyncTab(logf, l)
	}
	return nil
}

// resyncTab updates the pane->tab map for one tab from a full layout and prunes
// / seeds the persisted state. Called on bootstrap and on layout_updated.
func (d *Daemon) resyncTab(logf *log.Logger, l *layout.Layout) {
	d.mu.Lock()
	key := tabKey{WorkspaceID: l.WorkspaceID, TabID: l.TabID}

	// Remove old pane->tab entries for this tab.
	for _, pid := range d.tabPanes[key.String()] {
		delete(d.paneToTab, pid)
	}
	// Add fresh mapping.
	panes := make([]string, 0, len(l.Panes))
	for _, p := range l.Panes {
		d.paneToTab[p.PaneID] = key
		panes = append(panes, p.PaneID)
	}
	d.tabPanes[key.String()] = panes
	d.mu.Unlock()

	// Rebuild tree & reconcile persisted state.
	tree := layout.BuildTree(l)
	st, err := Load(key.WorkspaceID, key.TabID)
	if err != nil {
		logf.Printf("load %s: %v", key, err)
		return
	}
	// Prune split ids that no longer exist.
	present := map[string]bool{}
	if len(l.Splits) == 0 {
		// single-pane tab: nothing to track
	} else {
		for _, s := range l.Splits {
			present[s.ID] = true
		}
	}
	for id := range st.LastActivePerSplit {
		if !present[id] {
			delete(st.LastActivePerSplit, id)
		}
	}
	// Seed new splits with the focused pane if it lies in that subtree.
	if tree != nil {
		for _, s := range l.Splits {
			if _, ok := st.LastActivePerSplit[s.ID]; ok {
				continue
			}
			splitNode := findSplitNode(tree, s.ID)
			if splitNode != nil && layout.ContainsPane(splitNode, l.FocusedPaneID) {
				st.LastActivePerSplit[s.ID] = l.FocusedPaneID
			}
		}
	}
	st.LastFocusedPane = l.FocusedPaneID
	if err := Save(st); err != nil {
		logf.Printf("save %s: %v", key, err)
	}
}

func findSplitNode(n *layout.Node, splitID string) *layout.Node {
	if n == nil {
		return nil
	}
	if n.IsSplit() && n.SplitID == splitID {
		return n
	}
	if r := findSplitNode(n.First, splitID); r != nil {
		return r
	}
	return findSplitNode(n.Second, splitID)
}

func (d *Daemon) handle(ev herdr.Event, logf *log.Logger) {
	switch ev.Event {
	case "pane_focused":
		var d2 struct {
			PaneID      string `json:"pane_id"`
			WorkspaceID string `json:"workspace_id"`
		}
		if err := json.Unmarshal(ev.Data, &d2); err != nil {
			return
		}
		d.onPaneFocused(logf, d2.PaneID)
	case "layout_updated":
		var d2 struct {
			Layout layout.Layout `json:"layout"`
		}
		if err := json.Unmarshal(ev.Data, &d2); err != nil {
			return
		}
		d.resyncTab(logf, &d2.Layout)
	case "pane_closed":
		var d2 struct {
			PaneID string `json:"pane_id"`
		}
		if err := json.Unmarshal(ev.Data, &d2); err != nil {
			return
		}
		d.mu.Lock()
		var deletedTab tabKey
		hasDelete := false
		if old, ok := d.paneToTab[d2.PaneID]; ok {
			delete(d.paneToTab, d2.PaneID)
			plist := d.tabPanes[old.String()]
			for i, p := range plist {
				if p == d2.PaneID {
					d.tabPanes[old.String()] = append(plist[:i], plist[i+1:]...)
					break
				}
			}
			if len(d.tabPanes[old.String()]) == 0 {
				delete(d.tabPanes, old.String())
				deletedTab = old
				hasDelete = true
			}
		}
		d.mu.Unlock()
		if hasDelete {
			if err := Delete(deletedTab.WorkspaceID, deletedTab.TabID); err != nil {
				logf.Printf("delete state %s: %v", deletedTab, err)
			}
		}
	case "pane_moved":
		var d2 struct {
			PreviousPaneID string `json:"previous_pane_id"`
			Pane           struct {
				PaneID      string `json:"pane_id"`
				WorkspaceID string `json:"workspace_id"`
				TabID       string `json:"tab_id"`
			} `json:"pane"`
		}
		if err := json.Unmarshal(ev.Data, &d2); err != nil {
			return
		}
		d.mu.Lock()
		oldKey, hadOld := d.paneToTab[d2.PreviousPaneID]
		delete(d.paneToTab, d2.PreviousPaneID)
		newKey := tabKey{WorkspaceID: d2.Pane.WorkspaceID, TabID: d2.Pane.TabID}
		d.paneToTab[d2.Pane.PaneID] = newKey
		if hadOld {
			plist := d.tabPanes[oldKey.String()]
			for i, p := range plist {
				if p == d2.PreviousPaneID {
					d.tabPanes[oldKey.String()] = append(plist[:i], plist[i+1:]...)
					break
				}
			}
		}
		d.tabPanes[newKey.String()] = append(d.tabPanes[newKey.String()], d2.Pane.PaneID)
		d.mu.Unlock()
	}
}

func (d *Daemon) onPaneFocused(logf *log.Logger, paneID string) {
	d.mu.Lock()
	key, ok := d.paneToTab[paneID]
	d.mu.Unlock()
	if !ok {
		// Unknown pane: skip; an upcoming layout_updated will map it.
		return
	}
	// Fetch the live tab layout to walk the path.
	res, err := d.client.Call("pane.layout", map[string]string{"pane_id": paneID})
	if err != nil {
		logf.Printf("pane.layout %s: %v", paneID, err)
		return
	}
	l, err := layout.ParseLayout(res)
	if err != nil {
		logf.Printf("parse layout: %v", err)
		return
	}
	tree := layout.BuildTree(l)
	if tree == nil {
		return
	}
	st, err := Load(key.WorkspaceID, key.TabID)
	if err != nil {
		logf.Printf("load %s: %v", key, err)
		return
	}
	st.LastFocusedPane = paneID
	for _, anc := range layout.Ancestors(tree, paneID) {
		if anc.IsSplit() {
			st.LastActivePerSplit[anc.SplitID] = paneID
		}
	}
	if err := Save(st); err != nil {
		logf.Printf("save %s: %v", key, err)
	}
}

// acquireLock gets an advisory flock and writes a pid file. Returns a release fn.
func acquireLock(dir string) (func(), error) {
	lockPath := filepath.Join(dir, "daemon.lock")
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another daemon is running (%s): %w", lockPath, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "daemon.pid"), []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644); err != nil {
		f.Close()
		return nil, fmt.Errorf("write pid file: %w", err)
	}
	return func() {
		os.Remove(filepath.Join(dir, "daemon.pid"))
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

func newLogger(dir string) *log.Logger {
	f, err := os.OpenFile(filepath.Join(dir, "daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return log.New(os.Stderr, "smartnav: ", log.LstdFlags)
	}
	return log.New(f, "", log.LstdFlags|log.Lmicroseconds)
}
