package monitor

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
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
	// Each install gets its own identity, so a session that loaded the previous copy can be
	// told apart from one that loaded this one
	id := strconv.FormatInt(time.Now().UnixNano(), 36)
	content := bytes.Replace(piExtension, []byte(piInstallMarker), []byte(id), 1)
	return os.WriteFile(path, content, 0o644)
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
	case bytes.Equal(withoutInstallID(content), piExtension):
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

// piInstallMarker is the identity the shipped extension carries. Installing replaces it with
// one of its own, so the file on disk says which copy it is and a session reports the same
// value back. The marker itself is versioned because a hand-copied install keeps it: without
// the version, two releases would look like the same install. Bump it with the extension
const piInstallMarker = "__SPARK_INSTALL_V1__"

// PiInstalledID is the identity of the installed extension, which is the marker itself when the
// file was copied rather than installed
func PiInstalledID(home string) string {
	content, err := os.ReadFile(PiExtensionPath(home))
	if err != nil {
		return ""
	}
	return extensionID(content)
}

// extensionID reads the identity out of an extension file
func extensionID(content []byte) string {
	_, rest, found := bytes.Cut(content, []byte("const INSTALL_ID = \""))
	if !found {
		return ""
	}
	id, _, found := bytes.Cut(rest, []byte("\""))
	if !found {
		return ""
	}
	return string(id)
}

// withoutInstallID is the file with its identity blanked, so two copies of the same extension
// compare equal whatever identity each was installed with
func withoutInstallID(content []byte) []byte {
	id := extensionID(content)
	if id == "" {
		return content
	}
	return bytes.Replace(content, []byte(id), []byte(piInstallMarker), 1)
}

// PiReported reports whether a pi process is running the extension that is installed now. pi
// loads extensions when it starts, so the file on disk is not enough: a session still running
// the previous copy keeps rewriting its own receipt
func PiReported(home string) bool {
	want := PiInstalledID(home)
	if want == "" {
		return false
	}
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
		if r.Install == want {
			return true
		}
	}
	return false
}
