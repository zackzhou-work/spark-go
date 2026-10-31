package monitor

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
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

// HerdrTerminal is the part of a herdr-core terminal spark uses
type HerdrTerminal struct {
	// Address is what focusing asks for: `t7`, this terminal's id forever on this core
	Address string
	// PID is the foreground process group's leader, which is the pi process itself while a
	// turn runs and the shell underneath between them. It is the one thing a terminal and a
	// session file can be matched by: herdr-core doesn't say which session a terminal runs,
	// but the extension writes the pid both sides agree on into its receipt
	PID int
	// Status is the program's own word: idle, working, done, blocked or error
	Status string
	// BlockKind is what a blocked program is asking for: permission, question or auth
	BlockKind string
}

// HerdrCoreSocketPath is where the core that runs the terminals listens. A terminal it
// started is told the path, and the default profile is what everything else uses
func HerdrCoreSocketPath(home string) string {
	if path := os.Getenv("HC_TERMINAL_SOCKET"); path != "" {
		return path
	}
	return filepath.Join(home, ".local", "state", "herdr-core", "default", "default.sock")
}

// HerdrAppSocketPath is where the window listens; only it can show a terminal to the user
func HerdrAppSocketPath(home string) string {
	if dir := os.Getenv("HERDR_APP_HOME"); dir != "" {
		return filepath.Join(dir, "herdr-app.sock")
	}
	return filepath.Join(home, ".config", "herdr-app", "herdr-app.sock")
}

// HerdrAvailable reports whether the socket is there at all, so a machine without Herdr
// skips the dial entirely
func HerdrAvailable(socketPath string) bool {
	if socketPath == "" {
		return false
	}
	info, err := os.Stat(socketPath)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

// listen opens one connection and hands back what it read, which both of Herdr's sockets
// answer as a single line
func listen(socketPath string, request any) ([]byte, error) {
	if socketPath == "" {
		return nil, errors.New("herdr: no socket path")
	}
	conn, err := net.DialTimeout("unix", socketPath, herdrTimeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(herdrTimeout))

	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(append(body, '\n')); err != nil {
		return nil, err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return nil, err
	}
	return line, nil
}

// HerdrCore is the JSON-RPC socket of the core that runs the terminals. It is the only
// source of what a terminal's program is doing, and of what showing it would take
type HerdrCore struct {
	SocketPath string
}

type herdrReply struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c HerdrCore) call(method string, out any) error {
	line, err := listen(c.SocketPath, map[string]any{"id": "spark", "method": method, "params": struct{}{}})
	if err != nil {
		return err
	}
	var reply herdrReply
	if err := json.Unmarshal(line, &reply); err != nil {
		return err
	}
	if reply.Error != nil {
		return fmt.Errorf("herdr %s: %s", method, reply.Error.Message)
	}
	return json.Unmarshal(reply.Result, out)
}

// Terminals lists the live terminals running a pi agent. Herdr runs other agents too, but
// spark's Claude and Codex sides have their own sources
func (c HerdrCore) Terminals() ([]HerdrTerminal, error) {
	var page struct {
		Terminals []struct {
			Address    string `json:"address"`
			Foreground *struct {
				PID int `json:"pid"`
			} `json:"foreground"`
			Status *struct {
				App   string `json:"app"`
				State string `json:"state"`
				Kind  string `json:"kind"`
			} `json:"status"`
		} `json:"terminals"`
	}
	if err := c.call("terminal.list", &page); err != nil {
		return nil, err
	}
	out := make([]HerdrTerminal, 0, len(page.Terminals))
	for _, t := range page.Terminals {
		// A terminal on another machine answers with `machine/t7`, and its pid says nothing
		// about a process here: only this machine's sessions have a session file to match
		if strings.Contains(t.Address, "/") {
			continue
		}
		if t.Status == nil || t.Status.App != "pi" || t.Foreground == nil {
			continue
		}
		out = append(out, HerdrTerminal{
			Address:   t.Address,
			PID:       t.Foreground.PID,
			Status:    t.Status.State,
			BlockKind: t.Status.Kind,
		})
	}
	return out, nil
}

// HerdrApp is the window's own socket. herdr-core runs terminals; only the window can put
// one in front of the user
type HerdrApp struct {
	SocketPath string
}

// FocusTerminal shows a terminal in the window, which is also how Herdr marks it seen. Its
// reply is `{"ok":true}`, or one error string
func (c HerdrApp) FocusTerminal(address string) error {
	if address == "" {
		return errors.New("herdr-app: no terminal to focus")
	}
	line, err := listen(c.SocketPath, map[string]any{
		"do":   "agent.focus",
		"args": map[string]string{"terminal": address},
	})
	if err != nil {
		return err
	}
	var reply struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(line, &reply); err != nil {
		return err
	}
	if reply.Error != "" {
		return fmt.Errorf("herdr-app agent.focus: %s", reply.Error)
	}
	if !reply.OK {
		return errors.New("herdr-app agent.focus: no ok")
	}
	return nil
}
