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

func main() {
	avatars := flag.Bool("avatars", false, "open the character preview instead of the monitor")
	flag.Parse()
	if *avatars {
		runAvatarGallery()
		return
	}

	scanner := monitor.NewScanner()
	home, _ := os.UserHomeDir()
	a := &app{
		pinned:             true,
		groups:             scanner.ScanAll(),
		hookInstalled:      monitor.HookInstalled(home),
		codexHookInstalled: monitor.CodexHooksInstalled(home),
		piHookInstalled:    monitor.PiExtensionInstalled(home),
	}

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
			if reflect.DeepEqual(groups, last) {
				return true
			}
			last = groups
			hooked := monitor.HookInstalled(home)
			codexHooked := monitor.CodexHooksInstalled(home)
			piHooked := monitor.PiExtensionInstalled(home)
			win.Update(func() {
				a.groups, a.hookInstalled, a.codexHookInstalled, a.piHookInstalled = groups, hooked, codexHooked, piHooked
			})
			return true
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
