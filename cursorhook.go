package main

import (
	"io"
	"log"
	"os"
	"time"

	"github.com/zackzhou-work/spark-go/internal/monitor"
)

const (
	// CursorInstallFlag installs the hooks from the command line, which is how a development build
	// or a headless machine gets them without the window
	CursorInstallFlag = "--cursor-install"
	// CursorUninstallFlag takes spark's own entries, helper and metadata back out of Cursor's hook
	// configuration, leaving every other entry alone
	CursorUninstallFlag = "--cursor-uninstall"
)

// isCursorHookRun says whether this process is one of the helper runs Cursor starts. It is
// checked before flags, scanner or window: the helper has to be a short-lived process with no
// interface of its own
func isCursorHookRun(args []string) bool {
	return len(args) > 1 && args[1] == monitor.CursorHookFlag
}

// isCursorInstallRun says whether this process was started to install spark's Cursor hooks
func isCursorInstallRun(args []string) bool {
	return len(args) > 1 && args[1] == CursorInstallFlag
}

// runCursorInstall copies this executable to the fixed helper path and merges the hooks, which is
// what the window's install button does
func runCursorInstall() int {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("[Cursor] no home directory to install into: %v", err)
		return 1
	}
	exe, err := os.Executable()
	if err != nil {
		log.Printf("[Cursor] can't find this executable: %v", err)
		return 1
	}
	if err := monitor.InstallCursorHooks(home, exe); err != nil {
		log.Printf("[Cursor] installing the hooks failed: %v", err)
		return 1
	}
	return 0
}

// isCursorUninstallRun says whether this process was started to remove spark's Cursor hooks
func isCursorUninstallRun(args []string) bool {
	return len(args) > 1 && args[1] == CursorUninstallFlag
}

// runCursorUninstall removes spark's own hooks. It is the documented way out, and it only ever
// touches entries that name spark's helper
func runCursorUninstall() int {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("[Cursor] no home directory to uninstall from: %v", err)
		return 1
	}
	if err := monitor.RemoveCursorHooks(home); err != nil {
		log.Printf("[Cursor] removing the hooks failed: %v", err)
		return 1
	}
	return 0
}

// runCursorHook records one hook event and returns the exit code. Nothing is written to
// stdout: a decision spark didn't mean to make must not land in Cursor's way. A failure is
// logged and swallowed, because a hook that reports trouble can still change what the agent
// does
func runCursorHook(stdin io.Reader) int {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("[Cursor] no home directory for hook events: %v", err)
		return 0
	}
	if _, err := monitor.ReadAndCollectCursorHook(stdin, home, time.Now()); err != nil {
		log.Printf("[Cursor] dropping hook event: %v", err)
	}
	return 0
}
