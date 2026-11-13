package monitor

import (
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	// After a file event, wait this long to fold a burst of writes into one scan
	debounce    = 300 * time.Millisecond
	maxDebounce = time.Second
	// Changes like a process exiting or a pending tool timing out produce no file event,
	// so a timer catches them
	fallbackWatching = 5 * time.Second
	fallbackPolling  = 2 * time.Second
)

// Watch follows the session, transcript and hook directories in the background and rescans
// on any change, handing results to publish. Returning false from publish stops it
func Watch(s *Scanner, publish func([]ProjectGroup) bool) {
	go func() {
		events, closeWatcher := watchDirectories(s.WatchDirs(), s.antigravityShallowWatchDirs(), s.CursorDB+"-shm")
		defer closeWatcher()
		fallback := fallbackPolling
		if events != nil {
			fallback = fallbackWatching
		}

		for {
			if !publish(s.ScanAll()) {
				return
			}
			select {
			case _, ok := <-events:
				if ok {
					drain(events)
				} else {
					events = nil
					fallback = fallbackPolling
				}
			case <-time.After(fallback):
			}
		}
	}()
}

// Wait for a quiet period, but keep scanning during a continuous stream of events
func drain(events <-chan struct{}) {
	quiet := time.NewTimer(debounce)
	defer quiet.Stop()
	limit := time.NewTimer(maxDebounce)
	defer limit.Stop()
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return
			}
			if !quiet.Stop() {
				select {
				case <-quiet.C:
				default:
				}
			}
			quiet.Reset(debounce)
		case <-quiet.C:
			return
		case <-limit.C:
			return
		}
	}
}

// fsnotify uses kqueue on macOS, which can't watch recursively, so every subdirectory under
// recursiveRoots is added individually and newly created ones are picked up too. shallowRoots
// are added non-recursively so large media trees (such as Antigravity's browser recordings) do
// not exhaust file descriptors. With nothing watched it returns a nil channel and the caller
// falls back to polling
func watchRecursive(roots []string, ignoredPath string) (<-chan struct{}, func()) {
	return watchDirectories(roots, nil, ignoredPath)
}

func watchDirectories(recursiveRoots, shallowRoots []string, ignoredPath string) (<-chan struct{}, func()) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, func() {}
	}
	watching := false
	addTree := func(root string) {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() && w.Add(path) == nil {
				watching = true
			}
			return nil
		})
	}
	for _, root := range recursiveRoots {
		addTree(root)
	}
	shallowSet := make(map[string]struct{}, len(shallowRoots))
	for _, root := range shallowRoots {
		clean := filepath.Clean(root)
		if w.Add(clean) == nil {
			watching = true
			shallowSet[clean] = struct{}{}
		}
	}
	if !watching {
		w.Close()
		return nil, func() {}
	}

	ignoredPath = filepath.Clean(ignoredPath)
	out := make(chan struct{}, 1)
	go func() {
		for {
			select {
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				cleanName := filepath.Clean(ev.Name)
				// SQLite readers update the shared-memory file without changing the data
				if cleanName == ignoredPath || strings.HasSuffix(cleanName, "-shm") {
					continue
				}
				if ev.Has(fsnotify.Create) {
					if _, shallow := shallowSet[filepath.Dir(cleanName)]; !shallow {
						addTree(cleanName)
					}
				}
				select {
				case out <- struct{}{}:
				default:
				}
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	return out, func() { w.Close() }
}
