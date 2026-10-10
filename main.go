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
	a := &app{pinned: true, groups: scanner.Scan(), hookInstalled: monitor.HookInstalled(home)}

	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Width:     360,
			Height:    640,
			MinWidth:  minWidth,
			MinHeight: 300,
			StateKey:  "main",
			// 红绿灯离窗口左上角的偏移：x 对齐标题栏的左内边距，y 让三颗按钮和右侧的图钉落在同一条中线上
			TitleBarStyle:        mygo.TitleBarHidden,
			TrafficLightPosition: &mygo.Point{X: 14, Y: 18},
			Transparent:          true,
			AlwaysOnTop:          true,
			Content:              ui.View(a.view),
		})
		a.win = win
		// 记住的窗口尺寸不受 MinWidth 约束，以前缩得更窄的窗口在这里撑回来
		if w, h := win.Size(); w < minWidth {
			win.SetSize(minWidth, h)
		}

		var closed atomic.Bool
		win.OnClosed(func() { closed.Store(true) })
		last := a.groups
		// 列表里的"几分钟前"不随文件变化刷新，按分钟重建一次视图
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
			win.Update(func() { a.groups, a.hookInstalled = groups, hooked })
			return true
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
