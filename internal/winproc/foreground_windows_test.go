//go:build windows

package winproc

import "testing"

func proc(pid uint32, name string) process { return process{pid: pid, name: name} }

func TestForegroundNameKeepsTopLevelAgentWhenItHasHelpers(t *testing.T) {
	tree := map[uint32][]process{
		1:  {proc(10, "codex")},
		10: {proc(11, "rg")},
		11: {proc(12, "git")},
	}
	if got := foregroundName(tree, 1, noCreationTime); got != "codex" {
		t.Fatalf("foregroundName() = %q, want top-level agent %q", got, "codex")
	}
}

func TestForegroundNameSupportsBuiltInAndCustomHarnesses(t *testing.T) {
	tests := []struct {
		name    string
		harness string
		helper  string
	}{
		{name: "claude", harness: "claude", helper: "node"},
		{name: "pi", harness: "pi", helper: "git"},
		{name: "zot", harness: "zot", helper: "zot-tool"},
		{name: "custom native", harness: "acme-agent", helper: "python"},
		{name: "custom runtime", harness: "python", helper: "rg"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tree := map[uint32][]process{
				1:  {proc(10, test.harness)},
				10: {proc(11, test.helper)},
			}
			if got := foregroundName(tree, 1, noCreationTime); got != test.harness {
				t.Fatalf("foregroundName() = %q, want %q", got, test.harness)
			}
		})
	}
}

func TestForegroundNameSkipsShellLaunchers(t *testing.T) {
	tree := map[uint32][]process{
		1:  {proc(10, "cmd")},
		10: {proc(11, "pwsh")},
		11: {proc(12, "claude")},
		12: {proc(13, "helper")},
	}
	if got := foregroundName(tree, 1, noCreationTime); got != "claude" {
		t.Fatalf("foregroundName() = %q, want %q", got, "claude")
	}
}

func TestForegroundNameChoosesNewestTopLevelCommand(t *testing.T) {
	tree := map[uint32][]process{
		1: {
			proc(10, "old-background-job"),
			proc(20, "codex"),
		},
		20: {proc(21, "helper")},
	}
	created := func(pid uint32) uint64 {
		return map[uint32]uint64{10: 100, 20: 200}[pid]
	}
	if got := foregroundName(tree, 1, created); got != "codex" {
		t.Fatalf("foregroundName() = %q, want newest command %q", got, "codex")
	}
}

func TestForegroundNameIdleOrLauncherOnlyIsEmpty(t *testing.T) {
	tests := []struct {
		name string
		tree map[uint32][]process
	}{
		{name: "idle", tree: map[uint32][]process{}},
		{name: "launcher only", tree: map[uint32][]process{1: {proc(10, "cmd")}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := foregroundName(test.tree, 1, noCreationTime); got != "" {
				t.Fatalf("foregroundName() = %q, want empty", got)
			}
		})
	}
}

func TestExeBaseName(t *testing.T) {
	for input, want := range map[string]string{
		`C:\\Tools\\CODEX.EXE`: "codex",
		`Claude.CmD`:           "claude",
		`Pi.COM`:               "pi",
		`Zot`:                  "zot",
	} {
		if got := exeBaseName(input); got != want {
			t.Errorf("exeBaseName(%q) = %q, want %q", input, got, want)
		}
	}
}

func noCreationTime(uint32) uint64 { return 0 }
