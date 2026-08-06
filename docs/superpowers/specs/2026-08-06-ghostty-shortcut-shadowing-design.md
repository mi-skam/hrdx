# Ghostty Shortcut Shadowing Design

## Objective

Give HRDX's tab and pane controls the same direct macOS gestures used in the user's Ghostty configuration, following the working Herdr precedent. Preserve HRDX's existing prefix bindings and Ghostty configuration.

## Current behavior

Ghostty defines these relevant shortcuts:

- `Cmd+W`: close the current Ghostty surface
- `Cmd+T`: create a Ghostty tab
- `Cmd+Shift+H`: select the previous Ghostty tab
- `Cmd+Shift+L`: select the next Ghostty tab

HRDX already enables Kitty keyboard reporting with CSI-u and recovers raw modified-key sequences that Bubble Tea does not expose. Its parser currently names only Shift, Alt, and Ctrl. In terminal mode, HRDX intercepts only Ctrl+B; other raw CSI-u sequences are forwarded to the focused pane.

Herdr demonstrates the intended input path: it requests modern keyboard reporting, represents the Super modifier, parses direct `cmd+…` bindings, and handles them before forwarding input to a child terminal.

## Scope

Add four direct HRDX aliases:

| Gesture | HRDX action |
| --- | --- |
| `Cmd+W` | Close the active HRDX surface according to the hierarchy below |
| `Cmd+T` | Open the existing agent/shell picker to create a new tab |
| `Cmd+Shift+H` | Select the previous HRDX tab, wrapping at the beginning |
| `Cmd+Shift+L` | Select the next HRDX tab, wrapping at the end |

Keep all existing Ctrl+B bindings. Do not add a general keybinding framework or change the Ghostty configuration. Do not add shortcuts for workspaces, splits, zoom, copy/paste, settings, font size, or application lifecycle.

## Input routing

Extend the Kitty modifier constants in `internal/ui/rawinput.go` with the Super bit. Continue using the existing CSI-u parser; it already subtracts Kitty's modifier offset and preserves unknown modifier bits.

In `Model.updateRaw`, after parsing CSI-u and before forwarding to the focused pane:

1. Consider direct HRDX aliases only in `modeTerminal`.
2. Match the exact modifier set, not a subset. Extra Ctrl or Alt modifiers must not trigger an HRDX action.
3. Normalize the reported base letter sufficiently to accept the host terminal's shifted-letter representation for `Cmd+Shift+H/L` without conflating unrelated codepoints.
4. Consume a matched shortcut so it never reaches the child pane.
5. Preserve the current forwarding path for every unmatched sequence, including other Super chords.

Modal input states retain their current ownership. The direct shortcuts do not close panes or switch tabs while a rename input, agent picker, menu, or settings view is active.

## Close hierarchy and invariant

`Cmd+W` performs one semantic close operation:

1. If the active tab has multiple panes, close the focused pane.
2. If the active tab has one pane and the workspace has multiple tabs, close the active tab and its pane.
3. If this is the final pane of the final tab, do not close anything and show `last pane cannot be closed` as transient status.

The same guarded operation must back the existing prefix and menu pane-close paths. No user input path may leave a live workspace with an empty final tab. Workspace close and full-application quit remain separate explicit actions.

Closing a tab must preserve the existing process lifecycle: close each pane terminal, remove the tab, clamp the active-tab index, resize remaining panes, and persist the state once.

## New-tab behavior

`Cmd+T` opens the same agent/shell kind picker used by the existing Ctrl+B,T command. Opening or cancelling the picker does not mutate tabs. Selecting a kind creates and focuses the new tab through the existing path.

## Failure and compatibility behavior

- A malformed CSI-u sequence follows the existing raw-input fallback and is not treated as an HRDX shortcut.
- A matching shortcut with no current workspace or tab is a safe no-op.
- Existing Ctrl+B behavior and child Kitty-key forwarding remain unchanged.
- The feature is terminal-protocol based rather than Ghostty-specific, so another terminal that reports Super through Kitty CSI-u may provide the same shortcuts.
- Ghostty 1.3.1 is the required smoke-test host because the outer terminal determines whether Cmd chords reach HRDX.

## Verification

Add behavioral coverage for:

- CSI-u parsing of Super and Super+Shift modifier sets.
- Exact modifier matching, including rejection of Super+Ctrl and Super+Alt variants.
- `Cmd+W` with multiple panes.
- `Cmd+W` with one pane and multiple tabs.
- `Cmd+W` on the final pane of the final tab, including the status and unchanged process/layout.
- Existing prefix/menu close paths preserving the same final-pane invariant.
- `Cmd+T` opening the tab kind picker without immediately creating a tab.
- `Cmd+Shift+H/L` selecting and wrapping tabs.
- An unrelated Super CSI-u sequence reaching the focused child unchanged.
- Direct aliases not firing in modal input states.

Finally, run HRDX in Ghostty 1.3.1 and exercise all four gestures against multiple panes and tabs. The smoke test must confirm that Ghostty does not perform its outer action for a gesture handled by HRDX.