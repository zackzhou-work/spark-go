package monitor

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Herdr answers within microseconds over a local socket; anything slower is treated as
// unavailable so a scan never waits on it
const herdrTimeout = 400 * time.Millisecond

// Herdr's vocabulary has two words the extension never sends: a finished turn nobody has
// looked at, and an agent Herdr cannot classify
const (
	herdrDone    = "done"
	herdrUnknown = "unknown"
)

// HerdrAgent is the part of a Herdr pane's agent spark uses
type HerdrAgent struct {
	Agent  string
	PaneID string
	// Status is Herdr's own vocabulary: working, blocked, idle, done or unknown. The
	// extension reports the first three, and only Herdr tells a finished turn the user has
	// seen (idle) from one they haven't (done)
	Status      string
	Focused     bool
	SessionPath string
}

// HerdrSocketPath is where the Herdr server listens. Spark dials it directly: the CLI
// isn't needed, and its location depends on how Herdr was installed
func HerdrSocketPath(home string) string {
	return filepath.Join(home, ".config", "herdr", "herdr.sock")
}

type HerdrClient struct {
	SocketPath string
}

type herdrReply struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c HerdrClient) call(method string, params any, out any) error {
	if c.SocketPath == "" {
		return errors.New("herdr: no socket path")
	}
	conn, err := net.DialTimeout("unix", c.SocketPath, herdrTimeout)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(herdrTimeout))

	body, err := json.Marshal(map[string]any{"id": "spark", "method": method, "params": params})
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(body, '\n')); err != nil {
		return err
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return err
	}
	var reply herdrReply
	if err := json.Unmarshal(line, &reply); err != nil {
		return err
	}
	if reply.Error != nil {
		return fmt.Errorf("herdr %s: %s", method, reply.Error.Message)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(reply.Result, out)
}

// Agents lists the live pi agents. Herdr tracks other agents too, but spark's Claude and
// Codex sides have their own sources
func (c HerdrClient) Agents() ([]HerdrAgent, error) {
	var page struct {
		Agents []struct {
			Agent        string `json:"agent"`
			PaneID       string `json:"pane_id"`
			Status       string `json:"agent_status"`
			Focused      bool   `json:"focused"`
			AgentSession struct {
				Value string `json:"value"`
			} `json:"agent_session"`
		} `json:"agents"`
	}
	if err := c.call("agent.list", struct{}{}, &page); err != nil {
		return nil, err
	}
	out := make([]HerdrAgent, 0, len(page.Agents))
	for _, a := range page.Agents {
		if a.Agent != "pi" {
			continue
		}
		out = append(out, HerdrAgent{
			Agent:       a.Agent,
			PaneID:      a.PaneID,
			Status:      a.Status,
			Focused:     a.Focused,
			SessionPath: a.AgentSession.Value,
		})
	}
	return out, nil
}

// FocusPane brings the pane holding a session to the front. Herdr also marks the target
// seen, which is how a finished turn stops counting as unread
func (c HerdrClient) FocusPane(paneID string) error {
	if paneID == "" {
		return errors.New("herdr: no pane to focus")
	}
	return c.call("agent.focus", map[string]string{"target": paneID}, nil)
}

// HerdrAvailable reports whether the server is there at all, so a machine without Herdr
// skips the dial entirely
func HerdrAvailable(socketPath string) bool {
	if socketPath == "" {
		return false
	}
	info, err := os.Stat(socketPath)
	return err == nil && info.Mode()&os.ModeSocket != 0
}
