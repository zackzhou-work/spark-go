package main

import (
	_ "embed"
	"fmt"
	"log"
	"math"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/zackzhou-work/spark-go/internal/monitor"
)

var (
	//go:embed icons/pin-filled.svg
	pinFilledSVG []byte
	//go:embed icons/pin-outline.svg
	pinOutlineSVG []byte
	//go:embed icons/resize-grip.svg
	resizeGripSVG []byte
	//go:embed icons/chevron-left.svg
	chevronLeftSVG []byte

	pinFilledIcon   = ui.MustParseSVG(pinFilledSVG)
	pinOutlineIcon  = ui.MustParseSVG(pinOutlineSVG)
	resizeGripIcon  = ui.MustParseSVG(resizeGripSVG)
	chevronLeftIcon = ui.MustParseSVG(chevronLeftSVG)
)

// palette is a monochrome interface where colour comes only from the characters; the
// light and dark sets follow the system appearance
type palette struct {
	bg               ui.Color
	ink, strong, sec ui.Color
	// tileHot is a card that needs you, tile is any other card
	tileHot, tile        ui.Color
	fill, rowHover       ui.Color
	pillHover            ui.Color
	primary, onPrimary   ui.Color
	primaryHover         ui.Color
	edge, hairline, line ui.Color
	shadow               ui.Color
}

var (
	lightPalette = palette{
		bg:  ui.Hex("#FFFFFF"),
		ink: ui.Hex("#34322D"), strong: ui.Hex("#0D0D0D"), sec: ui.Hex("#858481"),
		tileHot: ui.Hex("#F4F4F4"), tile: ui.Hex("#F8F8F7"),
		fill: ui.Hex("#37352F").Alpha(0.06), rowHover: ui.Hex("#37352F").Alpha(0.04),
		pillHover: ui.Hex("#37352F").Alpha(0.1),
		primary:   ui.Hex("#1A1A19"), onPrimary: ui.Hex("#FFFFFF"), primaryHover: ui.Hex("#3A3A38"),
		edge: ui.Hex("#000000").Alpha(0.12), hairline: ui.Hex("#000000").Alpha(0.12), line: ui.Hex("#000000").Alpha(0.08),
		shadow: ui.Hex("#000000").Alpha(0.04),
	}
	darkPalette = palette{
		bg:  ui.Hex("#1A1918"),
		ink: ui.Hex("#E6E4DF"), strong: ui.Hex("#F7F6F2"), sec: ui.Hex("#9B9993"),
		tileHot: ui.Hex("#2B2A27"), tile: ui.Hex("#232220"),
		fill: ui.Hex("#FFFFFF").Alpha(0.08), rowHover: ui.Hex("#FFFFFF").Alpha(0.05),
		pillHover: ui.Hex("#FFFFFF").Alpha(0.12),
		primary:   ui.Hex("#F2F1ED"), onPrimary: ui.Hex("#1A1918"), primaryHover: ui.Hex("#D9D7D2"),
		edge: ui.Hex("#FFFFFF").Alpha(0.12), hairline: ui.Hex("#FFFFFF").Alpha(0.1), line: ui.Hex("#FFFFFF").Alpha(0.1),
		shadow: ui.Hex("#000000").Alpha(0.3),
	}
)

const (
	filterToday = 0
	filterAll   = 1

	// Switching Today / All slides the pages and the button pill together: fast then slow,
	// easing to a stop
	slideDuration = 260 * time.Millisecond

	// Traffic light position is fixed when the window is created, so every title bar uses
	// this height to keep them aligned with the pin
	titleBarHeight = 48
	tileRadius     = 24
	markerSize     = 8

	// Two width layouts: below narrowWidth the cards stack. Height doesn't matter; a short
	// window just scrolls
	narrowWidth = 340
	// minWidth is as narrow as the window goes; past it even the narrow cards stop fitting
	minWidth = 260

	// The working spinner: 30 frames reads clearly without redrawing at the display's rate
	spinPeriod = 900 * time.Millisecond
	spinTick   = 33 * time.Millisecond

	rowHoverFade = 140 * time.Millisecond

	hookGuideURL = "https://github.com/zackzhou-work/spark-go#installing-the-hook"
)

var slideEase = ui.EaseOut

type layout int

const (
	layoutRegular layout = iota
	layoutNarrow
)

func layoutFor(w float32) layout {
	if w < narrowWidth {
		return layoutNarrow
	}
	return layoutRegular
}

type app struct {
	win    *mygo.Window
	groups []monitor.ProjectGroup
	filter int
	// A non-empty project shows that project's detail page
	project string
	pinned  bool
	// snoozed records waiting sessions dismissed with Later, keyed by the ActivityAt at the
	// time; any new activity brings them back
	snoozed map[string]time.Time
	// hookInstalled covers Claude only; each side's hook installs separately and the banner
	// prompts for them individually
	hookInstalled      bool
	codexHookInstalled bool
	// cast is the character each project was given, handed out across all projects at once so
	// that projects on screen clash as little as possible
	cast map[string]character
}

func (a *app) view(c *ui.Context) {
	c.Root().Background(ui.Transparent)
	p := lightPalette
	if c.Theme().Dark {
		p = darkPalette
	}
	w, _ := c.Size()
	l := layoutFor(w)
	a.cast = castCharacters(a.groups)

	ui.Column(c).Fill().Radius(12).Border(1, p.edge).Background(p.bg).TextColor(p.ink).Clip().Children(func() {
		a.titleBar(c, p, l)
		if g, ok := a.openProject(); ok {
			a.projectPage(c.Key("project:"+g.ProjectName), p, g)
		} else {
			a.pages(c, p, l)
		}
		ui.Box(c).Absolute().Bottom(3).Right(3).Size(14, 14).Center().Cursor(ui.CursorResizeNWSE).Children(func() {
			ui.Icon(c, resizeGripIcon).Size(10, 10).TextColor(p.sec.Alpha(0.5))
		})
	})
}

func (a *app) openProject() (monitor.ProjectGroup, bool) {
	if a.project == "" {
		return monitor.ProjectGroup{}, false
	}
	for _, g := range a.groups {
		if g.ProjectName == a.project {
			return g, true
		}
	}
	return monitor.ProjectGroup{}, false
}

func (a *app) titleBar(c *ui.Context, p palette, l layout) {
	bar := ui.Row(c).Height(titleBarHeight).Padding(0, 12, 0, 14).Gap(8).DragWindow()
	room, pin := float32(60), float32(16)
	if l != layoutRegular {
		bar.Padding(0, 8, 0, 12).Gap(6)
		room, pin = 56, 14
	}
	bar.Children(func() {
		ui.Box(c).Size(room, 22)
		ui.Row(c).Grow(1).Justify(ui.Center).Children(func() {
			if a.project == "" {
				a.tabs(c, p, l)
			}
		})
		a.pinButton(c, p, 28, pin)
	})
}

func (a *app) tabs(c *ui.Context, p palette, l layout) {
	height, pad, size, gap := float32(28), float32(12), float32(13), float32(4)
	if l == layoutNarrow {
		height, pad, size, gap = 26, 10, 12, 2
	}
	labels := [2]string{"Today", "All"}
	// Fixed per-segment widths let the pill line up with the labels
	var widths [2]float32
	for i, label := range labels {
		widths[i] = float32(math.Ceil(float64(textWidth(shaped(label, size, 510))))) + 2*pad
	}

	seg := ui.SegmentedBase(c, &a.filter, 2)
	track := seg.Track.Gap(gap).Label("Filter")
	// The selection is a pill drawn on the track, sliding with the pages on the same curve
	x := track.AnimateWith("thumb-x", float32(a.filter)*(widths[0]+gap), slideDuration, slideEase)
	w := track.AnimateWith("thumb-w", widths[a.filter], slideDuration, slideEase)
	track.Draw(func(pt *ui.Painter, r ui.Rect) {
		pt.Fill(ui.Rect{X: r.X + x, Y: r.Y, W: w, H: height}, p.fill, height/2)
	})
	track.Children(func() {
		for i, label := range labels {
			s := seg.Segment(i).Size(widths[i], height).Center().Radius(height / 2).Cursor(ui.CursorPointer).Label(label)
			if i != a.filter && s.Hovered() {
				s.Background(p.rowHover)
			}
			s.Children(func() {
				ui.Text(c, label).FontSize(size).FontWeight(510)
			})
		}
	})
}

func (a *app) pinButton(c *ui.Context, p palette, size, icon float32) {
	pin := ui.Box(c).Size(size, size).Center().Radius(size / 2).Cursor(ui.CursorPointer).Label("Keep window on top")
	if pin.Hovered() {
		pin.Background(p.fill)
	}
	pin.OnClick(func() {
		a.pinned = !a.pinned
		if a.win != nil {
			a.win.SetAlwaysOnTop(a.pinned)
		}
	})
	pin.Children(func() {
		if a.pinned {
			ui.Icon(c, pinFilledIcon).Size(icon, icon).TextColor(p.ink)
		} else {
			ui.Icon(c, pinOutlineIcon).Size(icon, icon).TextColor(p.sec)
		}
	})
}

// pages puts Today and All side by side on one track that slides when switching; at rest
// only the current page is built
func (a *app) pages(c *ui.Context, p palette, l layout) {
	w, _ := c.Size()
	pageWidth := max(w-2, 200)

	ui.Box(c).Grow(1).ClipX().Children(func() {
		track := ui.Row(c).Absolute().Top(0).Bottom(0).AlignItems(ui.Stretch)
		progress := track.AnimateWith("slide", float32(a.filter), slideDuration, slideEase)
		sliding := progress != 0 && progress != 1

		offset := -progress * pageWidth
		if !sliding {
			offset = 0
		}
		track.Left(offset).Children(func() {
			if sliding || a.filter == filterToday {
				a.todayPage(c.Key("today"), p, l, pageWidth)
			}
			if sliding || a.filter == filterAll {
				a.allPage(c.Key("all"), p, l, pageWidth)
			}
		})
	})
}

func (a *app) todayPage(c *ui.Context, p palette, l layout, width float32) {
	today := monitor.TodayGroups(a.groups)
	if len(today) == 0 {
		a.emptyState(c, p, filterToday, width)
		return
	}
	cards := projectCards(today)
	needs, rest := splitNeeds(cards)
	recent := ordered(unwaited(today))
	now := time.Now()

	scroller(c).Width(width).Children(func() {
		if l == layoutNarrow {
			ui.Column(c).Padding(0, 10).Gap(8).Children(func() {
				pageHeading(c, p, l, "Your agents, today.")
				for _, card := range needs {
					a.needsStack(c.Key(card.group.ProjectName), p, card)
				}
				if len(rest) > 0 {
					ui.Column(c).Background(p.tile).Radius(20).Padding(4).Children(func() {
						for _, card := range rest {
							a.projectRow(c.Key(card.group.ProjectName), p, card)
						}
					})
				}
			})
			if len(recent) > 0 {
				ui.Column(c).Padding(14, 6, 16, 6).Children(func() {
					sectionLabel(c, p, "Recent").Padding(0, 8, 6, 8)
					for _, s := range recent {
						sessionRow(c.Key(s.ID), p, s, narrowRows, now)
					}
				})
			}
			return
		}

		ui.Column(c).Padding(4, 12, 16, 12).Children(func() {
			pageHeading(c, p, l, "Your agents, today.")
			ui.Column(c).Gap(8).Children(func() {
				for _, card := range needs {
					a.needsRow(c.Key(card.group.ProjectName), p, card)
				}
				if len(rest) > 0 {
					ui.Grid(c).Columns(fitColumns(width-24, 150, 8)).Gap(8).Children(func() {
						for _, card := range rest {
							a.smallTile(c.Key(card.group.ProjectName), p, card)
						}
					})
				}
			})
			a.sessionSection(c.Key("recent"), p, "Recent", recent, now)
		})
	})
}

// fitColumns is CSS's repeat(auto-fill, minmax(min, 1fr)): as many columns as fit, at
// least one
func fitColumns(width, min, gap float32) int {
	return max(1, int((width+gap)/(min+gap)))
}

func splitNeeds(cards []projectCard) (needs, rest []projectCard) {
	for _, card := range cards {
		if card.mood == moodNeedsYou {
			needs = append(needs, card)
		} else {
			rest = append(rest, card)
		}
	}
	return needs, rest
}

func (a *app) allPage(c *ui.Context, p palette, l layout, width float32) {
	if len(a.groups) == 0 {
		a.emptyState(c, p, filterAll, width)
		return
	}
	now := time.Now()
	scroller(c).Width(width).Children(func() {
		ui.Column(c).Padding(4, 12, 16, 12).Children(func() {
			header := ui.Row(c).Padding(0, 4, 14, 4).Justify(ui.SpaceBetween).AlignItems(ui.End).Gap(8)
			if l == layoutNarrow {
				header.Padding(0, 4, 10, 4).Wrap()
			}
			header.Children(func() {
				pageHeading(c, p, l, "Every agent.").Padding(0)
				ui.Text(c, plural(len(a.groups), "project")+" · "+plural(monitor.CountSessions(a.groups), "session")).
					FontSize(12).LineHeight(1.5).FontFeatures("tnum").TextColor(p.sec)
			})
			ui.Grid(c).Columns(fitColumns(width-24, 100, 8)).Gap(8).Children(func() {
				for _, card := range projectCards(a.groups) {
					a.castTile(c.Key(card.group.ProjectName), p, card)
				}
			})
			for _, day := range byDay(ordered(unwaited(a.groups)), now) {
				a.sessionSection(c.Key(day.label), p, day.label, day.sessions, now)
			}
		})
	})
}

func heading(c *ui.Context, p palette, s string) ui.Element {
	return ui.Text(c, s).FontSize(24).LineHeight(30.0 / 24).FontWeight(590).LetterSpacing(-0.5).TextColor(p.strong)
}

// scroller scrolls as usual but moves the scrollbar outside the window: it isn't drawn and
// doesn't eat clicks along the right edge
func scroller(c *ui.Context) ui.Element {
	return ui.Scroll(c).ScrollbarInsets(0, -40, 0, 0)
}

// pageHeading is the line atop the Today and All pages; one size smaller in a narrow
// window to save height
func pageHeading(c *ui.Context, p palette, l layout, s string) ui.Element {
	if l == layoutNarrow {
		// Stacked cards already have 8 between them, so 2 more makes 10
		return ui.Text(c, s).FontSize(20).LineHeight(26.0/20).FontWeight(590).LetterSpacing(-0.4).TextColor(p.strong).Padding(0, 4, 2, 4)
	}
	return heading(c, p, s).Padding(0, 4, 14, 4)
}

// needsRow is a project needing you: a full row with a button on the right that returns to
// the session
func (a *app) needsRow(c *ui.Context, p palette, card projectCard) {
	tile := ui.Row(c).Background(p.tileHot).Radius(tileRadius).Padding(14, 12, 14, 16).Gap(14).
		Cursor(ui.CursorPointer).Label(card.group.ProjectName + ", needs you")
	tile.OnClick(func() { a.project = card.group.ProjectName })
	tile.Children(func() {
		a.avatar(c, card.group.ProjectName, card.mood, dressed(card.prop, p, p.tileHot), 40)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, card.group.ProjectName).SingleLine().FontSize(16).LineHeight(20.0 / 16).FontWeight(510)
			ui.Text(c, waitLine(card.waiting.WaitReason)).SingleLine().FontSize(14).LineHeight(20.0 / 14).TextColor(p.sec)
		})
		a.respondButton(c, p, card.waiting, 32, 14).Shrink(0)
	})
}

// needsStack is the narrow-window needs-you card: avatar and name on one line, the button
// filling the next
func (a *app) needsStack(c *ui.Context, p palette, card projectCard) {
	tile := ui.Column(c).Background(p.tileHot).Radius(20).Padding(14).Gap(10).
		Cursor(ui.CursorPointer).Label(card.group.ProjectName + ", needs you")
	tile.OnClick(func() { a.project = card.group.ProjectName })
	tile.Children(func() {
		ui.Row(c).Gap(12).Children(func() {
			a.avatar(c, card.group.ProjectName, card.mood, dressed(card.prop, p, p.tileHot), 34)
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, card.group.ProjectName).SingleLine().FontSize(15).LineHeight(20.0 / 15).FontWeight(510)
				ui.Text(c, waitLine(card.waiting.WaitReason)).SingleLine().FontSize(13).LineHeight(18.0 / 13).TextColor(p.sec)
			})
		})
		a.respondButton(c, p, card.waiting, 32, 13).FillWidth()
	})
}

// respondButton returns to a stuck session: a dark Respond for an approval, a light Answer
// for a question
func (a *app) respondButton(c *ui.Context, p palette, s monitor.SessionItem, height, size float32) ui.Element {
	label, primary := "Respond", true
	if s.WaitReason == monitor.WaitQuestion {
		label, primary = "Answer", false
	}
	return pillButton(c, p, label, primary, height, size).OnClick(func() { jumpToSession(c, s) })
}

func waitLine(r monitor.WaitReason) string {
	if r == monitor.WaitQuestion {
		return "Asked you a question"
	}
	return "Wants permission"
}

func (a *app) smallTile(c *ui.Context, p palette, card projectCard) {
	status := card.status()
	if card.mood == moodWorking {
		status = fmt.Sprintf("Working on %d", card.running)
	}
	tile := ui.Row(c).Background(p.tile).Radius(20).Padding(12, 14).Gap(12).MinWidth(0).
		Cursor(ui.CursorPointer).Label(card.group.ProjectName + ", " + status)
	tile.OnClick(func() { a.project = card.group.ProjectName })
	tile.Children(func() {
		ui.Box(c).Margin(0, badgeRoom(card, 30), 0, 0).Children(func() {
			a.avatar(c, card.group.ProjectName, card.mood, dressed(card.prop, p, p.tile), 30)
		})
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, card.group.ProjectName).SingleLine().FontSize(14).LineHeight(20.0 / 14).FontWeight(510)
			ui.Text(c, status).SingleLine().FontSize(12).LineHeight(1.5).TextColor(p.sec)
		})
	})
}

// projectRow is a narrow-window project that doesn't need you; several share one card, one
// per line
func (a *app) projectRow(c *ui.Context, p palette, card projectCard) {
	row := ui.Row(c).Height(44).Padding(0, 12).Gap(12).Radius(16).Cursor(ui.CursorPointer).
		Label(card.group.ProjectName + ", " + card.status())
	hoverFill(row, row.Hovered(), p.rowHover)
	row.OnClick(func() { a.project = card.group.ProjectName })
	row.Children(func() {
		// Every row reserves the same width for the avatar, so a badged row's name doesn't shift
		// right
		ui.Box(c).Width(26 + badgeRoom(projectCard{mood: moodToRead}, 26)).Children(func() {
			a.avatar(c, card.group.ProjectName, card.mood, dressed(card.prop, p, p.tile), 26)
		})
		ui.Text(c, card.group.ProjectName).SingleLine().Grow(1).MinWidth(0).FontSize(14).FontWeight(510)
		ui.Text(c, card.status()).Shrink(0).FontSize(12).TextColor(p.sec)
	})
}

// dressed keeps a character's count badge in step with the card background and light/dark
// palette
func dressed(pr prop, p palette, ground ui.Color) prop {
	pr.ring, pr.fill, pr.ink = ground, p.primary, p.onPrimary
	return pr
}

// badgeRoom is the part of the count badge sticking out past the avatar; this makes room
func badgeRoom(card projectCard, size float32) float32 {
	if card.mood == moodToRead {
		return size * 9 / 64
	}
	return 0
}

// castTile is the All page's character wall, one cell per project
func (a *app) castTile(c *ui.Context, p palette, card projectCard) {
	bg := p.tile
	if card.mood == moodNeedsYou {
		bg = p.tileHot
	}
	tile := ui.Column(c).Background(bg).Radius(tileRadius).Padding(14, 10, 12, 10).Gap(8).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Label(card.group.ProjectName + ", " + card.status())
	tile.OnClick(func() { a.project = card.group.ProjectName })
	tile.Children(func() {
		a.avatar(c, card.group.ProjectName, card.mood, dressed(card.prop, p, bg), 36)
		ui.Column(c).AlignItems(ui.Center).MinWidth(0).FillWidth().Children(func() {
			ui.Text(c, card.group.ProjectName).SingleLine().TextAlign(ui.Center).FontSize(14).LineHeight(20.0 / 14).FontWeight(510)
			status := ui.Text(c, card.status()).SingleLine().TextAlign(ui.Center).FontSize(12).LineHeight(1.5)
			if card.mood == moodNeedsYou {
				status.FontWeight(510).TextColor(p.strong)
			} else {
				status.TextColor(p.sec)
			}
		})
	})
}

func (a *app) projectPage(c *ui.Context, p palette, g monitor.ProjectGroup) {
	m, pr := projectLook(g)
	now := time.Now()
	back := "Today"
	if a.filter == filterAll {
		back = "All projects"
	}
	scroller(c).Grow(1).Children(func() {
		ui.Column(c).Padding(0, 12, 16, 12).Gap(12).Children(func() {
			btn := ui.Row(c).AlignSelf(ui.Start).Height(32).Padding(0, 12, 0, 8).Gap(4).Radius(16).
				Background(p.fill).Cursor(ui.CursorPointer).Label("Back to " + back)
			if btn.Hovered() {
				btn.Background(p.pillHover)
			}
			btn.OnClick(func() { a.project = "" })
			btn.Children(func() {
				ui.Icon(c, chevronLeftIcon).Size(16, 16).TextColor(p.ink)
				ui.Text(c, back).FontSize(14).FontWeight(510)
			})

			ui.Column(c).Background(p.tileHot).Radius(tileRadius).Padding(24, 16, 20, 16).Gap(14).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Padding(8, 0, 0, 0).Children(func() { a.avatar(c, g.ProjectName, m, dressed(pr, p, p.tileHot), 72) })
				ui.Column(c).Gap(4).AlignItems(ui.Center).MinWidth(0).FillWidth().Children(func() {
					heading(c, p, g.ProjectName).SingleLine().TextAlign(ui.Center)
					ui.Text(c, projectSummary(g)).FontSize(14).LineHeight(20.0 / 14).TextAlign(ui.Center).TextColor(p.sec)
				})
			})

			for _, s := range g.Sessions {
				if s.State == monitor.Waiting && !a.isSnoozed(s) {
					a.waitingCard(c.Key(s.ID), p, s, now)
				}
			}

			var rows []monitor.SessionItem
			for _, s := range g.Sessions {
				if s.State != monitor.Waiting || a.isSnoozed(s) {
					rows = append(rows, s)
				}
			}
			if len(rows) > 0 {
				ui.Column(c).Padding(4, 0, 0, 0).Children(func() {
					sectionLabel(c, p, "Sessions")
					for _, s := range ordered(rows) {
						sessionRow(c.Key(s.ID), p, s, regularRows, now)
					}
				})
			}
		})
	})
}

func (a *app) isSnoozed(s monitor.SessionItem) bool {
	at, ok := a.snoozed[s.ID]
	return ok && at.Equal(s.ActivityAt)
}

// waitingCard singles out a stuck session: what it's waiting for, how long, and buttons to
// go handle it or put it off
func (a *app) waitingCard(c *ui.Context, p palette, s monitor.SessionItem, now time.Time) {
	card(c, p).Padding(16).Gap(12).Children(func() {
		ui.Column(c).Gap(4).Children(func() {
			meta := "Permission"
			if s.WaitReason == monitor.WaitQuestion {
				meta = "Question"
			}
			if when := relativeTime(s.ActivityAt, now); when != "" {
				meta += " · " + when
			}
			ui.Text(c, meta).FontSize(12).LineHeight(1.5).TextColor(p.sec)
			ui.Text(c, s.Title).FontSize(16).LineHeight(1.5).TextColor(p.strong)
		})
		ui.Row(c).Gap(8).Wrap().Children(func() {
			label := "Respond in Claude"
			if s.WaitReason == monitor.WaitQuestion {
				label = "Answer in Claude"
			}
			pillButton(c, p, label, true, 36, 14).OnClick(func() { jumpToSession(c, s) })
			pillButton(c, p, "Later", false, 36, 14).OnClick(func() {
				if a.snoozed == nil {
					a.snoozed = map[string]time.Time{}
				}
				a.snoozed[s.ID] = s.ActivityAt
			})
		})
	})
}

func projectSummary(g monitor.ProjectGroup) string {
	var question, permission, working, unread, done int
	for _, s := range g.Sessions {
		switch s.State {
		case monitor.Waiting:
			if s.WaitReason == monitor.WaitQuestion {
				question++
			} else {
				permission++
			}
		case monitor.Running:
			working++
		case monitor.Unread:
			unread++
		default:
			done++
		}
	}
	var parts []string
	add := func(n int, s string) {
		if n > 0 {
			parts = append(parts, s)
		}
	}
	add(question, plural(question, "question"))
	add(permission, plural(permission, "permission"))
	add(working, fmt.Sprintf("%d working", working))
	add(unread, fmt.Sprintf("%d to read", unread))
	add(done, fmt.Sprintf("%d done", done))
	return strings.Join(parts, " · ")
}

// card is a white card with a hairline border and a soft shadow
func card(c *ui.Context, p palette) ui.Element {
	return ui.Column(c).Radius(tileRadius).Border(0.5, p.hairline).Shadow(0, 4, 6, 0, p.shadow).Background(p.bg)
}

func pillButton(c *ui.Context, p palette, label string, primary bool, height, size float32) ui.Element {
	btn := ui.Row(c).Height(height).Padding(0, height/2-2).Radius(height / 2).Center().Cursor(ui.CursorPointer).Label(label)
	bg, fg := p.fill, p.ink
	if primary {
		bg, fg = p.primary, p.onPrimary
	}
	if btn.Hovered() {
		bg = p.pillHover
		if primary {
			bg = p.primaryHover
		}
	}
	btn.Background(bg)
	btn.Children(func() {
		ui.Text(c, label).FontSize(size).FontWeight(510).TextColor(fg)
	})
	return btn
}

func sectionLabel(c *ui.Context, p palette, s string) ui.Element {
	return ui.Text(c, s).FontSize(12).LineHeight(1.5).Padding(0, 4, 8, 4).TextColor(p.sec)
}

func (a *app) sessionSection(c *ui.Context, p palette, title string, sessions []monitor.SessionItem, now time.Time) {
	if len(sessions) == 0 {
		return
	}
	ui.Column(c).Padding(16, 0, 0, 0).Children(func() {
		sectionLabel(c, p, title)
		card(c, p).Padding(6).Children(func() {
			for _, s := range sessions {
				sessionRow(c.Key(s.ID), p, s, regularRows, now)
			}
		})
	})
}

// rowStyle holds session row metrics per width; a narrow window has no room for the time
// and keeps only the New pill for unread
type rowStyle struct {
	height, radius, padding, gap float32
	font, meta                   float32
	pill                         float32
	timeForUnread                bool
}

var (
	regularRows = rowStyle{height: 40, radius: 16, padding: 12, gap: 10, font: 14, meta: 12, pill: 20, timeForUnread: true}
	narrowRows  = rowStyle{height: 36, radius: 14, padding: 10, gap: 10, font: 13, meta: 11, pill: 18}
)

func sessionRow(c *ui.Context, p palette, s monitor.SessionItem, st rowStyle, now time.Time) {
	row := ui.Row(c).Height(st.height).Gap(st.gap).Padding(0, st.padding).Radius(st.radius).Cursor(ui.CursorPointer).
		Label(s.Title + ", " + s.State.String())
	hovered := row.Hovered()
	hoverFill(row, hovered, p.rowHover)
	row.OnClick(func() { jumpToSession(c, s) })

	when := relativeTime(s.ActivityAt, now)
	switch {
	case s.State == monitor.Running:
		when = "now"
		if !st.timeForUnread {
			when = ""
		}
	case s.State == monitor.Unread && !st.timeForUnread:
		when = ""
	}
	row.Children(func() {
		ui.Row(c).Width(markerSize).Center().Children(func() { marker(c, p, s.State) })
		title := ui.Text(c, s.Title).SingleLine().Grow(1).MinWidth(0).FontSize(st.font)
		switch s.State {
		case monitor.Unread:
			title.TextColor(p.strong)
		case monitor.Completed:
			title.TextColor(p.sec)
		}
		if when != "" || hovered {
			trailing(c, p, hovered, when, st.meta)
		}
		if s.State == monitor.Unread {
			newPill(c, p, st.pill)
		}
	})
}

// newPill is the black New at the end of an unread session row
func newPill(c *ui.Context, p palette, height float32) {
	ui.Row(c).Height(height).Padding(0, height*0.4).Radius(height / 2).Center().Shrink(0).Background(p.primary).Children(func() {
		ui.Text(c, "New").FontSize(height * 0.6).FontWeight(510).TextColor(p.onPrimary)
	})
}

// hoverFill fades a row's hover background in and out instead of snapping
func hoverFill(row ui.Element, hovered bool, color ui.Color) {
	target := float32(0)
	if hovered {
		target = 1
	}
	if t := row.Animate("hover", target, rowHoverFade); t > 0 {
		row.Background(color.Alpha(t))
	}
}

// trailing shows how long ago by default, swapping to a jump arrow on hover
func trailing(c *ui.Context, p palette, hovered bool, when string, size float32) {
	label := when
	if hovered {
		label = "→"
	}
	ui.Text(c, label).Shrink(0).FontSize(size).FontFeatures("tnum").TextColor(p.sec)
}

func relativeTime(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
}

// marker is the leading glyph, ink only: a spinner while running, a filled dot when unread,
// a hollow ring once seen, a solid ring when waiting on you
func marker(c *ui.Context, p palette, state monitor.TaskState) {
	dot := ui.Box(c).Size(markerSize, markerSize)
	switch state {
	case monitor.Running:
		still := c.Preferences().ReduceMotion
		// Not Loop: that redraws the whole window at the display's rate. Stepping on a fixed
		// interval inside Draw repaints without rebuilding the view
		dot.Draw(func(pt *ui.Painter, r ui.Rect) {
			turn := float32(0)
			if !still {
				turn = spinPhase(pt.Now())
				pt.After(spinTick)
			}
			paintSpinner(pt, r, p, turn)
		})
	case monitor.Waiting:
		dot.Radius(markerSize/2).Border(1.5, p.strong)
	case monitor.Unread:
		dot.Radius(markerSize / 2).Background(p.strong)
	default:
		dot.Radius(markerSize/2).Border(1.5, p.sec)
	}
}

func spinPhase(now time.Time) float32 {
	period := spinPeriod.Milliseconds()
	return float32(now.UnixMilli()%period) / float32(period)
}

// paintSpinner draws a faint track with a quarter arc in ink; turn counts revolutions
func paintSpinner(pt *ui.Painter, r ui.Rect, p palette, turn float32) {
	const width = 1.5
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	radius := (r.W - width) / 2

	var track ui.Path
	track.Circle(cx, cy, radius)
	pt.StrokePath(&track, width, p.ink.Alpha(0.18))

	var arc ui.Path
	const steps = 8
	start := (float64(turn) - 0.375) * 2 * math.Pi
	for i := 0; i <= steps; i++ {
		a := start + float64(i)/steps*math.Pi/2
		x, y := cx+radius*float32(math.Cos(a)), cy+radius*float32(math.Sin(a))
		if i == 0 {
			arc.MoveTo(x, y)
		} else {
			arc.LineTo(x, y)
		}
	}
	pt.StrokePath(&arc, width, p.ink)
}

// restingCast is the five sleeping characters in the empty state, staggered in height and
// spread across the body shapes
var restingCast = []struct {
	who  character
	size float32
}{{character{0, 0}, 38}, {character{1, 4}, 46}, {character{4, 3}, 42}, {character{2, 7}, 38}, {character{3, 6}, 36}}

func (a *app) emptyState(c *ui.Context, p palette, filter int, width float32) {
	ui.Column(c).Width(width).Children(func() {
		ui.Column(c).Grow(1).Margin(4, 12, 0, 12).Background(p.tileHot).Radius(tileRadius).Padding(24).Gap(24).
			AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.End).Children(func() {
				for i, who := range restingCast {
					avatarOf(c.Key(i), who.who, uint32(i), moodCaughtUp, prop{}, who.size)
				}
			})
			ui.Column(c).Gap(8).AlignItems(ui.Center).Children(func() {
				title, body := "Everyone’s resting.", "Nothing ran today. Sessions you start in Claude’s Code tab show up here."
				if filter == filterAll {
					title, body = "No sessions yet.", "Sessions you start in Claude’s Code tab show up here."
				}
				ui.Text(c, title).FontSize(28).LineHeight(34.0 / 28).FontWeight(590).LetterSpacing(-0.75).TextAlign(ui.Center).TextColor(p.strong)
				ui.Text(c, body).FontSize(16).LineHeight(1.5).TextAlign(ui.Center).TextColor(p.sec)
			})
			if filter == filterToday {
				pillButton(c, p, "Show all sessions", true, 40, 14).OnClick(func() { a.filter = filterAll })
			}
		})
		if !a.hookInstalled || !a.codexHookInstalled {
			a.hookBanner(c, p)
		} else {
			ui.Box(c).Height(12)
		}
	})
}

// hookBanner prompts for the hook: without it an approval prompt can only be guessed at.
// Claude and Codex hooks install separately, so whichever is missing gets offered
func (a *app) hookBanner(c *ui.Context, p palette) {
	ui.Row(c).Margin(12).Radius(16).Border(1, p.line).Padding(12, 12, 12, 16).Gap(12).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, "Hear about prompts right away").FontSize(14).LineHeight(20.0 / 14).FontWeight(510)
			ui.Text(c, "Install the hook so spark doesn’t have to guess.").FontSize(12).LineHeight(1.5).TextColor(p.sec)
		})
		if !a.hookInstalled {
			pillButton(c, p, "Install", false, 32, 14).OnClick(func() { openURL(c, hookGuideURL) })
		}
		if !a.codexHookInstalled {
			pillButton(c, p, "Install for Codex", false, 32, 14).OnClick(func() {
				home, _ := os.UserHomeDir()
				if err := monitor.InstallCodexHookScript(home); err != nil {
					log.Printf("[Hooks] writing the Codex hook script failed: %v", err)
					return
				}
				if err := monitor.InstallCodexHooks(home, monitor.CodexHookScriptPath(home)); err != nil {
					log.Printf("[Hooks] installing the Codex hook failed: %v", err)
					return
				}
				a.codexHookInstalled = true
			})
		}
	})
}

// projectCard is what a project shows on its card
type projectCard struct {
	group   monitor.ProjectGroup
	mood    mood
	prop    prop
	waiting monitor.SessionItem
	running int
}

func (card projectCard) status() string {
	switch card.mood {
	case moodNeedsYou:
		return "Needs you"
	case moodWorking:
		return "Working"
	case moodToRead:
		return fmt.Sprintf("%d to read", card.prop.count)
	default:
		return "All caught up"
	}
}

// projectCards sorts by how urgent the face is: needs you, working, to read, all caught up;
// order within a tier is preserved
func projectCards(groups []monitor.ProjectGroup) []projectCard {
	cards := make([]projectCard, 0, len(groups))
	for _, g := range groups {
		m, pr := projectLook(g)
		card := projectCard{group: g, mood: m, prop: pr}
		for _, s := range g.Sessions {
			switch s.State {
			case monitor.Waiting:
				if card.waiting.ID == "" {
					card.waiting = s
				}
			case monitor.Running:
				card.running++
			}
		}
		cards = append(cards, card)
	}
	slices.SortStableFunc(cards, func(x, y projectCard) int { return int(x.mood) - int(y.mood) })
	return cards
}

// unwaited collects sessions other than the ones needing you, which are already on cards
func unwaited(groups []monitor.ProjectGroup) []monitor.SessionItem {
	var out []monitor.SessionItem
	for _, g := range groups {
		for _, s := range g.Sessions {
			if s.State != monitor.Waiting {
				out = append(out, s)
			}
		}
	}
	return out
}

// ordered puts running first, then unread, then seen; newest first within a tier
func ordered(sessions []monitor.SessionItem) []monitor.SessionItem {
	rank := func(s monitor.TaskState) int {
		switch s {
		case monitor.Running:
			return 0
		case monitor.Waiting:
			return 1
		case monitor.Unread:
			return 2
		default:
			return 3
		}
	}
	out := slices.Clone(sessions)
	slices.SortStableFunc(out, func(x, y monitor.SessionItem) int {
		if d := rank(x.State) - rank(y.State); d != 0 {
			return d
		}
		return y.ActivityAt.Compare(x.ActivityAt)
	})
	return out
}

type daySessions struct {
	label    string
	sessions []monitor.SessionItem
}

// byDay splits by last activity into Today, Yesterday and Earlier, keeping the incoming
// order
func byDay(sessions []monitor.SessionItem, now time.Time) []daySessions {
	days := []daySessions{{label: "Today"}, {label: "Yesterday"}, {label: "Earlier"}}
	y, m, d := now.Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	for _, s := range sessions {
		switch {
		case !s.ActivityAt.Before(midnight):
			days[0].sessions = append(days[0].sessions, s)
		case !s.ActivityAt.Before(midnight.AddDate(0, 0, -1)):
			days[1].sessions = append(days[1].sessions, s)
		default:
			days[2].sessions = append(days[2].sessions, s)
		}
	}
	return days
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// jumpToSession jumps to a session: each side uses its own desktop deep link
func jumpToSession(c *ui.Context, s monitor.SessionItem) {
	var (
		url string
		ok  bool
	)
	if s.Source == monitor.SourceCodex {
		url, ok = monitor.CodexDeeplink(s.ID)
	} else {
		url, ok = monitor.SessionDeeplink(s.ID)
	}
	if !ok {
		log.Printf("[Jump] session ID doesn't match the deep link format, skipping: %s", s.ID)
		return
	}
	openURL(c, url)
}

func openURL(c *ui.Context, url string) {
	c.OpenURLThen(url, func(err error) {
		if err != nil {
			log.Printf("[Open] %v", err)
		}
	})
}
