# Workspace Card Sidebar Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the duplicated two-section sidebar with a 30-column workspace-card sidebar that exposes Git state, pane activity, attention, and focus while preserving existing interactions.

**Architecture:** Keep sidebar state and mutation in `ui.Model`. Rework the existing flattened row builder, renderer, hit-testing, and mouse paths in `internal/ui/model.go`; extend the existing cached Git metadata in `internal/ui/git.go`; derive styles from the current theme palette in `internal/ui/styles.go`.

**Tech Stack:** Go 1.25+, Bubble Tea, Lip Gloss, Unix/Windows foreground-process adapters, standard `os/exec` Git integration.

## Global Constraints

- Keep every UI mutation inside the Bubble Tea update loop.
- Keep the sidebar content width fixed at exactly 30 terminal cells plus its existing one-cell right border.
- Preserve serialized state compatibility; activity and attention are ephemeral.
- Preserve external API busy events and existing finish-sound/desktop-notification semantics.
- Git and foreground-process checks must be timeout-bounded or use existing bounded caches; do not add blocking work to `View`.
- Use display-cell width for clipping and padding.
- Keep shell `●` and agent `◆` glyphs static and one cell wide.
- Do not create a separate sidebar component or collapsible cards.

---

### Task 1: Full Cached Git State

**Files:**
- Modify: `internal/ui/git.go:13-95`
- Modify: `internal/ui/model_test.go:525-546`

**Interfaces:**
- Produces: `type gitWorktreeState int` with `gitStateUnknown`, `gitStateClean`, and `gitStateDirty`.
- Produces: `func parseGitStatus(output []byte) (state gitWorktreeState, ahead, behind int)`.
- Produces: `func readGitStatus(cwd string) (state gitWorktreeState, ahead, behind int)`.
- Extends: `branchInfo` with `state gitWorktreeState`; existing `value`, `ahead`, `behind`, and `checked` remain.
- Removes: `readGitAheadBehind`; callers use `readGitStatus`.

- [ ] **Step 1: Add parser tests that fail**

Add table-driven coverage in `internal/ui/model_test.go`:

```go
func TestParseGitStatus(t *testing.T) {
	tests := []struct {
		name          string
		output        string
		state         gitWorktreeState
		ahead, behind int
	}{
		{"clean", "# branch.oid abc\n# branch.head main\n", gitStateClean, 0, 0},
		{"dirty tracked", "# branch.head main\n1 .M N... 100644 100644 100644 abc abc file\n", gitStateDirty, 0, 0},
		{"dirty untracked", "# branch.head main\n? new.txt\n", gitStateDirty, 0, 0},
		{"diverged", "# branch.head main\n# branch.ab +2 -3\n", gitStateClean, 2, 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, ahead, behind := parseGitStatus([]byte(test.output))
			if state != test.state || ahead != test.ahead || behind != test.behind {
				t.Fatalf("got %v/%d/%d, want %v/%d/%d", state, ahead, behind, test.state, test.ahead, test.behind)
			}
		})
	}
}
```

- [ ] **Step 2: Run the parser test and verify red**

Run: `go test ./internal/ui -run 'TestParseGitStatus'`

Expected: compile failure because `gitWorktreeState` and `parseGitStatus` do not exist.

- [ ] **Step 3: Implement one bounded Git status command**

Use `git status --porcelain=v2 --branch --untracked-files=normal`. Parse `# branch.ab +N -N`; any non-header record means dirty. A successful empty record set is clean. Command lookup, timeout, or parse failure returns `gitStateUnknown` and zero counts.

```go
type gitWorktreeState int

const (
	gitStateUnknown gitWorktreeState = iota
	gitStateClean
	gitStateDirty
)

func parseGitStatus(output []byte) (gitWorktreeState, int, int) {
	state := gitStateClean
	var ahead, behind int
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "# branch.ab ") {
			fields := strings.Fields(line)
			if len(fields) != 4 {
				return gitStateUnknown, 0, 0
			}
			ahead, _ = strconv.Atoi(strings.TrimPrefix(fields[2], "+"))
			behind, _ = strconv.Atoi(strings.TrimPrefix(fields[3], "-"))
			continue
		}
		if !strings.HasPrefix(line, "# ") {
			state = gitStateDirty
		}
	}
	return state, ahead, behind
}
```

Keep the existing 500 ms timeout. Populate `branchInfo.state`, `ahead`, and `behind` only when `readGitBranch` found a repository.

- [ ] **Step 4: Run Git tests and verify green**

Run: `go test ./internal/ui -run 'Test(ParseGitStatus|GitBranchDetection)'`

Expected: PASS.

- [ ] **Step 5: Commit Git state support**

```bash
git add internal/ui/git.go internal/ui/model_test.go
git commit -m "feat: report full workspace git state"
```

---

### Task 2: Pane Activity and Attention State

**Files:**
- Modify: `internal/ui/model.go:130-180,298-348,700-790,1840-1877,2613-2720`
- Modify: `internal/ui/model_test.go`

**Interfaces:**
- Produces: `type paneActivityState struct { active bool; known bool; agent bool }`.
- Produces: `type paneActivityObservedMsg struct { panes map[int]paneActivityState }`.
- Produces: `func (m Model) pollPaneActivity() tea.Cmd`.
- Produces: `func (m *Model) applyPaneActivity(states map[int]paneActivityState)`.
- Produces: `func (m *Model) clearFocusedAttention()`.
- Adds Model fields: `paneActivity map[int]paneActivityState`, `paneAttention map[int]bool`.
- Keeps: `paneBusy`, `trackBusy`, API busy events, sounds, and notifications agent-oriented.

- [ ] **Step 1: Add failing transition and precedence tests**

Construct panes without real PTYs and exercise the pure state application path:

```go
func TestPaneActivitySetsAndClearsAttention(t *testing.T) {
	model := newTestModel("/tmp/api", "/tmp/web")
	first := model.spaces[0].tab().panes[0]
	second := model.spaces[1].tab().panes[0]
	model.selected = 0

	model.applyPaneActivity(map[int]paneActivityState{
		first.id: {active: false, known: true},
		second.id: {active: true, known: true},
	})
	model.applyPaneActivity(map[int]paneActivityState{
		first.id: {active: false, known: true},
		second.id: {active: false, known: true},
	})
	if !model.paneAttention[second.id] {
		t.Fatal("unfocused active-to-idle transition did not set attention")
	}
	model.selected = 1
	model.clearFocusedAttention()
	if model.paneAttention[second.id] {
		t.Fatal("focusing pane did not clear attention")
	}
}

func TestUnknownActivityDoesNotCreateAttention(t *testing.T) {
	model := newTestModel("/tmp/api")
	target := model.currentPane()
	model.applyPaneActivity(map[int]paneActivityState{target.id: {active: true, known: true}})
	model.applyPaneActivity(map[int]paneActivityState{target.id: {known: false}})
	if model.paneAttention[target.id] {
		t.Fatal("unknown observation created attention")
	}
}
```

Add a table test for visual precedence: failure red; active yellow; attention cyan; known idle green; unknown/exited gray.

- [ ] **Step 2: Run activity tests and verify red**

Run: `go test ./internal/ui -run 'Test(PaneActivity|UnknownActivity|PaneVisualState)'`

Expected: compile failure for missing activity types and methods.

- [ ] **Step 3: Add activity fields and polling messages**

Initialize both maps in `New`. Start a recurring activity tick from `Init`. On each tick, return a command that snapshots every pane's state and emits `paneActivityObservedMsg`; schedule the next tick separately so the update loop never blocks on foreground lookup.

Classify activity as follows:

```go
func (m Model) observePaneActivity(target *pane) paneActivityState {
	if target == nil || target.term == nil || !target.running || target.failure != "" {
		return paneActivityState{}
	}
	if isAgentKind(target.kind) {
		return paneActivityState{active: m.paneBusy(target), known: true, agent: true}
	}
	foreground := target.term.ForegroundCommand()
	if foreground == "" {
		return paneActivityState{}
	}
	if kind := m.paneAgentKind(target); kind != "" {
		return paneActivityState{active: m.paneBusy(target), known: true, agent: true}
	}
	return paneActivityState{
		active: foreground != filepath.Base(m.config.Shell),
		known:  true,
	}
}
```

Use a two-second poll interval to match `ForegroundCommand`'s cache TTL.

- [ ] **Step 4: Apply transitions without changing public busy semantics**

`applyPaneActivity` clears attention when new activity starts. It creates immediate attention only for confirmed non-agent active-to-idle transitions while unfocused. Agent transitions continue through `soundConfirmMsg`; make `trackBusy` schedule that confirmation even when sounds and notifications are disabled, and set attention there after rechecking idle state.

Call `clearFocusedAttention` from `focusPane`, `selectSpace`, `selectTab`, and `cyclePane`. Delete pane IDs from both maps in `removePane`.

- [ ] **Step 5: Run activity and existing notification tests**

Run: `go test ./internal/ui -run 'Test(PaneActivity|UnknownActivity|PaneVisualState|TrackBusy)'`

Expected: PASS, including unchanged sound debounce behavior.

- [ ] **Step 6: Commit activity state**

```bash
git add internal/ui/model.go internal/ui/model_test.go
git commit -m "feat: track pane activity and attention"
```

---

### Task 3: Workspace Card Rendering

**Files:**
- Modify: `internal/ui/styles.go:20-78`
- Modify: `internal/ui/model.go:31,1840-2010,2131-2214,2480-2520`
- Modify: `internal/ui/model_test.go:93-200`
- Modify: `internal/ui/settings_test.go:60-70`

**Interfaces:**
- Changes: `sidebarWidth` from `26` to `30`.
- Keeps: `type sidebarRow` as the shared render/hit record, extending fields only when needed for card ownership.
- Produces: `func (m Model) paneSidebarState(*pane) paneSidebarState` and a static glyph renderer.
- Changes: `sidebarRows` returns card content only; pinned actions are handled by `renderSidebar` and `sidebarHit`.

- [ ] **Step 1: Replace old row-layout tests with failing card contracts**

Cover exact semantic row order rather than ANSI byte strings:

```go
func TestSidebarRowsAreWorkspaceCards(t *testing.T) {
	model := newTestModel("/tmp/api", "/tmp/web")
	rows := model.sidebarRows()
	if len(rows) != 5 { // api, pane, separator, web, pane
		t.Fatalf("rows = %d, want 5", len(rows))
	}
	if rows[0].kind != "space" || rows[1].kind != "pane" || rows[2].kind != "" ||
		rows[3].kind != "space" || rows[4].kind != "pane" {
		t.Fatalf("unexpected card row kinds: %+v", rows)
	}
	for _, row := range rows {
		if strings.Contains(row.label, "WORKSPACES") || strings.Contains(row.label, "AGENTS") {
			t.Fatalf("legacy section survived: %q", row.label)
		}
	}
}
```

Add tests for: optional Git row; `●` shell versus `◆` agent; static glyphs; selected rail on every selected-card content row but not separator; focused row padded to 30 cells before background styling; no duplicated pane rows.

- [ ] **Step 2: Run card tests and verify red**

Run: `go test ./internal/ui -run 'TestSidebar(RowsAreWorkspaceCards|GitLine|Glyphs|SelectedRail|FocusedRow)'`

Expected: failures showing legacy headings, duplicate agent rows, and 26-column geometry.

- [ ] **Step 3: Add theme-derived sidebar styles**

Add styles for attention and focused rows in `styles.go`:

```go
styleDotAttention = lipgloss.NewStyle().Foreground(colorAccent)
stylePaneFocus = lipgloss.NewStyle().Background(colorFaint).Foreground(colorBarFg)
```

Continue using `styleDotBusy` for yellow, `styleDotOn` for green, and `styleDotOff` for red. Gray uses `stylePaneDim`.

- [ ] **Step 4: Rebuild `sidebarRows` as cards**

For each workspace append name, optional Git, flat panes, and a separator except after the last workspace. Compose the Git line as branch, known cleanliness, ahead, and behind tokens joined by ` · `. Use `ansiCut`/Lip Gloss width to clip by display cells.

Every selected card content row starts with the cyan rail; every other card uses the same-width blank prefix. Render shell `●` and agent `◆`. Pad a focused pane row to exactly 30 cells before applying `stylePaneFocus` so the background spans the card width.

- [ ] **Step 5: Pin actions and update narrow-height behavior**

Reserve the last two body rows for `+ new workspace` and `settings`. Remove the trailing blank row. `sidebarOffset` uses `max(0, height-2)` as its card viewport. `sidebarHit` returns `new` and `settings` for the two fixed rows before mapping card offsets.

Update `settings_test.go` to click the final body row. Add an overflow test proving both pinned actions stay present while card rows scroll.

- [ ] **Step 6: Run rendering and settings tests**

Run: `go test ./internal/ui -run 'Test(Sidebar|SettingsSidebarEntry)'`

Expected: PASS.

- [ ] **Step 7: Commit card rendering**

```bash
git add internal/ui/styles.go internal/ui/model.go internal/ui/model_test.go internal/ui/settings_test.go
git commit -m "feat: render workspace card sidebar"
```

---

### Task 4: Focus-Reveal and Card Interactions

**Files:**
- Modify: `internal/ui/model.go:1473-1758,1763-1830,1984-2010,2613-2662`
- Modify: `internal/ui/model_test.go:188-269`

**Interfaces:**
- Produces: `func (m *Model) revealFocusedPane()`.
- Keeps: `moveSpaceTo`, `sidebarHit`, `sideScroll`, and existing workspace context menu APIs.
- Changes: a left press on any `space` or `pane` row arms workspace drag; pane rows also perform their normal focus action.

- [ ] **Step 1: Add failing focus-reveal tests**

Create a small-height model with several workspaces and assert selection changes produce the minimal offset that exposes the focused pane. Add a workspace with more pane rows than the viewport and assert the pane takes priority over the card header.

```go
func TestSidebarRevealFollowsKeyboardFocus(t *testing.T) {
	model := newTestModel("/tmp/a", "/tmp/b", "/tmp/c")
	model.height = 7
	model.selectSpace(2)
	kind, index, _ := model.sidebarHit(model.height - 4)
	if kind == "" || index != 2 {
		t.Fatalf("selected workspace not revealed: %q/%d offset=%d", kind, index, model.sideScroll)
	}
}
```

- [ ] **Step 2: Add failing drag and pane-tab tests**

Cover drag start from name, Git, and pane rows. Verify a pane row press still activates the pane's owning inactive tab. Verify right-click on all card row kinds opens the workspace menu.

- [ ] **Step 3: Run interaction tests and verify red**

Run: `go test ./internal/ui -run 'TestSidebar(Reveal|Drag|PaneClick|Context)'`

Expected: failures for unchanged scroll position and pane rows not arming drag.

- [ ] **Step 4: Implement minimal focus reveal**

Find the selected workspace header row and focused pane row in `sidebarRows`. Compute the card viewport height. Move `sideScroll` only when either required row is outside the viewport. If both cannot fit, reveal the pane. Clamp through `sidebarOffset`.

Invoke after `selectSpace`, `selectTab`, `cyclePane`, `focusPane`, and direct workspace selection in mouse handling.

- [ ] **Step 5: Extend drag arming without breaking clicks**

On left press for `space` and `pane` rows, set `dragSpace` to the owner. Pane rows first activate/focus their pane. A release without crossing a workspace clears drag state without persisting. Existing motion and context-menu paths continue to resolve ownership through `sidebarHit`.

- [ ] **Step 6: Run interaction tests**

Run: `go test ./internal/ui -run 'Test(Sidebar|MoveSpace|MouseSelectsSpace)'`

Expected: PASS.

- [ ] **Step 7: Commit interactions**

```bash
git add internal/ui/model.go internal/ui/model_test.go
git commit -m "feat: preserve sidebar card interactions"
```

---

### Task 5: Integration Verification and Smoke Test

**Files:**
- Modify only if verification exposes a real defect in the approved behavior.

**Interfaces:**
- Consumes all previous tasks.
- Produces a buildable, tested `feat/sidebar-cards` branch.

- [ ] **Step 1: Format changed Go files**

Run: `gofmt -w internal/ui/git.go internal/ui/model.go internal/ui/model_test.go internal/ui/settings_test.go internal/ui/styles.go`

- [ ] **Step 2: Run focused package verification**

Run: `go test ./internal/ui`

Expected: PASS.

- [ ] **Step 3: Run project verification**

Run in order:

```bash
go vet ./...
go test ./...
go test -race ./...
test -z "$(gofmt -l .)"
git diff --check
```

Expected: every command exits zero.

- [ ] **Step 4: Build and launch HRDX**

Run: `make build`

Launch the built binary in a managed PTY with fresh state and at least three temporary workspaces. Exercise card scrolling, pane/workspace focus, new workspace, settings, right-click menus, and workspace dragging. Confirm the sidebar occupies 30 content cells plus one border and pane mouse/cursor coordinates remain aligned.

- [ ] **Step 5: Exercise live state transitions**

In shell panes, run a long command and confirm yellow active → cyan attention while unfocused → green idle after focus. Exercise an agent pane and confirm the same visual transition uses the existing 1.5-second completion confirmation. Make a repository dirty and confirm `dirty`; restore it and confirm `clean` after cache refresh.

- [ ] **Step 6: Commit any verification fixes**

If Step 3–5 required code changes:

```bash
git add internal/ui
git commit -m "fix: harden workspace card sidebar"
```

If no code changed, do not create an empty commit.
