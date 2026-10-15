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

// fakeHerdr serves one request per connection and returns what reply says
func fakeHerdr(t *testing.T, reply func(method string, params map[string]any) string) (HerdrClient, *[]string) {
	t.Helper()
	path := shortSocketPath(t)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	var seen []string
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
				var req struct {
					Method string         `json:"method"`
					Params map[string]any `json:"params"`
				}
				if json.Unmarshal(line, &req) != nil {
					return
				}
				seen = append(seen, req.Method)
				if reply != nil {
					// One line, as the real server sends it
					var compact any
					if json.Unmarshal([]byte(reply(req.Method, req.Params)), &compact) != nil {
						return
					}
					body, _ := json.Marshal(compact)
					_, _ = conn.Write(append(body, '\n'))
				}
			}()
		}
	}()
	return HerdrClient{SocketPath: path}, &seen
}

func TestHerdrAgentsKeepsPiOnly(t *testing.T) {
	client, _ := fakeHerdr(t, func(string, map[string]any) string {
		return `{"id":"spark","result":{"type":"agent_list","agents":[
			{"agent":"pi","pane_id":"w1:p1","agent_status":"working","focused":true,"cwd":"/tmp/spark-go",
			 "agent_session":{"kind":"path","source":"herdr:pi","value":"/sessions/a.jsonl"}},
			{"agent":"claude","pane_id":"w1:p2","agent_status":"idle","focused":false,
			 "agent_session":{"value":"/elsewhere"}}]}}`
	})

	agents, err := client.Agents()
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("want only the pi agent, got %+v", agents)
	}
	got := agents[0]
	if got.PaneID != "w1:p1" || got.Status != herdrWorking || !got.Focused {
		t.Errorf("wrong fields: %+v", got)
	}
	if got.SessionPath != "/sessions/a.jsonl" || got.Cwd != "/tmp/spark-go" {
		t.Errorf("wrong identity: %+v", got)
	}
}

func TestHerdrFocusPaneSendsTheTarget(t *testing.T) {
	var target string
	client, methods := fakeHerdr(t, func(_ string, params map[string]any) string {
		target, _ = params["target"].(string)
		return `{"id":"spark","result":{"type":"agent_info","agent":{"pane_id":"w2:p3"}}}`
	})

	if err := client.FocusPane("w2:p3"); err != nil {
		t.Fatalf("FocusPane: %v", err)
	}
	if target != "w2:p3" {
		t.Errorf("focus target = %q, want w2:p3", target)
	}
	if len(*methods) != 1 || (*methods)[0] != "agent.focus" {
		t.Errorf("methods = %v", *methods)
	}
}

func TestHerdrFocusPaneReportsServerError(t *testing.T) {
	client, _ := fakeHerdr(t, func(string, map[string]any) string {
		return `{"id":"spark","error":{"code":"agent_not_found","message":"agent target w9:p9 not found"}}`
	})

	err := client.FocusPane("w9:p9")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("want the server's message, got %v", err)
	}
}

func TestHerdrAgentsWithoutServer(t *testing.T) {
	client := HerdrClient{SocketPath: shortSocketPath(t)}
	if _, err := client.Agents(); err == nil {
		t.Fatal("want an error when the socket is missing")
	}
	if HerdrAvailable(client.SocketPath) {
		t.Error("a missing socket is not available")
	}
}

func TestHerdrAgentsGivesUpOnASilentServer(t *testing.T) {
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
	if _, err := (HerdrClient{SocketPath: path}).Agents(); err == nil {
		t.Fatal("want a timeout error")
	}
	if elapsed := time.Since(start); elapsed > 3*herdrTimeout {
		t.Errorf("took %v, want it to give up around %v", elapsed, herdrTimeout)
	}
}
