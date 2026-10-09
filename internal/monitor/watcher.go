package monitor

import (
	"io/fs"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	// 文件事件到达后再等这么久，把一串连续写入合并成一次扫描
	debounce = 300 * time.Millisecond
	// 进程退出、工具挂起超时这类变化不产生文件事件，靠定时兜底
	fallbackWatching = 5 * time.Second
	fallbackPolling  = 2 * time.Second
)

// Watch 在后台监听会话、transcript 和 hooks 目录，有变化就重新扫描，把结果交给 publish。
// publish 返回 false 时停止
func Watch(s *Scanner, publish func([]ProjectGroup) bool) {
	go func() {
		events, closeWatcher := watchRecursive(s.WatchDirs())
		defer closeWatcher()
		fallback := fallbackPolling
		if events != nil {
			fallback = fallbackWatching
		}

		for {
			if !publish(s.Scan()) {
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

// 事件一停下来超过 debounce 就返回
func drain(events <-chan struct{}) {
	for {
		select {
		case <-events:
		case <-time.After(debounce):
			return
		}
	}
}

// fsnotify 在 macOS 上走 kqueue，不支持递归监听，只能把每层子目录都加进去，新建的目录也得补上。
// 一个目录都没监听上时返回 nil channel，调用方退回轮询
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
