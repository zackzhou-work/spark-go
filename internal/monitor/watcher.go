package monitor

import (
	"io/fs"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	// After a file event, wait this long to fold a burst of writes into one scan
	debounce = 300 * time.Millisecond
	// Changes like a process exiting or a pending tool timing out produce no file event,
	// so a timer catches them
	fallbackWatching = 5 * time.Second
	fallbackPolling  = 2 * time.Second
)

// Watch follows the session, transcript and hook directories in the background and rescans
// on any change, handing results to publish. Returning false from publish stops it
func Watch(s *Scanner, publish func([]ProjectGroup) bool) {
	go func() {
		events, closeWatcher := watchRecursive(s.WatchDirs())
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
			case <-events:
				drain(events)
			case <-time.After(fallback):
			}
		}
	}()
}

// Return once events have stopped for longer than debounce
func drain(events <-chan struct{}) {
	for {
		select {
		case <-events:
		case <-time.After(debounce):
			return
		}
	}
}

// fsnotify uses kqueue on macOS, which can't watch recursively, so every subdirectory is
// added individually and newly created ones are picked up too. With nothing watched it
// returns a nil channel and the caller falls back to polling
func watchRecursive(roots []string) (<-chan struct{}, func()) {
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
	for _, root := range roots {
		addTree(root)
	}
	if !watching {
		w.Close()
		return nil, func() {}
	}

	out := make(chan struct{}, 1)
	go func() {
		for {
			select {
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if ev.Has(fsnotify.Create) {
					addTree(ev.Name)
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
