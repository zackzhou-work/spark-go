package monitor

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// shortSocketPath keeps the path under the ~104 character limit unix sockets have, which
// t.TempDir's own path already exceeds
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "herdr")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "h.sock")
}

// fakeSocket serves one request per connection and returns what reply says
func fakeSocket(t *testing.T, reply func(request map[string]any) string) (string, *[]map[string]any) {
	t.Helper()
	path := shortSocketPath(t)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	var seen []map[string]any
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					return
				}
				var req map[string]any
				if json.Unmarshal(line, &req) != nil {
					return
				}
				seen = append(seen, req)
				if reply != nil {
					// One line, as the real sockets answer
					var compact any
					if json.Unmarshal([]byte(reply(req)), &compact) != nil {
						return
					}
					body, _ := json.Marshal(compact)
					_, _ = conn.Write(append(body, '\n'))
				}
			}()
		}
	}()
	return path, &seen
}

func TestHerdrCoreTerminalsKeepsPiOnly(t *testing.T) {
	path, seen := fakeSocket(t, func(map[string]any) string {
		return `{"id":"spark","result":{"terminals":[
			{"address":"t4","foreground":{"pid":67424},"status":{"app":"pi","state":"working"}},
			{"address":"vps/t9","foreground":{"pid":67424},"status":{"app":"pi","state":"working"}},
			{"address":"t5","foreground":{"pid":67425},"status":{"app":"claude","state":"idle"}},
			{"address":"t6","foreground":{"pid":67426},"status":{"app":"pi","state":"blocked","kind":"question"}},
			{"address":"t7","foreground":{"pid":67427}},
			{"address":"t8","status":{"app":"pi","state":"idle"}}]}}`
	})

	terminals, err := (HerdrCore{SocketPath: path}).Terminals()
	if err != nil {
		t.Fatalf("Terminals: %v", err)
	}
	if len(terminals) != 2 {
		t.Fatalf("want the two local pi terminals that say what they run, got %+v", terminals)
	}
	if got := terminals[0]; got.Address != "t4" || got.PID != 67424 || got.Status != piWorking {
		t.Errorf("wrong fields: %+v", got)
	}
	if got := terminals[1]; got.BlockKind != "question" || got.Status != piBlocked {
		t.Errorf("wrong block: %+v", got)
	}
	if len(*seen) != 1 || (*seen)[0]["method"] != "terminal.list" {
		t.Errorf("calls = %v", *seen)
	}
}

func TestHerdrCoreTerminalsReportsServerError(t *testing.T) {
	path, _ := fakeSocket(t, func(map[string]any) string {
		return `{"error":{"code":"-32601","message":"unknown method \"terminal.list\""}}`
	})

	if _, err := (HerdrCore{SocketPath: path}).Terminals(); err == nil ||
		!strings.Contains(err.Error(), "unknown method") {
		t.Fatalf("want the server's message, got %v", err)
	}
}

// The window is asked for a command, not for JSON-RPC, and it answers ok or one error
func TestHerdrAppFocusTerminalSendsTheCommand(t *testing.T) {
	path, seen := fakeSocket(t, func(map[string]any) string {
		return `{"ok":true}`
	})

	if err := (HerdrApp{SocketPath: path}).FocusTerminal("t4"); err != nil {
		t.Fatalf("FocusTerminal: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("calls = %v", *seen)
	}
	req := (*seen)[0]
	if req["do"] != "agent.focus" {
		t.Errorf("command = %v, want agent.focus", req["do"])
	}
	args, _ := req["args"].(map[string]any)
	if args["terminal"] != "t4" {
		t.Errorf("terminal = %v, want t4", args["terminal"])
	}
}

func TestHerdrAppFocusTerminalReportsServerError(t *testing.T) {
	path, _ := fakeSocket(t, func(map[string]any) string {
		return `{"error":"no running terminal \"t99\""}`
	})

	err := (HerdrApp{SocketPath: path}).FocusTerminal("t99")
	if err == nil || !strings.Contains(err.Error(), "no running terminal") {
		t.Fatalf("want the window's message, got %v", err)
	}
}

func TestHerdrWithoutServer(t *testing.T) {
	missing := shortSocketPath(t)
	if _, err := (HerdrCore{SocketPath: missing}).Terminals(); err == nil {
		t.Error("want an error when the socket is missing")
	}
	if err := (HerdrApp{SocketPath: missing}).FocusTerminal("t4"); err == nil {
		t.Error("want an error when the window's socket is missing")
	}
	if HerdrAvailable(missing) {
		t.Error("a missing socket is not available")
	}
}

func TestHerdrGivesUpOnASilentServer(t *testing.T) {
	// A connected server that never answers must not hold the scan
	path := shortSocketPath(t)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			if _, err := ln.Accept(); err != nil {
				return
			}
		}
	}()

	start := time.Now()
	if _, err := (HerdrCore{SocketPath: path}).Terminals(); err == nil {
		t.Fatal("want a timeout error")
	}
	if elapsed := time.Since(start); elapsed > 3*herdrTimeout {
		t.Errorf("took %v, want it to give up around %v", elapsed, herdrTimeout)
	}
}
