package main

import (
	"flag"
	"log"
	"os"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/zackzhou-work/spark-go/internal/monitor"
)

// sideState is everything the window shows besides its session groups: the hooks each agent
// needs and what pi's setup line goes by, all of it read from disk
type sideState struct {
	hooked        bool
	codexHooked   bool
	piState       monitor.PiExtensionState
	piInstalledAt time.Time
	piReported    bool
	piInstalled   bool

	// cursorState is what spark's own Cursor hooks look like on disk, cursorDetected keeps the
	// subject out of the window for people who don't run Cursor, and cursorReported says whether
	// the installed helper has actually written an event
	cursorState    monitor.CursorHookState
	cursorDetected bool
	cursorReported bool
}

func readSideState(home string) sideState {
	side := sideState{
		hooked:      monitor.HookInstalled(home),
		codexHooked: monitor.CodexHooksInstalled(home),
		piState:     monitor.PiExtensionOnDisk(home),
		piReported:  monitor.PiReported(home),
	}
	side.piInstalledAt = monitor.PiInstalledAt(home)
	side.piInstalled = side.piState == monitor.PiExtensionCurrent
	side.cursorState = monitor.CursorHooksOnDisk(home)
	side.cursorDetected = monitor.CursorPresent(home)
	return side
}

// readSideStateFor adds what only the scanner knows: whether Cursor's helper has ever reported
func readSideStateFor(scanner *monitor.Scanner, home string) sideState {
	side := readSideState(home)
	side.cursorReported = scanner.CursorReported()
	return side
}

func main() {
	// Cursor runs this executable as a hook. That path must not touch flags, the scanner or
	// the app, so it comes first and returns
	if isCursorHookRun(os.Args) {
		os.Exit(runCursorHook(os.Stdin))
	}
	if isCursorInstallRun(os.Args) {
		os.Exit(runCursorInstall())
	}
	if isCursorUninstallRun(os.Args) {
		os.Exit(runCursorUninstall())
	}

	avatars := flag.Bool("avatars", false, "open the character preview instead of the monitor")
	flag.Parse()
	if *avatars {
		runAvatarGallery()
		return
	}

	scanner := monitor.NewScanner()
	home, _ := os.UserHomeDir()
	// The saved characters are read once here, not in the view: a build must not touch the
	// disk, and a file that can't be read has to stay unread rather than be written over
	charactersAt := charactersPath(home)
	characters, charactersErr := readCharacterSettings(charactersAt)
	if charactersErr != nil {
		log.Printf("[Characters] reading %s failed, saving stays off: %v", charactersAt, charactersErr)
	}
	a := &app{
		scanner:       scanner,
		pinned:        true,
		groups:        scanner.ScanAll(),
		characters:    characters,
		charactersErr: charactersErr,
		charactersAt:  charactersAt,
	}
	// The state the window starts with is also the watcher's baseline, so the two cannot
	// disagree about what has already been published
	side := readSideStateFor(scanner, home)
	a.applySideState(side)

	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Width:     360,
			Height:    640,
			MinWidth:  minWidth,
			MinHeight: 300,
			StateKey:  "main",
			// Traffic light offset from the window's top-left: x lines up with the title
			// bar's left inset, y puts the three buttons on the same centre line as the pin
			TitleBarStyle:        mygo.TitleBarHidden,
			TrafficLightPosition: &mygo.Point{X: 14, Y: 18},
			Transparent:          true,
			AlwaysOnTop:          true,
			Content:              ui.View(a.view),
		})
		a.win = win
		// A remembered window size isn't subject to MinWidth, so a window narrowed earlier is
		// widened back here
		if w, h := win.Size(); w < minWidth {
			win.SetSize(minWidth, h)
		}

		var closed atomic.Bool
		win.OnClosed(func() { closed.Store(true) })
		last := a.groups
		lastSide := side
		// The "minutes ago" labels don't refresh on file changes, so rebuild the view once a
		// minute
		go func() {
			for range time.Tick(time.Minute) {
				if closed.Load() {
					return
				}
				win.Update(func() {})
			}
		}()
		monitor.Watch(scanner, func(groups []monitor.ProjectGroup) bool {
			if closed.Load() {
				return false
			}
			// What the window shows besides its groups comes from disk, so it is read here on
			// the scan goroutine rather than in the view, which rebuilds several times for one
			// event. A hook or a receipt can arrive without changing any group, so both have to
			// be compared, not just the groups
			side := readSideStateFor(scanner, home)
			if reflect.DeepEqual(groups, last) && side == lastSide {
				return true
			}
			last, lastSide = groups, side
			win.Update(func() {
				a.groups = groups
				a.applySideState(side)
			})
			return true
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
