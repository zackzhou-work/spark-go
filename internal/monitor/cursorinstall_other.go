//go:build !darwin

package monitor

// lockCursorInstall is a no-op away from macOS, where Cursor's desktop app and spark's install
// path don't exist
func lockCursorInstall(home string) (func(), error) {
	return func() {}, nil
}
