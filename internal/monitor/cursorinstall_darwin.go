package monitor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// cursorInstallLockWait is how long an install waits for the other Spark window to finish
const cursorInstallLockWait = 5 * time.Second

// lockCursorInstall serialises installs from two Spark windows, which would otherwise merge the
// same file and lose one of the results. An outside editor takes no lock: that race is what the
// unchanged-bytes check before the write narrows
func lockCursorInstall(home string) (func(), error) {
	dir := CursorDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, "install.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(cursorInstallLockWait)
	for {
		err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() {
				_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
				file.Close()
			}, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) || time.Now().After(deadline) {
			file.Close()
			return nil, fmt.Errorf("waiting for the install lock: %w", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
