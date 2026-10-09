package main

import (
	"log"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/zackzhou-work/spark-go/internal/monitor"
)

func main() {
	scanner := monitor.NewScanner()
	a := &app{pinned: true, groups: scanner.Scan()}

	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Width:     320,
			Height:    480,
			MinWidth:  240,
			MinHeight: 280,
			StateKey:  "main",
			// 红绿灯离窗口左上角的偏移：x 对齐标题栏的左内边距，y 让三颗按钮和右侧的图钉落在同一条中线上
			TitleBarStyle:        mygo.TitleBarHidden,
			TrafficLightPosition: &mygo.Point{X: 14, Y: 16},
			Transparent:          true,
			AlwaysOnTop:          true,
			Content:              ui.View(a.view),
		})
		a.win = win

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
			win.Update(func() { a.groups = groups })
			return true
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
