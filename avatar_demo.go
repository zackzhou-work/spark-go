package main

import (
	"fmt"
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

var moodNames = [...]string{"Needs you", "Working", "To read", "All caught up"}

// avatarGallery 是角色的预览窗口：上面切换表情看过渡，下面是五个角色乘四种表情
type avatarGallery struct {
	mood mood
}

func (g *avatarGallery) view(c *ui.Context) {
	ink, sec := ui.Hex("#34322D"), ui.Hex("#858481")
	ui.Column(c).Fill().Background(ui.Hex("#FFFFFF")).TextColor(ink).Padding(48, 32, 32, 32).Gap(28).Children(func() {
		ui.Row(c).Gap(6).Children(func() {
			for i, name := range moodNames {
				btn := ui.Row(c.Key(i)).Height(32).Padding(0, 14).Radius(16).Cursor(ui.CursorPointer).Label(name)
				switch {
				case mood(i) == g.mood:
					btn.Background(ui.Hex("#1A1A19")).TextColor(ui.Hex("#FFFFFF"))
				case btn.Hovered():
					btn.Background(ui.Hex("#37352F").Alpha(0.1))
				default:
					btn.Background(ui.Hex("#37352F").Alpha(0.06))
				}
				btn.OnClick(func() { g.mood = mood(i) })
				btn.Children(func() { ui.Text(c, name).FontSize(14).FontWeight(510) })
			}
		})

		ui.Row(c).Gap(32).Padding(16, 0, 8, 0).Children(func() {
			for i := range bodies {
				avatarOf(c.Key(fmt.Sprint("big", i)), castSeed(i, i), g.mood, demoProp(i), 64)
			}
		})

		ui.Column(c).Gap(24).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				for _, name := range moodNames {
					ui.Text(c, name).Width(88).FontSize(11).TextColor(sec).TextAlign(ui.Center)
				}
			})
			for i := range bodies {
				ui.Row(c).Gap(8).Children(func() {
					for m := range moodNames {
						ui.Row(c).Width(88).Center().Children(func() {
							avatarOf(c.Key(fmt.Sprint(i, "-", m)), castSeed(i, i), mood(m), demoProp(i), 48)
						})
					}
				})
			}
		})
	})
}

// demoProp 让预览里 ! 和 ? 两种气泡、一位和多位数的角标都出现
func demoProp(i int) prop {
	glyph := "!"
	if i%2 == 1 {
		glyph = "?"
	}
	return prop{glyph: glyph, count: []int{1, 2, 3, 1, 12}[i]}
}

func runAvatarGallery() {
	g := &avatarGallery{}
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:   "Spark characters",
			Width:   560,
			Height:  680,
			Content: ui.View(g.view),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
