package update

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVersionLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.0.1", "0.0.2", true},
		{"0.0.2", "0.0.1", false},
		{"0.0.99", "0.1.0", true},
		{"0.1.0", "0.0.99", false},
		{"0.0.5", "0.0.5", false},
		{"v0.0.1", "v0.0.2", true},
		{"0.0.5 (abc1234, 2026-01-01)", "0.0.6", true},
		{"1.0.0", "0.9.9", false},
	}
	for _, c := range cases {
		if got := VersionLess(c.a, c.b); got != c.want {
			t.Errorf("VersionLess(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestVersionOnly(t *testing.T) {
	if got := VersionOnly("0.0.5 (43da5e5, 2026-05-12)"); got != "0.0.5" {
		t.Fatalf("VersionOnly = %q, want 0.0.5", got)
	}
	if got := VersionOnly("0.0.5"); got != "0.0.5" {
		t.Fatalf("VersionOnly = %q, want 0.0.5", got)
	}
}

func TestCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, checkFile)
	want := cache{
		CheckedAt: time.Now().UTC().Truncate(time.Second),
		CurrentAt: "0.0.1",
		Latest:    "v0.0.2",
		URL:       "https://example.com",
	}
	if err := writeCache(path, want); err != nil {
		t.Fatal(err)
	}
	got, ok := readCache(path)
	if !ok || got.Latest != want.Latest || got.CurrentAt != want.CurrentAt {
		t.Fatalf("readCache = %+v/%v, want %+v", got, ok, want)
	}
}

func TestReadCacheMissingOrBad(t *testing.T) {
	if _, ok := readCache(filepath.Join(t.TempDir(), "nope.json")); ok {
		t.Fatal("missing file should be a cache miss")
	}
	bad := filepath.Join(t.TempDir(), checkFile)
	if err := os.WriteFile(bad, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readCache(bad); ok {
		t.Fatal("bad json should be a cache miss")
	}
}

func TestCheckSkipsDevBuild(t *testing.T) {
	// Must not hit the network; dev builds return the zero value fast.
	info := Check(t.Context(), t.TempDir(), "0.0.0")
	if info.Available {
		t.Fatal("dev build must never report an update")
	}
}

func TestLookupChecksum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checksums.txt")
	content := "abc123  hrdx_0.0.2_darwin_arm64.tar.gz\ndef456  hrdx_0.0.2_linux_amd64.tar.gz\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := lookupChecksum(path, "hrdx_0.0.2_linux_amd64.tar.gz")
	if err != nil || got != "def456" {
		t.Fatalf("lookupChecksum = %q/%v, want def456", got, err)
	}
	if _, err := lookupChecksum(path, "missing.tar.gz"); err == nil {
		t.Fatal("missing asset should error")
	}
}

func TestReleaseAssetNameFor(t *testing.T) {
	tests := []struct {
		goos, goarch string
		want         string
	}{
		{"linux", "amd64", "hrdx_1.2.3_linux_amd64.tar.gz"},
		{"darwin", "arm64", "hrdx_1.2.3_darwin_arm64.tar.gz"},
		{"windows", "amd64", "hrdx_1.2.3_windows_amd64.zip"},
		{"windows", "arm64", "hrdx_1.2.3_windows_arm64.zip"},
	}
	for _, tt := range tests {
		got, err := releaseAssetNameFor("1.2.3", tt.goos, tt.goarch)
		if err != nil || got != tt.want {
			t.Errorf("releaseAssetNameFor(%q, %q) = %q, %v; want %q", tt.goos, tt.goarch, got, err, tt.want)
		}
	}
	if _, err := releaseAssetNameFor("1.2.3", "freebsd", "amd64"); err == nil {
		t.Error("unsupported OS should fail")
	}
	if _, err := releaseAssetNameFor("1.2.3", "windows", "386"); err == nil {
		t.Error("unsupported architecture should fail")
	}
}

func TestExtractZipFindsWindowsExecutable(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "release.zip")
	writeZip(t, archive, map[string]string{
		"README.md": "release notes",
		"hrdx.exe":  "new executable",
	})
	dst := filepath.Join(dir, "out")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := extractArchive(archive, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "hrdx.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new executable" {
		t.Fatalf("extracted content = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dst, "README.md")); !os.IsNotExist(err) {
		t.Fatal("extractor should only write hrdx.exe")
	}
}

func TestExtractZipRequiresWindowsExecutable(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "release.zip")
	writeZip(t, archive, map[string]string{"hrdx": "wrong platform binary"})
	if err := extractZip(archive, dir); err == nil {
		t.Fatal("ZIP without hrdx.exe should fail")
	}
}

func TestReplaceBinaryWindowsStagesAndKeepsBackup(t *testing.T) {
	dir := t.TempDir()
	cur := filepath.Join(dir, "hrdx.exe")
	newBin := filepath.Join(t.TempDir(), "hrdx.exe") // exercise cross-volume-safe staging path
	if err := os.WriteFile(cur, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cur+".old", []byte("stale backup"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newBin, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceBinaryWindows(cur, newBin); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, cur, "new")
	assertFileContent(t, cur+".old", "old")
	assertFileContent(t, newBin, "new")
}

func writeZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s content = %q, want %q", path, got, want)
	}
}
