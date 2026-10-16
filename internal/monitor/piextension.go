package monitor

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
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

// PiExtensionState is what is on disk: nothing, a copy from an older release, or the one this
// build installs
type PiExtensionState int

const (
	// PiExtensionAbsent: nothing has been installed
	PiExtensionAbsent PiExtensionState = iota
	// PiExtensionOutdated: an older release's copy is in place. Reloading pi would load that
	// same file, so spark has to offer to write the current one
	PiExtensionOutdated
	PiExtensionCurrent
)

func PiExtensionOnDisk(home string) PiExtensionState {
	content, err := os.ReadFile(PiExtensionPath(home))
	switch {
	case err != nil:
		return PiExtensionAbsent
	case bytes.Equal(content, piExtension):
		return PiExtensionCurrent
	default:
		return PiExtensionOutdated
	}
}

// PiExtensionInstalled reports whether the current file is in place
func PiExtensionInstalled(home string) bool {
	return PiExtensionOnDisk(home) == PiExtensionCurrent
}

// PiInstalledAt is when the extension file was last written, which is the moment a receipt has
// to beat to prove that a pi process loaded it
func PiInstalledAt(home string) time.Time {
	info, err := os.Stat(PiExtensionPath(home))
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// PiReported reports whether a pi process is running this version of the extension. pi loads
// extensions when it starts, so the file on disk is not enough: a session still running an
// older copy keeps rewriting its own receipt, which is why the receipt's payload version is
// what counts rather than when anything was written
func PiReported(home string) bool {
	entries, err := os.ReadDir(PiReceiptDir(home))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		content, err := os.ReadFile(filepath.Join(PiReceiptDir(home), e.Name()))
		if err != nil {
			continue
		}
		var r PiReceipt
		if json.Unmarshal(content, &r) != nil {
			continue
		}
		if r.Payload >= piReceiptPayload {
			return true
		}
	}
	return false
}
