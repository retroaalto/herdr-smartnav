// Package layout models a herdr tab layout and reconstructs the nested split
// tree from the flat panes+splits representation the socket API returns.
package layout

import "encoding/json"

// Rect is a pane/split screen rectangle.
type Rect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Pane is a flat pane entry from pane.layout / snapshot.layouts.
type Pane struct {
	PaneID  string `json:"pane_id"`
	Focused bool   `json:"focused"`
	Rect    Rect   `json:"rect"`
}

// Split is a flat split entry. Direction is "right" (horizontal axis) or "down".
type Split struct {
	ID        string  `json:"id"`
	Direction string  `json:"direction"` // "right" | "down"
	Ratio     float64 `json:"ratio"`
	Rect      Rect    `json:"rect"`
}

// Layout is the flat per-tab layout returned by pane.layout and present in
// session.snapshot.layouts[].
type Layout struct {
	WorkspaceID   string  `json:"workspace_id"`
	TabID         string  `json:"tab_id"`
	Zoomed        bool    `json:"zoomed"`
	Area          Rect    `json:"area"`
	FocusedPaneID string  `json:"focused_pane_id"`
	Panes         []Pane  `json:"panes"`
	Splits        []Split `json:"splits"`
}

// ParseLayout decodes a pane.layout result payload.
func ParseLayout(raw json.RawMessage) (*Layout, error) {
	var wrap struct {
		Type   string `json:"type"`
		Layout Layout `json:"layout"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, err
	}
	l := wrap.Layout
	return &l, nil
}

// Node is a reconstructed tree node: either a pane leaf or a split with children.
type Node struct {
	Kind      string // "pane" | "split"
	PaneID    string
	SplitID   string
	Direction string // for split
	First     *Node
	Second    *Node
	Rect      Rect
}

// IsPane reports whether the node is a pane leaf.
func (n *Node) IsPane() bool { return n != nil && n.Kind == "pane" }

// IsSplit reports whether the node is a split.
func (n *Node) IsSplit() bool { return n != nil && n.Kind == "split" }

// contains reports whether r strictly contains inner (inner fits within r and
// is smaller in area; equal rects are treated as not-strictly-contained).
func contains(r, inner Rect) bool {
	return inner.X >= r.X && inner.Y >= r.Y &&
		inner.X+inner.Width <= r.X+r.Width &&
		inner.Y+inner.Height <= r.Y+r.Height &&
		!(inner == r)
}

func rectEqual(a, b Rect) bool { return a == b }

// BuildTree reconstructs the nested tree from the flat layout by rect
// containment. The immediate parent of a node is the smallest split whose rect
// strictly contains the node's rect. Root is the split with rect == area (or the
// single pane when there are no splits).
func BuildTree(l *Layout) *Node {
	if len(l.Splits) == 0 {
		if len(l.Panes) == 1 {
			return &Node{Kind: "pane", PaneID: l.Panes[0].PaneID, Rect: l.Panes[0].Rect}
		}
		if len(l.Panes) > 0 {
			// degenerate (no splits but multiple panes): pick the focused one.
			for _, p := range l.Panes {
				if p.Focused || p.PaneID == l.FocusedPaneID {
					return &Node{Kind: "pane", PaneID: p.PaneID, Rect: p.Rect}
				}
			}
		}
		return nil
	}

	// Build split nodes keyed by id.
	splitNodes := map[string]*Node{}
	for _, s := range l.Splits {
		splitNodes[s.ID] = &Node{Kind: "split", SplitID: s.ID, Direction: s.Direction, Rect: s.Rect}
	}

	// immediate parent of each split = smallest split that strictly contains it.
	parentOf := map[string]string{} // child split id -> parent split id
	for _, child := range l.Splits {
		var bestParent string
		var bestArea int = -1
		for _, par := range l.Splits {
			if par.ID == child.ID {
				continue
			}
			if contains(par.Rect, child.Rect) {
				a := par.Rect.Width * par.Rect.Height
				if bestParent == "" || a < bestArea {
					bestParent = par.ID
					bestArea = a
				}
			}
		}
		if bestParent != "" {
			parentOf[child.ID] = bestParent
		}
	}

	// Assign each pane to its immediate parent split (smallest containing split).
	paneParent := map[string]string{} // pane_id -> split id
	for _, p := range l.Panes {
		var bestSplit string
		var bestArea int = -1
		for _, s := range l.Splits {
			if contains(s.Rect, p.Rect) {
				a := s.Rect.Width * s.Rect.Height
				if bestSplit == "" || a < bestArea {
					bestSplit = s.ID
					bestArea = a
				}
			}
		}
		if bestSplit != "" {
			paneParent[p.PaneID] = bestSplit
		}
	}

	childrenOf := map[string][]*Node{}
	addChild := func(parentID string, n *Node) {
		childrenOf[parentID] = append(childrenOf[parentID], n)
	}

	paneNodes := map[string]*Node{}
	for _, p := range l.Panes {
		pn := &Node{Kind: "pane", PaneID: p.PaneID, Rect: p.Rect}
		paneNodes[p.PaneID] = pn
		if pid, ok := paneParent[p.PaneID]; ok {
			addChild(pid, pn)
		}
	}
	for _, s := range l.Splits {
		if pid, ok := parentOf[s.ID]; ok {
			addChild(pid, splitNodes[s.ID])
		}
	}

	// For each split, sort its two children into first/second by axis geometry.
	for _, s := range l.Splits {
		kids := childrenOf[s.ID]
		if len(kids) != 2 {
			// Could be 1 kid (degenerate) — attach what we have.
			if len(kids) == 1 {
				splitNodes[s.ID].First = kids[0]
			}
			continue
		}
		a, b := kids[0], kids[1]
		if s.Direction == "right" {
			// horizontal axis: first = leftmost (smaller x)
			if a.Rect.X <= b.Rect.X {
				splitNodes[s.ID].First = a
				splitNodes[s.ID].Second = b
			} else {
				splitNodes[s.ID].First = b
				splitNodes[s.ID].Second = a
			}
		} else { // "down" — vertical axis: first = topmost (smaller y)
			if a.Rect.Y <= b.Rect.Y {
				splitNodes[s.ID].First = a
				splitNodes[s.ID].Second = b
			} else {
				splitNodes[s.ID].First = b
				splitNodes[s.ID].Second = a
			}
		}
	}

	// Root: parentless split with rect == area (strict), then same dimensions, then any parentless.
	for _, s := range l.Splits {
		if _, hasParent := parentOf[s.ID]; !hasParent && rectEqual(s.Rect, l.Area) {
			return splitNodes[s.ID]
		}
	}
	for _, s := range l.Splits {
		if _, hasParent := parentOf[s.ID]; !hasParent && s.Rect.Width == l.Area.Width && s.Rect.Height == l.Area.Height {
			return splitNodes[s.ID]
		}
	}
	for _, s := range l.Splits {
		if _, hasParent := parentOf[s.ID]; !hasParent {
			return splitNodes[s.ID]
		}
	}
	// Last resort: a single pane.
	for _, p := range l.Panes {
		return paneNodes[p.PaneID]
	}
	return nil
}

// FindPane returns the pane node with the given id, or nil.
func FindPane(root *Node, paneID string) *Node {
	if root == nil {
		return nil
	}
	if root.IsPane() {
		if root.PaneID == paneID {
			return root
		}
		return nil
	}
	if n := FindPane(root.First, paneID); n != nil {
		return n
	}
	return FindPane(root.Second, paneID)
}

// Ancestors returns the chain of split nodes from root down to the one that
// directly contains the pane (deepest ancestor of paneID is last). The pane
// itself is not included.
func Ancestors(root *Node, paneID string) []*Node {
	path, _ := ancestors(root, paneID)
	return path
}

func ancestors(n *Node, paneID string) (path []*Node, found bool) {
	if n == nil {
		return nil, false
	}
	if n.IsPane() {
		if n.PaneID == paneID {
			return nil, true
		}
		return nil, false
	}
	// n is a split; recurse into the child that contains the pane.
	if p, ok := ancestors(n.First, paneID); ok {
		return append([]*Node{n}, p...), true
	}
	if p, ok := ancestors(n.Second, paneID); ok {
		return append([]*Node{n}, p...), true
	}
	return nil, false
}

// InnermostAncestorSplit returns the deepest ancestor split of paneID whose
// direction matches the given axis ("right"=horizontal, "down"=vertical), or
// nil. axis must be "right" or "down".
func InnermostAncestorSplit(root *Node, paneID, axis string) *Node {
	anc := Ancestors(root, paneID)
	for i := len(anc) - 1; i >= 0; i-- {
		if anc[i].Direction == axis {
			return anc[i]
		}
	}
	return nil
}

// MoveTarget resolves the sibling subtree to move into when navigating in
// `direction` (left/right/up/down) from paneID. It finds the deepest ancestor
// split on the requested axis where paneID lies on the side OPPOSITE the
// movement direction, then returns that split's other child. Returns nil if no
// valid move exists in that direction (caller should fall back to default nav).
//
//	direction | axis   | pane must be in  | sibling taken from
//	----------+--------+------------------+--------------------
//	right      | right  | first  (left)    | second
//	left       | right  | second (right)   | first
//	down       | down   | first  (top)     | second
//	up         | down   | second (bottom)  | first
func MoveTarget(root *Node, paneID, direction string) *Node {
	axis, requiredSide := "", ""
	switch direction {
	case "right":
		axis, requiredSide = "right", "first"
	case "left":
		axis, requiredSide = "right", "second"
	case "down":
		axis, requiredSide = "down", "first"
	case "up":
		axis, requiredSide = "down", "second"
	default:
		return nil
	}
	anc := Ancestors(root, paneID)
	for i := len(anc) - 1; i >= 0; i-- {
		s := anc[i]
		if s.Direction != axis {
			continue
		}
		var inFirst bool = FindPane(s.First, paneID) != nil
		if inFirst == (requiredSide == "first") {
			// pane is on the required side: the sibling is the other child
			if requiredSide == "first" {
				return s.Second
			}
			return s.First
		}
	}
	return nil
}

// Sibling returns the other child of split s relative to the side containing
// paneID: if paneID is in First, returns Second; if in Second, returns First.
func Sibling(split *Node, paneID string) *Node {
	if split == nil || !split.IsSplit() {
		return nil
	}
	if FindPane(split.First, paneID) != nil {
		return split.Second
	}
	if FindPane(split.Second, paneID) != nil {
		return split.First
	}
	return nil
}

// FirstLeafPane returns the pane id of the first leaf under a node.
func FirstLeafPane(n *Node) string {
	for n != nil {
		if n.IsPane() {
			return n.PaneID
		}
		n = n.First
	}
	return ""
}

// ContainsPane reports whether paneID exists under node.
func ContainsPane(n *Node, paneID string) bool {
	return FindPane(n, paneID) != nil
}
