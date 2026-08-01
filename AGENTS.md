# herdr-smartnav — Agent Instructions

## Commands

```
go build -o herdr-smartnav .    # build the plugin binary
go vet ./...                    # typecheck + static analysis
make test                       # build + run integration harness (needs herdr on PATH)
make bump-version VERSION=x.y.z # edit herdr-plugin.toml, commit, tag
make release                    # cross-compile + tar (auto-stamps version from toml)
```

`make build` and `make test` auto-configure `.githooks/`
(via `git config core.hooksPath .githooks`).

No codegen, no linter config, no go.sum (stdlib only).

## Architecture

- **Daemon** (`internal/history/daemon.go`): long-lived `[[startup]]` hook. Subscribes to
  `pane.focused`/`layout.updated`/`pane.closed`/`pane.moved`. Tracks last-active pane per
  split in-memory; persists atomically per tab.
- **Action** (`internal/nav/smart.go`): short-lived per-keypress. Reads layout via socket,
  resolves direction target via layout tree, loads daemon state, calls `pane.focus`.
  Falls back to `pane.focus_direction` on any failure path.
- **Layout tree** (`internal/layout/tree.go`): flat `panes[]`+`splits[]` → nested tree
  by rect containment. No BSP assumptions.
- **State** (`internal/history/store.go`): `smart.<ws>.<tab>.json` per tab.
  `HERDR_PLUGIN_STATE_DIR` → `HERDR_PLUGIN_CONFIG_DIR` → `~/.local/share/herdr/smartnav`.

## Gotchas

- **Startup hooks don't run on `reload-config` or `plugin link`.** Must restart the
  server (`herdr server stop && herdr server`) for the daemon to start. This is covered
  in the README install section.
- **JSON-RPC `id` MUST be a string.** The herdr socket rejects integer ids.
- **Subscription types are dotted** (`pane.focused`), **pushed event types are snake_case**
  (`pane_focused`). Event handler must match on the pushed type.
- **`pane_focused` event has no `tab_id`.** The daemon maintains a `pane_id → tab_id`
  map built from `session.snapshot` + `layout_updated` full layouts.
- **`pane.focus` is undocumented** but works. `pane.focus_direction` is the documented fallback.
- **Module name is `herdr-smartnav`** (no namespace prefix). Plugin id is `smartnav`.
  Import paths are `herdr-smartnav/internal/...`.
- **Layout splits use `"right"`/`"down"`** for direction — not `"Horizontal"`/`"Vertical"`.

## Testing

The harness (`test/harness.py`) boots a fully isolated herdr server via `XDG_CONFIG_HOME`
redirect — zero contact with the live session. Drives everything over the raw JSON-RPC
socket (no TUI). Test layouts are built with `layout.apply` declaratively.

```
make test          # build + run all scenarios
python3 test/harness.py --keep   # keep temp dir on failure for debugging
```
