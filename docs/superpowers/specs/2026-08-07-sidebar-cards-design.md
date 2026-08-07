# Workspace Card Sidebar Design

## Objective

Reimplement HRDX's sidebar as a 30-column, workspace-centric card list based on the approved reference still. Make workspace, Git, pane focus, activity, and attention state readable in one hierarchy without changing the central Bubble Tea state-ownership model.

The implementation will be developed on `feat/sidebar-cards`, created from the synchronized `main` branch after this specification and its implementation plan are approved.

## Current behavior

The sidebar currently renders two overlapping indexes:

- a `WORKSPACES` section containing each workspace, its branch, and its panes
- an `AGENTS` section containing agent panes again across all workspaces

The sidebar is 26 columns wide. It supports workspace and pane selection, workspace drag/reordering, right-click workspace menus, wheel scrolling, new-workspace creation, and pinned settings. Pane state uses static bullets in workspace rows and animated spinners in the global agent index.

Git metadata contains the branch and ahead/behind counts but not worktree cleanliness. Busy detection is agent-oriented. Shell panes are recognized as agents only when a configured agent binary is their foreground command.

## Scope

Replace the two-section sidebar with one card per workspace. Each card contains:

1. workspace name
2. optional Git status line
3. every pane in the workspace, flattened across tabs
4. a separator before the next workspace

The feature also adds:

- a 30-column sidebar content width
- selected-workspace rail and focused-pane row treatments
- consistent static shell and agent glyphs
- explicit active, idle, attention, stopped/unknown, and failure colors
- activity detection for foreground commands in shell panes
- session-local attention tracking for agent and shell completions
- full cached Git clean/dirty and ahead/behind state
- focus-aware sidebar auto-scrolling
- pinned new-workspace and settings actions

## Non-goals

Do not:

- introduce a separate Bubble Tea sidebar component
- add collapsible workspace cards
- group panes under tab subheadings
- change tab, pane, workspace, or persistence data structures
- broaden the external API `busy` contract
- broaden existing finish sounds or desktop notifications to arbitrary shell commands
- persist attention across HRDX restarts or holder reattachment
- add configurable or resizable sidebar width
- add new gestures or move existing context actions to a different menu

## Architecture

Keep the existing sidebar architecture in `internal/ui/model.go`.

`Model` remains the sole owner of workspace, tab, pane, focus, scroll, activity, and attention state. The existing `sidebarRows`, `sidebarHit`, `sidebarOffset`, `renderSidebar`, and `updateMouse` paths remain the rendering and interaction boundary. Rework their row metadata and behavior in place rather than adding a nested model or a second selection state.

Rendering and hit-testing must consume the same flattened row sequence. Every visible row records its semantic kind and owning workspace or pane so clipping, scrolling, clicking, dragging, and context menus cannot drift apart.

All state mutation remains inside `Model.Update` or synchronous methods called from it. Foreground-process observations arrive as Bubble Tea messages before they change activity or attention state.

## Sidebar geometry

Set the sidebar content width to 30 terminal cells. The existing right border remains one additional cell. Update every dependent calculation in lockstep:

- terminal area width
- tab-bar width
- hardware cursor placement
- menu placement
- mouse coordinate translation
- pane layout and resize calculations

The scrollable card viewport excludes two pinned bottom rows:

1. `+ new workspace`
2. `settings`

The pinned rows do not move when cards scroll. At very small terminal heights, row construction and hit-testing must remain bounded and consistent even when no card row fits above the pinned actions.

## Workspace cards

Remove the `WORKSPACES` heading and the global `AGENTS` section.

A card uses these rows:

- workspace name in strong text
- Git status when the workspace is a repository
- one row per pane across every tab
- separator after the card unless it is the final card

Pane order remains the current flat tab order: tab order first, then each tab's pane slice order. Clicking a pane in an inactive tab activates the owning tab before focusing the pane.

The selected workspace receives a cyan rail on every visible content row in its card. The rail does not continue through the separator. When clipping cuts through a selected card, the visible fragment retains the rail.

Only the globally focused pane receives the approved subtle row background. The pane's state glyph keeps its semantic color; focus does not override state color.

## Pane glyphs and visual state

Use static, width-safe, one-cell glyphs:

- `●` for shell panes
- `◆` for agent panes, including recognized agents running inside shell panes

Do not use emoji or animated sidebar glyphs. This avoids ambiguous cell width and fallback-font behavior across macOS, Linux, and Windows terminals.

Resolve pane color with this precedence:

1. **failed — red:** the pane has a startup or runtime failure
2. **active — yellow:** an agent is busy or a shell has a non-shell foreground command
3. **attention — cyan:** the pane completed active work while unfocused
4. **idle — green:** the pane is running and confirmed idle
5. **unknown/stopped — gray:** the pane is starting, exited, detached without a known process state, or foreground lookup failed

Starting new activity clears stale attention so yellow always represents current work. A confirmed active-to-idle transition sets attention only when the pane is not focused. Focusing the pane clears attention through every focus path, including mouse selection, workspace cycling, tab cycling, pane cycling, and activation of a pane from another tab.

## Activity observation

Agent activity continues to use the existing built-in spinner and custom-harness busy heuristics. Agent idle is the confirmed inverse of that heuristic while the pane is running.

Agent active-to-idle attention uses the existing 1.5-second finish confirmation so transient spinner redraw gaps do not turn a still-working pane cyan. A shell transition from a known non-shell foreground command to the known login shell is already process-confirmed and does not require the spinner debounce.

For a shell pane:

- an empty or failed foreground-process lookup is unknown
- the configured login shell as foreground command is idle
- any other foreground command is active, including scripts, builds, SSH, editors, monitors, and agents

Observe activity often enough for the sidebar to update without requiring unrelated keyboard or mouse input. The observation path must not perform unbounded work in `View` or mutate `Model` from a background goroutine. Existing foreground-command caching may be reused, but observations must return to the Bubble Tea loop as messages.

Keep sidebar activity separate from the existing API and notification semantics. Arbitrary shell commands may become yellow and later cyan, but they do not emit agent busy events or trigger finish sounds and desktop notifications.

Attention is ephemeral. Restoring state or reattaching to holder sessions starts without attention flags because HRDX cannot prove an active-to-idle transition it did not observe.

## Git status line

Extend cached workspace Git metadata with an explicit cleanliness state:

- clean
- dirty
- unknown

Dirty includes staged changes, unstaged changes, and untracked files. Keep the existing branch resolution for normal branches, worktrees, submodules, and detached HEADs. Preserve timeout-bounded Git subprocesses and the existing short cache window so rendering cannot hang indefinitely.

Render tokens in this order:

1. branch or detached short hash
2. `clean` or `dirty` when known
3. `ahead N` when nonzero
4. `behind N` when nonzero

Examples:

```text
main · clean
feature/x · dirty · ahead 2
main · clean · ahead 1 · behind 3
```

If cleanliness or upstream comparison fails, omit only the unknown token. Never claim `clean` after an error or timeout. A non-repository omits the Git row entirely.

Truncate the composed line by display-cell width after reserving card indentation and rail width.

## Scrolling and focus visibility

Wheel input over the sidebar scrolls only the card viewport. Preserve one-row wheel movement and bounded offsets.

Keyboard or API focus changes adjust `sideScroll` by the smallest amount required to reveal the focused pane. Keep the selected workspace header visible when the header and focused pane fit in the viewport together. If a single workspace card is taller than the viewport, prioritize the focused pane.

Retain discoverable top and bottom overflow markers when content is clipped. Markers replace only visible presentation rows; hit-testing continues to resolve against the underlying row sequence and offset.

## Mouse and context behavior

Preserve these interactions:

- clicking a workspace name or Git row selects the workspace and its current pane
- clicking a pane row selects its workspace, activates its tab, and focuses it
- clicking either pinned action opens the existing new-workspace or settings flow
- wheel input scrolls cards
- right-clicking any card row opens the owning workspace's existing context menu

A workspace drag may begin on any row in its card. Motion across any row in another card reorders against that workspace. A press and release without crossing into another workspace remains a normal click and does not persist an order change.

Dragging and context menus continue to use the shared row metadata rather than re-deriving workspace ownership from labels or visual positions.

## Failure and compatibility behavior

- No workspace: render an empty card viewport with both pinned actions available.
- Non-Git workspace: omit the Git row.
- Git timeout or parse error: retain known branch data and omit unknown status tokens.
- Foreground-process lookup failure: render gray and do not synthesize an activity transition.
- Pane exit: clear stale activity tracking and render gray until the pane is removed or restarted.
- Pane deletion: remove its activity and attention entries.
- Workspace reorder: retain attention by pane ID rather than visual position.
- Narrow height: clamp card viewport, scroll offset, and hit targets without panic.
- Existing saved state remains compatible because no serialized fields are added.
- Existing public API status, busy events, sound behavior, and notification behavior remain compatible.

## Expected code areas

Primary implementation areas:

- `internal/ui/model.go`
  - model fields and activity messages
  - sidebar rows, rendering, hit-testing, scrolling, focus reveal, and mouse behavior
  - pane state glyphs and attention transitions
- `internal/ui/git.go`
  - cleanliness state and bounded Git status parsing
- `internal/ui/model_test.go`
  - card rendering, state, scrolling, hit-testing, drag, and focus behavior
- existing Git-related UI tests
  - clean/dirty/ahead/behind and failure cases

Do not create a new sidebar component file. The approved design keeps the current `Model`-owned sidebar architecture.

## Verification

Add behavioral coverage for:

- workspace card row order and separator placement
- removal of the global `AGENTS` section and duplicate pane rows
- flat pane ordering across tabs
- pane click activating an inactive owning tab
- selected rail span, focused-row styling, and semantic glyph width
- state precedence: failed, active, attention, idle, and unknown/stopped
- agent and shell active-to-idle attention transitions
- no attention transition while focused or through unknown process state
- attention clearing through all mouse and keyboard focus paths
- attention cleanup after pane deletion
- shell activity classification for login shell, arbitrary foreground command, and lookup failure
- fixed 30-column sidebar and all dependent coordinate calculations
- pinned action rendering and hit targets during card overflow
- one-row wheel scrolling and bounded offsets
- automatic focus reveal, including a card taller than the viewport
- drag/reorder beginning from each card-row kind
- workspace context menus from each card-row kind
- Git clean, dirty, ahead, behind, diverged, detached, non-repository, timeout, and malformed-output cases
- unchanged external API busy and finish-notification behavior

Run formatting, UI package tests, the full Go test suite, vet, and race tests. Then launch HRDX in a real terminal and exercise:

1. multiple workspaces, tabs, and panes
2. card scrolling and keyboard focus reveal
3. workspace dragging and context menus
4. long branch and workspace names
5. clean, dirty, ahead, behind, and non-Git workspaces
6. idle and active shells
7. busy, idle, attention, failed, and exited agent panes
8. new-workspace and settings actions while cards overflow

The smoke test must confirm that the sidebar remains 30 cells wide, the main terminal receives the remaining width correctly, pane focus and mouse coordinates stay aligned, and no interaction requires the removed global agent index.