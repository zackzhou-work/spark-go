package main

import (
	"fmt"
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

var moodNames = [...]string{"Needs you", "Working", "To read", "All caught up"}

// avatarGallery is the character preview window: expressions to switch between and watch
// transition up top, one row per body shape with four expressions below
type avatarGallery struct {
	mood mood
}

func (g *avatarGallery) view(c *ui.Context) {
	ink, sec := ui.Hex("#34322D"), ui.Hex("#858481")
	ui.Column(c).Fill().Background(ui.Hex("#FFFFFF")).TextColor(ink).Padding(36, 28, 28, 28).Gap(22).Children(func() {
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

		ui.Row(c).Gap(32).Padding(8, 0, 4, 0).Children(func() {
			for i := range bodies {
				avatarOf(c.Key(fmt.Sprint("big", i)), demoCharacter(i), uint32(i), g.mood, demoProp(i), 64)
			}
		})

		// Every colour with every expression, and every shape with every expression: a body
		// dark enough to swallow a mouth and a shape that leaves the eyes no room show here
		// whichever expression is up
		ui.Row(c).Gap(28).AlignItems(ui.Start).Children(func() {
			g.wall(c, sec, "Colours", len(colours),
				func(i int) string { return colours[i].name },
				func(i int) character { return character{hue: i, shape: bodyIndex("cloud")} })
			g.wall(c, sec, "Shapes", len(bodies),
				func(i int) string { return bodyNames[i] },
				func(i int) character { return character{hue: 0, shape: i} })
		})
	})
}

// wall lists characters down the page, the four expressions across
func (g *avatarGallery) wall(c *ui.Context, sec ui.Color, title string, n int, name func(int) string, who func(int) character) {
	const cell, size = float32(64), float32(34)
	ui.Column(c).Children(func() {
		ui.Row(c).Height(18).Gap(8).Children(func() {
			ui.Text(c, title).Width(cell).FontSize(11).TextColor(sec)
		})
		for i := range n {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, name(i)).Width(cell).FontSize(11).TextColor(sec)
				for m := range moodCount {
					ui.Row(c).Width(cell).Height(size + 6).Center().Children(func() {
						avatarOf(c.Key(fmt.Sprint(i, "-", m)), who(i), uint32(i), mood(m), demoProp(i), size)
					})
				}
			})
		}
	})
}

// demoCharacter gives every body shape a turn, cycling through the colours
func demoCharacter(i int) character {
	return character{hue: i % len(colours), shape: i}
}

// demoProp makes the preview show both ! and ? bubbles along with single- and multi-digit
// badges
func demoProp(i int) prop {
	glyph := "!"
	if i%2 == 1 {
		glyph = "?"
	}
	counts := []int{1, 2, 3, 1, 12}
	return prop{glyph: glyph, count: counts[i%len(counts)]}
}

func runAvatarGallery() {
	g := &avatarGallery{}
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:   "Spark characters",
			Width:   840,
			Height:  760,
			Content: ui.View(g.view),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
