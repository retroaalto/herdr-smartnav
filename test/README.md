# Integration test harness

Isolated-server integration tests for herdr-smartnav.

## Overview

`harness.py` boots a throwaway herdr server with fully redirected state
(`XDG_CONFIG_HOME`), builds test layouts via `layout.apply`, runs the
smartnav daemon and action binaries, drives everything over the socket
JSON-RPC API, and asserts the focused pane matches the expected tmux-style
target.

No live `~/.config/herdr` is touched. No TUI or terminal is needed.

## Requirements

- Python 3.9+
- `herdr` binary on `$PATH`
- Built `herdr-smartnav` binary at the repo root

## Usage

```bash
# Build and run all tests
make test

# Or manually
go build -o herdr-smartnav .
python3 test/harness.py
# Add --keep to preserve the temp directory on failure
python3 test/harness.py --keep
```

## Test scenarios

| # | Layout | Action | Expected behavior |
|---|--------|--------|-------------------|
| 1 | `[A | [B, C]]` | Focus C, then A, right | Focuses C (last active in right split) |
| 2 | `[A | [B, C]]` | Focus B, then A, right | Focuses B |
| 3 | `[A | [B, C]]` | From C, up | Focuses B (single-pane sibling) |
| 4 | `[A | [B, C]]` | From B, left | Focuses A (single-pane sibling) |
| Z | `[A | [B, C]]` | Zoom A, right | Delegates to `pane.focus_direction`, navigates |
| 5 | `[A | [B, C]]` | Close C, right from A | Focuses B; stale `split_1_1` pruned from state |
| 6 | `[[A,B],[C,D]]` | Right from A → B | Innermost split wins (B, not C) |
| 7 | `[A | [B, C]]` | Daemon down, right from A | Falls back to default nav |

## Architecture

```
harness.py
├── Herdr.start/stop         → subprocess manages herdr server lifecycle
├── Herdr._call              → raw JSON-RPC over Unix socket
├── layout_apply_abc         → builds [A | [B, C]] declaratively
├── layout_apply_nested_hh   → builds [[A,B],[C,D]] for innermost-ancestor tests
├── wait_state               → polls state JSON files from daemon
└── run_action               → invokes herdr-smartnav [direction] as subprocess
```
