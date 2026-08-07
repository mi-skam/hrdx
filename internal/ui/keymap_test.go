package ui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestBuildPrefixKeysDefaults(t *testing.T) {
	keys := buildPrefixKeys(nil)
	for key, action := range map[string]string{
		"c": "picker-right", "a": "agent-right", "A": "agent-down",
		"/": "find", "tab": "pane-next", "q": "quit",
	} {
		if keys[key] != action {
			t.Fatalf("keys[%q] = %q, want %q", key, keys[key], action)
		}
	}
}

func TestBuildPrefixKeysOverride(t *testing.T) {
	keys := buildPrefixKeys(map[string]string{"find": "f", "agent-cycle": "g"})
	if keys["f"] != "find" || keys["g"] != "agent-cycle" {
		t.Fatalf("overrides not applied: %v", keys)
	}
	if keys["/"] == "find" {
		t.Fatal("override should replace the default key")
	}
}

func TestBuildPrefixKeysExcludesPrefixTrigger(t *testing.T) {
	keys := buildPrefixKeys(nil)
	if keys["ctrl+b"] != "literal" {
		t.Fatalf(`keys["ctrl+b"] = %q, want "literal" ("prefix" must not collide with it)`, keys["ctrl+b"])
	}
	keys = buildPrefixKeys(map[string]string{"prefix": "ctrl+a"})
	if _, ok := keys["ctrl+a"]; ok {
		t.Fatal(`overriding "prefix" must not add an in-prefix-mode dispatch entry`)
	}
}

func TestPrimaryKeyPrefixOverride(t *testing.T) {
	model := newTestModel("/tmp/api")
	if got := model.primaryKey("prefix"); got != "ctrl+b" {
		t.Fatalf("default prefix trigger = %q, want ctrl+b", got)
	}
	model.keyOverrides = map[string]string{"prefix": "ctrl+a"}
	if got := model.primaryKey("prefix"); got != "ctrl+a" {
		t.Fatalf("overridden prefix trigger = %q, want ctrl+a", got)
	}
}

func TestRawCSIUUsesRemappedPrefix(t *testing.T) {
	model := newTestModel("/tmp/api")
	model.prefixTrigger = "ctrl+a"

	updated, _ := model.updateRaw([]byte("\x1b[98;5u"))
	model = updated.(Model)
	if model.mode != modeTerminal {
		t.Fatal("old ctrl+b CSI-u sequence still entered prefix mode")
	}

	updated, _ = model.updateRaw([]byte("\x1b[97;5u"))
	model = updated.(Model)
	if model.mode != modePrefix {
		t.Fatal("remapped ctrl+a CSI-u sequence did not enter prefix mode")
	}
}

func TestIsSpuriousModifierKey(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.KeyMsg
		want bool
	}{
		{"bare nul", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{0}}, true},
		{"bare nul with alt", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{0}, Alt: true}, true},
		{"real rune", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}, false},
		{"multi rune paste-like", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{0, 'a'}}, false},
		{"named ctrl key", tea.KeyMsg{Type: tea.KeyCtrlAt}, false},
	}
	for _, c := range cases {
		if got := isSpuriousModifierKey(c.msg); got != c.want {
			t.Errorf("%s: isSpuriousModifierKey = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestLoadKeymap(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, keymapFile),
		[]byte(`{"find": "f", "bogus": "x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	overrides, problem := loadKeymap(dir)
	if overrides["find"] != "f" {
		t.Fatalf("overrides = %v, want find -> f", overrides)
	}
	if _, ok := overrides["bogus"]; ok {
		t.Fatal("unknown action must be dropped")
	}
	if problem == "" {
		t.Fatal("unknown action should surface a problem message")
	}
}

func TestPrefixAgentSplit(t *testing.T) {
	model := newTestModel("/tmp/api")

	updated, _ := model.updateKey(tea.KeyMsg{Type: tea.KeyCtrlB})
	model = updated.(Model)
	updated, _ = model.updateKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	model = updated.(Model)

	currentTab := model.spaces[0].tab()
	if len(currentTab.panes) != 2 {
		t.Fatalf("panes = %d, want 2 after ctrl+b a", len(currentTab.panes))
	}
	if kind := currentTab.panes[1].kind; kind != model.config.DefaultAgent {
		t.Fatalf("new pane kind = %q, want default agent %q", kind, model.config.DefaultAgent)
	}
}

func TestPrefixHintEntriesUseSpaceSeparator(t *testing.T) {
	model := newTestModel("/tmp/api")
	for _, entry := range model.prefixHintEntries() {
		for _, r := range entry[0] {
			if r == '/' && entry[1] != "find" {
				t.Fatalf("hint keys %q contain a slash separator", entry[0])
			}
		}
	}
}
