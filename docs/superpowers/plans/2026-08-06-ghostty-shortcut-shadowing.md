# Ghostty Shortcut Shadowing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add direct Cmd+W, Cmd+T, Cmd+Shift+H, and Cmd+Shift+L HRDX controls that mirror the user's Ghostty tab ergonomics while protecting the final pane and tab.

**Architecture:** Keep HRDX's existing prefix system and raw CSI-u routing. Add one guarded semantic-close operation, then decode the Kitty Super modifier and intercept only the four exact direct chords in terminal mode before unmatched input is forwarded to the child pane.

**Tech Stack:** Go 1.25+, Bubble Tea, Kitty keyboard protocol/CSI-u, existing `internal/ui` model tests.

## Global Constraints

- Do not change the Ghostty configuration.
- Do not add a general keybinding framework or a new configuration format.
- Keep every existing Ctrl+B binding available.
- Direct shortcuts are active only in `modeTerminal`; modal views retain their current input ownership.
- Match exact modifier sets; extra Ctrl or Alt modifiers must not trigger HRDX actions.
- Preserve unmatched Super sequences byte-for-byte for the focused child pane.
- The final pane of the final tab must never be closed by a UI shortcut.
- `Cmd+T` opens the existing agent/shell picker and does not create a tab until a kind is selected.
- Ghostty 1.3.1 is the required smoke-test host.

---

### Task 1: Guard the semantic close hierarchy

**Files:**
- Modify: `internal/ui/model.go:992-1054`
- Modify: `internal/ui/model.go:1333-1406`
- Modify: `internal/ui/model.go:2547-2586`
- Test: `internal/ui/model_test.go:444-493`

**Interfaces:**
- Consumes: existing `(*Model).closeCurrentPane()`, `(*Model).closeTab(*space, *tab)`, `(*Model).resizePanes(*space)`, `(*Model).persist()`, and `(*Model).flashStatus(string) tea.Cmd`.
- Produces: `func (m *Model) closeCurrentSurface() tea.Cmd`, the single guarded UI close operation used by prefix, menu, and direct-shortcut paths.
- Preserves: `closeCurrentPane()` as the low-level pane-removal operation used by the socket API; this task changes UI semantics, not the existing `pane.close` API contract.

- [ ] **Step 1: Add failing hierarchy and prefix tests**

Add these tests beside the existing close tests in `internal/ui/model_test.go`:

```go
func TestCloseCurrentSurfaceHierarchy(t *testing.T) {
	t.Run("pane", func(t *testing.T) {
		model := newTestModel("/tmp/api")
		currentSpace := model.currentSpace()
		model.addPane(currentSpace, "shell", true)

		if cmd := model.closeCurrentSurface(); cmd != nil {
			t.Fatal("closing one of multiple panes should not flash status")
		}
		if got := len(currentSpace.tab().panes); got != 1 {
			t.Fatalf("panes = %d, want 1", got)
		}
	})

	t.Run("tab", func(t *testing.T) {
		model := newTestModel("/tmp/api")
		currentSpace := model.currentSpace()
		model.addTab(currentSpace, "zot")

		if cmd := model.closeCurrentSurface(); cmd != nil {
			t.Fatal("closing one of multiple tabs should not flash status")
		}
		if got := len(currentSpace.tabs); got != 1 {
			t.Fatalf("tabs = %d, want 1", got)
		}
		if got := len(currentSpace.tab().panes); got != 1 {
			t.Fatalf("remaining tab panes = %d, want 1", got)
		}
	})

	t.Run("final surface", func(t *testing.T) {
		model := newTestModel("/tmp/api")
		currentSpace := model.currentSpace()
		currentPane := model.currentPane()

		if cmd := model.closeCurrentSurface(); cmd == nil {
			t.Fatal("protecting the final surface should schedule status expiry")
		}
		if model.status != "last pane cannot be closed" {
			t.Fatalf("status = %q", model.status)
		}
		if len(currentSpace.tabs) != 1 || len(currentSpace.tab().panes) != 1 {
			t.Fatal("final surface was removed")
		}
		if model.currentPane() != currentPane {
			t.Fatal("final pane focus changed")
		}
	})
}

func TestPrefixCloseProtectsFinalPane(t *testing.T) {
	model := newTestModel("/tmp/api")
	updated, _ := model.updateKey(tea.KeyMsg{Type: tea.KeyCtrlB})
	model = updated.(Model)
	updated, cmd := model.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	model = updated.(Model)

	if cmd == nil {
		t.Fatal("prefix close should flash the final-pane status")
	}
	if model.status != "last pane cannot be closed" {
		t.Fatalf("status = %q", model.status)
	}
	if len(model.currentSpace().tab().panes) != 1 {
		t.Fatal("prefix close removed the final pane")
	}
}

func TestPaneMenuCloseUsesGuardedSurfaceClose(t *testing.T) {
	model := newTestModel("/tmp/api")
	currentSpace := model.currentSpace()
	target := model.addPane(currentSpace, "shell", true)
	model.menuPane = target
	model.mode = modeMenu

	updated, _ := model.runMenuAction("close")
	model = updated.(Model)
	if got := len(currentSpace.tab().panes); got != 1 {
		t.Fatalf("panes = %d, want 1", got)
	}
}
```

- [ ] **Step 2: Run the focused tests and verify the missing operation fails**

Run:

```bash
go test ./internal/ui -run 'Test(CloseCurrentSurfaceHierarchy|PrefixCloseProtectsFinalPane|PaneMenuCloseUsesGuardedSurfaceClose)$'
```

Expected: build failure because `Model.closeCurrentSurface` is undefined.

- [ ] **Step 3: Implement the guarded close operation**

Add this operation immediately before `closeCurrentPane` in `internal/ui/model.go`:

```go
func (m *Model) closeCurrentSurface() tea.Cmd {
	currentSpace := m.currentSpace()
	if currentSpace == nil {
		return nil
	}
	currentTab := currentSpace.tab()
	if len(currentTab.panes) > 1 {
		m.closeCurrentPane()
		return nil
	}
	if len(currentTab.panes) == 1 && len(currentSpace.tabs) > 1 {
		m.closeTab(currentSpace, currentTab)
		m.resizePanes(currentSpace)
		m.persist()
		return nil
	}
	return m.flashStatus("last pane cannot be closed")
}
```

Route prefix close through it in `runPrefix`:

```go
	case "x":
		return m, m.closeCurrentSurface()
```

Route the pane menu close through the same operation in `runMenuAction`:

```go
	case "close":
		return m, m.closeCurrentSurface()
```

Keep `closeCurrentPane` and its API caller unchanged. The new wrapper owns only the user-facing close hierarchy.

- [ ] **Step 4: Format and run the close tests**

Run:

```bash
gofmt -w internal/ui/model.go internal/ui/model_test.go
go test ./internal/ui -run 'Test(CloseCurrentSurfaceHierarchy|PrefixCloseProtectsFinalPane|PaneMenuCloseUsesGuardedSurfaceClose|CloseCurrentPaneClamps|CloseLastPaneClosesTab|PaneMenuHidesCloseForOnlyPaneInTab)$'
```

Expected: PASS. Existing low-level pane-close and menu-visibility behavior remains covered.

- [ ] **Step 5: Commit the guarded close behavior**

```bash
git add internal/ui/model.go internal/ui/model_test.go
git commit -m "fix: protect the final pane from UI close"
```

---

### Task 2: Route direct Cmd shortcuts from Kitty CSI-u

**Files:**
- Modify: `internal/ui/rawinput.go:31-36`
- Modify: `internal/ui/model.go:796-839`
- Modify: `internal/ui/model.go` imports to add `unicode`
- Test: `internal/ui/model_test.go:3-12`
- Test: `internal/ui/model_test.go:312-324`
- Test: `internal/ui/model_test.go` near the existing tab and raw-input tests
- Modify: `README.md:68-96`

**Interfaces:**
- Consumes: `closeCurrentSurface() tea.Cmd` from Task 1; existing `openKindPicker`, `selectTab`, `currentSpace`, raw `parseCSIU`, and `term.NewHolderPane`.
- Produces: `modSuper = 8` and `func (m *Model) runDirectShortcut(code rune, mods int) (tea.Cmd, bool)`.
- Contract: `runDirectShortcut` returns `handled=true` only for exact Cmd+W, Cmd+T, Cmd+Shift+H, and Cmd+Shift+L combinations.

- [ ] **Step 1: Add a recording terminal host for forwarding tests**

Add `bytes` and the terminal package to the `internal/ui/model_test.go` imports:

```go
import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/patriceckhart/hrdx/internal/state"
	"github.com/patriceckhart/hrdx/internal/term"
)
```

Add this test-only host below `newTestModel`:

```go
type recordingSessionHost struct {
	writes [][]byte
}

func (h *recordingSessionHost) Write(_ int64, data []byte) {
	h.writes = append(h.writes, append([]byte(nil), data...))
}
func (*recordingSessionHost) Resize(int64, int, int) {}
func (*recordingSessionHost) Kill(int64)             {}
func (*recordingSessionHost) Foreground(int64) string { return "" }
```

- [ ] **Step 2: Add failing parser and direct-shortcut tests**

Extend `TestParseCSIU`:

```go
	code, mods, ok = parseCSIU([]byte("\x1b[119;9u"))
	if !ok || code != 'w' || mods != modSuper {
		t.Fatalf("parseCSIU cmd+w = %q/%d/%v", code, mods, ok)
	}
	code, mods, ok = parseCSIU([]byte("\x1b[104;10u"))
	if !ok || code != 'h' || mods != modSuper|modShift {
		t.Fatalf("parseCSIU cmd+shift+h = %q/%d/%v", code, mods, ok)
	}
```

Add these behavioral tests:

```go
func TestDirectSuperShortcuts(t *testing.T) {
	t.Run("close pane", func(t *testing.T) {
		model := newTestModel("/tmp/api")
		currentSpace := model.currentSpace()
		model.addPane(currentSpace, "shell", true)

		updated, _ := model.updateRaw([]byte("\x1b[119;9u"))
		model = updated.(Model)
		if got := len(currentSpace.tab().panes); got != 1 {
			t.Fatalf("panes = %d, want 1", got)
		}
	})

	t.Run("new tab picker", func(t *testing.T) {
		model := newTestModel("/tmp/api")
		before := len(model.currentSpace().tabs)

		updated, _ := model.updateRaw([]byte("\x1b[116;9u"))
		model = updated.(Model)
		if model.mode != modeMenu || model.pickAction != "tab" {
			t.Fatalf("mode/action = %d/%q, want menu/tab", model.mode, model.pickAction)
		}
		if got := len(model.currentSpace().tabs); got != before {
			t.Fatalf("tabs = %d before picker selection, want %d", got, before)
		}
	})

	t.Run("tab navigation wraps", func(t *testing.T) {
		model := newTestModel("/tmp/api")
		currentSpace := model.currentSpace()
		model.addTab(currentSpace, "zot")

		updated, _ := model.updateRaw([]byte("\x1b[72;10u"))
		model = updated.(Model)
		if currentSpace.active != 0 {
			t.Fatalf("previous tab = %d, want 0", currentSpace.active)
		}
		updated, _ = model.updateRaw([]byte("\x1b[108;10u"))
		model = updated.(Model)
		if currentSpace.active != 1 {
			t.Fatalf("next tab = %d, want 1", currentSpace.active)
		}
	})

	t.Run("no workspace", func(t *testing.T) {
		model := newTestModel()
		for _, input := range [][]byte{
			[]byte("\x1b[119;9u"),
			[]byte("\x1b[116;9u"),
			[]byte("\x1b[104;10u"),
			[]byte("\x1b[108;10u"),
		} {
			updated, _ := model.updateRaw(input)
			model = updated.(Model)
		}
		if len(model.spaces) != 0 || model.mode != modeTerminal {
			t.Fatal("direct shortcut mutated an empty model")
		}
	})
}

func TestUnmatchedSuperInputReachesPane(t *testing.T) {
	model := newTestModel("/tmp/api")
	host := &recordingSessionHost{}
	current := model.currentPane()
	current.term = term.NewHolderPane(host, 1, 80, 24)
	current.running = true

	inputs := [][]byte{
		[]byte("\x1b[107;9u"),  // Cmd+K: unrelated Super chord.
		[]byte("\x1b[119;13u"), // Cmd+Ctrl+W: extra modifier.
		[]byte("\x1b[98;13u"),  // Cmd+Ctrl+B: must not enter prefix mode.
	}
	for _, input := range inputs {
		updated, _ := model.updateRaw(input)
		model = updated.(Model)
	}
	if len(host.writes) != len(inputs) {
		t.Fatalf("forwarded writes = %d, want %d", len(host.writes), len(inputs))
	}
	for index := range inputs {
		if !bytes.Equal(host.writes[index], inputs[index]) {
			t.Fatalf("write %d = %q, want %q", index, host.writes[index], inputs[index])
		}
	}
}

func TestDirectSuperShortcutDoesNotFireInModalMode(t *testing.T) {
	model := newTestModel("/tmp/api")
	currentSpace := model.currentSpace()
	model.mode = modeRename
	model.renamePane = model.currentPane()
	model.input.Focus()

	updated, _ := model.updateRaw([]byte("\x1b[119;9u"))
	model = updated.(Model)
	if len(currentSpace.tab().panes) != 1 {
		t.Fatal("Cmd+W closed a pane from rename mode")
	}
}
```

- [ ] **Step 3: Run the direct-shortcut tests and verify they fail**

Run:

```bash
go test ./internal/ui -run 'Test(ParseCSIU|DirectSuperShortcuts|UnmatchedSuperInputReachesPane|DirectSuperShortcutDoesNotFireInModalMode)$'
```

Expected: build failure because `modSuper` is undefined; after defining only the constant, shortcut behavior still fails until routing is implemented.

- [ ] **Step 4: Add Super decoding and the four-action matcher**

Add the Kitty Super bit in `internal/ui/rawinput.go`:

```go
const (
	modShift = 1
	modAlt   = 2
	modCtrl  = 4
	modSuper = 8
)
```

Add `unicode` to `internal/ui/model.go` imports. Add this helper immediately before `updateRaw`:

```go
func (m *Model) runDirectShortcut(code rune, mods int) (tea.Cmd, bool) {
	code = unicode.ToLower(code)
	switch {
	case mods == modSuper && code == 'w':
		return m.closeCurrentSurface(), true
	case mods == modSuper && code == 't':
		if currentSpace := m.currentSpace(); currentSpace != nil {
			m.openKindPicker("tab", currentSpace, "", rect{x: sidebarWidth + 2, y: 1})
		}
		return nil, true
	case mods == modSuper|modShift && code == 'h':
		m.selectTab(-1)
		return nil, true
	case mods == modSuper|modShift && code == 'l':
		m.selectTab(1)
		return nil, true
	default:
		return nil, false
	}
}
```

Call it in `updateRaw` after `parseCSIU` and before the Ctrl+B check. Narrow the existing prefix check to exact Ctrl so a Super+Ctrl+B chord remains unmatched and reaches the pane:

```go
	code, mods, ok := parseCSIU(raw)
	if ok && m.mode == modeTerminal {
		if cmd, handled := m.runDirectShortcut(code, mods); handled {
			return m, cmd
		}
	}
	if ok && code == 'b' && mods == modCtrl && m.mode == modeTerminal {
```

Do not broaden the existing Kitty keyboard flags. HRDX's current disambiguation flag already causes modified keys without legacy encodings to use CSI-u, and retaining it avoids introducing press/repeat/release event handling.

- [ ] **Step 5: Document the direct shortcuts**

In `README.md` under `## Keys`, insert this table before the existing Ctrl+B table:

```markdown
On macOS, a host terminal that reports the Command modifier through the Kitty keyboard protocol also supports these direct HRDX shortcuts:

| Direct shortcut | Action |
|---|---|
| `cmd+w` | Close the focused pane, then the tab when it is the only pane; protects the final pane/tab |
| `cmd+t` | New tab (opens the agent/shell picker) |
| `cmd+shift+h` / `cmd+shift+l` | Previous / next tab |

All other keys go to the focused terminal, except the `ctrl+b` prefix (tmux style):
```

Replace the existing introductory sentence rather than duplicating it.

- [ ] **Step 6: Format and run focused and full automated verification**

Run:

```bash
gofmt -w internal/ui/rawinput.go internal/ui/model.go internal/ui/model_test.go
go test ./internal/ui -run 'Test(ParseCSIU|DirectSuperShortcuts|UnmatchedSuperInputReachesPane|DirectSuperShortcutDoesNotFireInModalMode|CloseCurrentSurfaceHierarchy|PrefixCloseProtectsFinalPane)$'
go test ./...
```

Expected: all commands pass.

- [ ] **Step 7: Smoke-test the real Ghostty path**

Build and launch a disposable HRDX state from an interactive Ghostty 1.3.1 surface:

```bash
make build
tmpdir="$(mktemp -d)"
./hrdx --fresh --state "$tmpdir/state.json"
```

Inside HRDX:

1. Create a second pane with the existing Ctrl+B split path.
2. Press Cmd+W. Expected: HRDX closes the focused pane; Ghostty keeps the same outer surface.
3. Press Cmd+T. Expected: HRDX opens the agent/shell picker; Ghostty does not create an outer tab.
4. Select a kind to create the second HRDX tab.
5. Press Cmd+Shift+H, then Cmd+Shift+L. Expected: HRDX moves between its tabs and wraps; Ghostty's outer tab selection does not change.
6. Press Cmd+W on the only pane in the second HRDX tab. Expected: HRDX closes that tab.
7. Press Cmd+W on the final pane/tab. Expected: HRDX leaves it running and displays `last pane cannot be closed`.

Exit HRDX normally after observing all outcomes.

- [ ] **Step 8: Clean build artifacts and commit**

```bash
rm -f hrdx
git add internal/ui/rawinput.go internal/ui/model.go internal/ui/model_test.go README.md
git commit -m "feat: shadow Ghostty tab shortcuts"
```
