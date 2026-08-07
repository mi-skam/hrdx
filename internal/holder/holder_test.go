package holder

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// testShell returns a shell and args that run script as one inline
// command: /bin/sh on unix, PowerShell on Windows. Scripts using only
// "sleep N" and "exit N" run verbatim on both (PowerShell ships a "sleep"
// alias for Start-Sleep and a C-like exit statement); anything else needs
// a matching windowsScript.
func testShell(script string) (string, []string) {
	if runtime.GOOS != "windows" {
		return "/bin/sh", []string{"-c", script}
	}
	return "powershell.exe", []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script}
}

// killSessionAndWait kills session and waits for the holder to confirm it
// is gone. Windows locks a running process's working directory, so any
// test that leaves a session running past its own return must kill it
// (and wait for the OS to actually release the handle) before t.TempDir's
// cleanup tries to remove that directory.
func killSessionAndWait(t *testing.T, client *Client, session int64) {
	t.Helper()
	client.Kill(session)
	deadline := time.Now().Add(5 * time.Second)
	for {
		sessions, _ := client.List()
		if len(sessions) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("session not removed: %+v", sessions)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// testEnv returns the env passed to test sessions. The unix PATH override
// is meaningless on Windows, where PowerShell needs its normal host
// environment (SystemRoot etc.) to start at all.
func testEnv() []string {
	if runtime.GOOS != "windows" {
		return []string{"PATH=/bin:/usr/bin"}
	}
	return nil
}

func TestRing(t *testing.T) {
	r := newRing(8)
	r.Write([]byte("abc"))
	if got := string(r.Bytes()); got != "abc" {
		t.Fatalf("ring = %q, want abc", got)
	}
	r.Write([]byte("defgh"))
	if got := string(r.Bytes()); got != "abcdefgh" {
		t.Fatalf("ring = %q, want abcdefgh", got)
	}
	// Overflow evicts the oldest bytes.
	r.Write([]byte("XY"))
	if got := string(r.Bytes()); got != "cdefghXY" {
		t.Fatalf("ring = %q, want cdefghXY", got)
	}
	// A write larger than the buffer keeps only the tail.
	r.Write([]byte("0123456789"))
	if got := string(r.Bytes()); got != "23456789" {
		t.Fatalf("ring = %q, want 23456789", got)
	}
}

func TestFrameRoundTrip(t *testing.T) {
	var buffer bytes.Buffer
	in := frame{Type: TOut, ID: 42, Payload: []byte("hello")}
	if err := writeFrame(&buffer, in); err != nil {
		t.Fatal(err)
	}
	out, err := readFrame(&buffer)
	if err != nil {
		t.Fatal(err)
	}
	if out.Type != in.Type || out.ID != in.ID || string(out.Payload) != "hello" {
		t.Fatalf("frame = %+v", out)
	}
	// Empty payload.
	if err := writeFrame(&buffer, frame{Type: TCtrl, ID: 0}); err != nil {
		t.Fatal(err)
	}
	if out, err = readFrame(&buffer); err != nil || out.Type != TCtrl || len(out.Payload) != 0 {
		t.Fatalf("empty frame = %+v err=%v", out, err)
	}
}

// shortSocketPath returns a socket path under the system temp dir; the
// deep t.TempDir() paths exceed the 104-byte sun_path limit on macOS.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hrdx")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "h.sock")
}

// startTestHolder runs a holder server on a temp socket.
func startTestHolder(t *testing.T) string {
	t.Helper()
	socket := shortSocketPath(t)
	server := NewServer(socket, "test")
	go func() { _ = server.Run() }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("holder socket never appeared")
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Cleanup(func() {
		if client, err := Connect(socket); err == nil {
			client.Shutdown()
			client.Close()
		}
	})
	return socket
}

// collector gathers session output thread-safely.
type collector struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *collector) sink(data []byte) {
	c.mu.Lock()
	c.buf.Write(data)
	c.mu.Unlock()
}

func (c *collector) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func waitContains(t *testing.T, c *collector, needle string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(c.String(), needle) {
		if time.Now().After(deadline) {
			t.Fatalf("output %q never contained %q", c.String(), needle)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestHolderSessionSurvivesDetach(t *testing.T) {
	socket := startTestHolder(t)

	// First client: start a shell, see its output.
	first, err := Connect(socket)
	if err != nil {
		t.Fatal(err)
	}
	script := "echo ready; cat"
	if runtime.GOOS == "windows" {
		script = "Write-Output 'ready'; while ($true) { $l = [Console]::In.ReadLine(); if ($null -eq $l) { break }; Write-Output $l }"
	}
	path, args := testShell(script)
	session, err := first.Start(path, args, t.TempDir(), testEnv(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	// Let the marker arrive before Attach. Output from a started but not yet
	// subscribed session must be buffered rather than dropped.
	time.Sleep(100 * time.Millisecond)
	var out1 collector
	if _, err := first.Attach(session, 80, 24, out1.sink); err != nil {
		t.Fatal(err)
	}
	waitContains(t, &out1, "ready")

	// Detach. The session keeps running; output lands in the ring.
	first.Close()
	time.Sleep(100 * time.Millisecond)

	// Second client: the session is still there, input still works.
	second, err := Connect(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	sessions, err := second.List()
	if err != nil || len(sessions) != 1 || !sessions[0].Running {
		t.Fatalf("sessions after detach = %+v err=%v", sessions, err)
	}
	var out2 collector
	running, err := second.Attach(session, 80, 24, out2.sink)
	if err != nil || !running {
		t.Fatalf("reattach = %v err=%v", running, err)
	}
	second.Write(session, []byte("echoed-back\n"))
	waitContains(t, &out2, "echoed-back")

	killSessionAndWait(t, second, session)
}

func TestHolderReplaysDetachedOutput(t *testing.T) {
	socket := startTestHolder(t)

	first, err := Connect(socket)
	if err != nil {
		t.Fatal(err)
	}
	// The marker prints shortly after attach; we detach before it fires.
	lateScript := "sleep 0.3; echo late-marker; sleep 30"
	if runtime.GOOS == "windows" {
		lateScript = "Start-Sleep -Milliseconds 300; Write-Output 'late-marker'; Start-Sleep -Seconds 30"
	}
	latePath, lateArgs := testShell(lateScript)
	session, err := first.Start(latePath, lateArgs, t.TempDir(), testEnv(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	first.Close()
	time.Sleep(600 * time.Millisecond) // marker printed while detached

	second, err := Connect(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var out collector
	if _, err := second.Attach(session, 80, 24, out.sink); err != nil {
		t.Fatal(err)
	}
	waitContains(t, &out, "late-marker")

	killSessionAndWait(t, second, session)
}

func TestHolderExitEvent(t *testing.T) {
	socket := startTestHolder(t)
	client, err := Connect(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	exited := make(chan int64, 1)
	client.SetExitHandler(func(session int64) { exited <- session })

	exitPath, exitArgs := testShell("exit 0")
	session, err := client.Start(exitPath, exitArgs, t.TempDir(), testEnv(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	var out collector
	_, _ = client.Attach(session, 80, 24, out.sink)

	select {
	case got := <-exited:
		if got != session {
			t.Fatalf("exit event for %d, want %d", got, session)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no exit event")
	}
}

func TestHolderProtocolMismatch(t *testing.T) {
	socket := startTestHolder(t)
	conn, err := Connect(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// A raw wrong-protocol hello must be rejected.
	if _, err := conn.call(request{Op: "hello", Protocol: Protocol + 1}); err == nil {
		t.Fatal("wrong protocol accepted")
	}
}

func TestHolderKill(t *testing.T) {
	socket := startTestHolder(t)
	client, err := Connect(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	sleepPath, sleepArgs := testShell("sleep 30")
	session, err := client.Start(sleepPath, sleepArgs, t.TempDir(), testEnv(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	client.Kill(session)
	deadline := time.Now().Add(5 * time.Second)
	for {
		sessions, _ := client.List()
		if len(sessions) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session not removed: %+v", sessions)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The exit handler may block on the UI loop, which itself may be waiting
// for a holder response. The read loop must keep delivering responses
// while a handler is stuck, or the whole TUI deadlocks (close pane bug).
func TestExitHandlerDoesNotBlockResponses(t *testing.T) {
	socket := startTestHolder(t)
	client, err := Connect(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	release := make(chan struct{})
	client.SetExitHandler(func(session int64) { <-release })
	defer close(release)

	sleepPath, sleepArgs := testShell("sleep 30")
	session, err := client.Start(sleepPath, sleepArgs, t.TempDir(), testEnv(), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	var out collector
	if _, err := client.Attach(session, 80, 24, out.sink); err != nil {
		t.Fatal(err)
	}
	client.Kill(session)
	time.Sleep(300 * time.Millisecond) // exit event arrives, handler blocks

	done := make(chan error, 1)
	go func() {
		_, err := client.List()
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("List during blocked exit handler: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("holder call deadlocked while the exit handler was blocked")
	}
}
