package monitor

import (
	_ "embed"
	"os"
	"path/filepath"
)

// PiExtensionPath is where the extension has to land for pi to load it: pi reads plain
// TypeScript files from its extensions directory, so one file is the whole install
func PiExtensionPath(home string) string {
	return filepath.Join(home, ".pi", "agent", "extensions", "spark-pi.ts")
}

//go:embed hookscript/spark-pi.ts
var piExtension []byte

// InstallPiExtension writes the extension into place. Other extensions in that directory
// are left alone, including the one Herdr manages
func InstallPiExtension(home string) error {
	path := PiExtensionPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, piExtension, 0o644)
}

// PiExtensionInstalled reports whether the file is in place. It doesn't check what the
// file says, so an outdated copy still counts as installed
func PiExtensionInstalled(home string) bool {
	info, err := os.Stat(PiExtensionPath(home))
	return err == nil && info.Mode().IsRegular()
}

// PiReported reports whether any pi process has written a receipt. pi loads extensions when
// it starts, so a file on disk is not enough: this is what says a running session has picked
// the extension up
func PiReported(home string) bool {
	entries, err := os.ReadDir(PiReceiptDir(home))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			return true
		}
	}
	return false
}
