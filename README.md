# herdr-smartnav

Direction-aware pane navigation plugin for [herdr](https://herdr.dev) that
remembers the last active pane per layout split — tmux-style.

## Problem

herdr's built-in `focus_pane_left/right/up/down` always picks the first child
when navigating toward a split container with multiple panes. It has no memory
of which pane was last active inside that split.

With herdr-smartnav, navigating right from A in `[A | [B, C]]` goes to the last
active pane among B and C — matching tmux behavior.

```
+-----------------+-----------------+
|                 |        B        |
|        A        +-----------------+
|                 |        C        |
+-----------------+-----------------+

C was last active in the right column. Press right from A:

  herdr default  →  B  (first child, always)
  herdr-smartnav →  C  (last active in split)

Now press up from C:

  herdr default  →  B  (up in same split, correct by coincidence)
  herdr-smartnav →  B  (agrees)

Now from A, press right, then up:

  herdr default  →  B → B  (stuck — can't reach C again via direction)
  herdr-smartnav →  C → B  (correct — remembers C was last in right split)
```

## Requirements

- herdr >= 0.7.5
- Go 1.22+ (build only)
- Linux or macOS

## Install

```bash
# Clone and build
git clone https://github.com/retroaalto/herdr-smartnav ~/.config/herdr/plugins/herdr-smartnav
cd ~/.config/herdr/plugins/herdr-smartnav
go build -o herdr-smartnav .

# Register the plugin
herdr plugin link ~/.config/herdr/plugins/herdr-smartnav

# Restart herdr to start the daemon (reload-config is not enough —
# startup hooks only run when the server starts)
herdr server stop
herdr server
```

> **Note:** The daemon runs as a `[[startup]]` hook. herdr only runs startup
> hooks on server start, not on `reload-config` or `plugin link`. If you
> update the binary later, restart the server (`herdr server stop && herdr
> server`) instead of reloading.

At runtime, herdr only needs the binary (`herdr-smartnav`) and manifest
(`herdr-plugin.toml`) in that directory. Source files (`.go`) are optional.

## Keybindings

First, unbind the built-in directional keys in `~/.config/herdr/config.toml`:

```toml
# Comment out or remove:
# focus_pane_left  = ["prefix+h", "alt+h"]
# focus_pane_down  = ["prefix+j", "alt+j"]
# focus_pane_up    = ["prefix+k", "alt+k"]
# focus_pane_right = ["prefix+l", "alt+l"]
```

Then bind the plugin actions:

```toml
[[keys.command]]
key = "alt+h"
type = "plugin_action"
command = "smartnav.left"
description = "Focus left (smart)"

[[keys.command]]
key = "alt+j"
type = "plugin_action"
command = "smartnav.down"
description = "Focus down (smart)"

[[keys.command]]
key = "alt+k"
type = "plugin_action"
command = "smartnav.up"
description = "Focus up (smart)"

[[keys.command]]
key = "alt+l"
type = "plugin_action"
command = "smartnav.right"
description = "Focus right (smart)"
```

Prefix bindings work the same way:

```toml
[[keys.command]]
key = "prefix+h"
type = "plugin_action"
command = "smartnav.left"
# ... etc
```

Reload after changes: `herdr server reload-config`

## How it works

1. A **daemon** subscribes to `pane.focused` events and tracks the last active
   pane in each layout split container, writing state per-tab to
   `$HERDR_PLUGIN_STATE_DIR/smart.<workspace>.<tab>.json`.
2. On each direction keypress, a short-lived **action process** reads the
   current layout, finds the neighboring split subtree via the layout tree,
   looks up the last active pane in that subtree, and calls `pane.focus`.
3. Falls back to herdr's default `pane.focus_direction` when the daemon is
   down, the state file is missing, or the tab is zoomed.

## Testing

```bash
make test
```

Requires a `herdr` binary on `$PATH`. The harness boots an isolated herdr
server (no live session interference) and runs assertion-based integration
tests over the socket API. See `test/README.md` for details.

## Releasing

```bash
make bump-version VERSION=x.y.z   # edit toml, git commit, git tag
make release                      # cross-compile + versioned tarballs
```

Artifacts land in `release/` as `herdr-smartnav-vX.Y.Z-{os}-{arch}.tar.gz`.

## Uninstall

```bash
herdr plugin unlink smartnav
```

## License

MIT
