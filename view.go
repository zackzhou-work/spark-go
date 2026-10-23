package main

import (
	_ "embed"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
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
	//go:embed icons/close.svg
	closeSVG []byte

	pinFilledIcon   = ui.MustParseSVG(pinFilledSVG)
	pinOutlineIcon  = ui.MustParseSVG(pinOutlineSVG)
	resizeGripIcon  = ui.MustParseSVG(resizeGripSVG)
	chevronLeftIcon = ui.MustParseSVG(chevronLeftSVG)
	closeIcon       = ui.MustParseSVG(closeSVG)
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
	// With three hooks missing, this page width leaves enough room for the banner's buttons
	// and a readable prose column on the same row. Below it the buttons stack under the
	// prose, which is also what keeps the empty state's card from being squeezed flat
	hookBannerInlineMinWidth = 650
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
	win *mygo.Window
	// scanner is what the setup notices ask about a source's own reporting; a view build never
	// touches it
	scanner *monitor.Scanner
	groups  []monitor.ProjectGroup
	filter  int
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
	piHookInstalled    bool
	// piState, piInstalledAt and piReported are what the pi setup line and the banner go by.
	// They are read from disk on the scan goroutine rather than in the view: mygo rebuilds a
	// view several times for one event, and a view build must not do filesystem I/O
	piState       monitor.PiExtensionState
	piInstalledAt time.Time
	piReported    bool
	// cursorState is spark's hooks file for Cursor on disk, cursorDetected says Cursor is
	// installed at all, and cursorReported whether the helper has written an event. cursorError
	// keeps a failed install visible without pretending it worked
	cursorState           monitor.CursorHookState
	cursorDetected        bool
	cursorReported        bool
	cursorError           string
	cursorNoticeDismissed bool
	// piNoticeDismissed is the install the setup notice was dismissed for, so a later install
	// brings it back
	piNoticeDismissed time.Time
	// cast is the character each project was given, handed out across all projects at once so
	// that projects on screen clash as little as possible
	cast map[string]character
	// characters are the choices saved on disk, read once at startup and replaced as saves
	// land; charactersErr says the file couldn't be read, so it must not be written over
	characters    characterSettings
	charactersErr error
	charactersAt  string
	// editor is the draft the character editor is showing, nil while it is closed. savedAt is
	// when a character was last saved, which the confirmation goes by
	editor  *characterDraft
	savedAt time.Time
	// characterEntry, editorPanel and editorPreview are the editor's own controls, kept by
	// handle rather than by element: elements expire before the next build
	characterEntry ui.Handle
	editorPanel    ui.Handle
	editorPreview  ui.Handle
}

func (a *app) view(c *ui.Context) {
	c.Root().Background(ui.Transparent)
	p := lightPalette
	if c.Theme().Dark {
		p = darkPalette
	}
	w, _ := c.Size()
	l := layoutFor(w)
	a.cast = castCharacters(a.groups, a.characters)
	// The editor belongs to the project page it was opened from: if that project goes away
	// there is nothing left for its draft to be about
	if d := a.editor; d != nil {
		if g, ok := a.openProject(); !ok || g.ProjectName != d.project {
			a.closeCharacterEditor()
		}
	}

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
		a.savedToast(c, p)
	})
	a.characterEditor(c, p)
}

// savedToast confirms a save for a moment, then takes itself away with one more frame
func (a *app) savedToast(c *ui.Context, p palette) {
	if a.savedAt.IsZero() {
		return
	}
	left := toastSpan - time.Since(a.savedAt)
	if left <= 0 {
		return
	}
	// One frame at the end takes the confirmation away
	c.After(left)
	ui.Row(c).Absolute().Left(0).Right(0).Bottom(22).Justify(ui.Center).PassThrough().Children(func() {
		ui.Row(c).Height(36).Padding(0, 18).Radius(18).Center().Background(p.primary).Children(func() {
			ui.Text(c, "Character saved").FontSize(12).FontWeight(510).TextColor(p.onPrimary)
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
		a.emptyState(c, p, l, filterToday, width)
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
				a.piSetupNotice(c, p)
				a.cursorSetupNotice(c, p)
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
						sessionRow(c.Key(sessionKey(s)), p, s, narrowRows, now)
					}
				})
			}
			return
		}

		ui.Column(c).Padding(4, 12, 16, 12).Children(func() {
			pageHeading(c, p, l, "Your agents, today.")
			a.piSetupNotice(c, p)
			a.cursorSetupNotice(c, p)
			ui.Column(c).Gap(8).Padding(8, 0, 0, 0).Children(func() {
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
		a.emptyState(c, p, l, filterAll, width)
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
		// A session spark can't open has nowhere for this button to go, so it isn't offered;
		// the project page explains why
		if card.waiting.Jumpable() {
			a.respondButton(c, p, card.waiting, 32, 14).Shrink(0)
		}
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
		if card.waiting.Jumpable() {
			a.respondButton(c, p, card.waiting, 32, 13).FillWidth()
		}
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
				// The bottom padding is room for the hint the hover puts under the character
				ui.Box(c).Padding(8, 0, 20, 0).Children(func() { a.characterButton(c, p, g, m, pr) })
				ui.Column(c).Gap(4).AlignItems(ui.Center).MinWidth(0).FillWidth().Children(func() {
					heading(c, p, g.ProjectName).SingleLine().TextAlign(ui.Center)
					ui.Text(c, projectSummary(g)).FontSize(14).LineHeight(20.0 / 14).TextAlign(ui.Center).TextColor(p.sec)
				})
			})

			for _, s := range g.Sessions {
				if s.State == monitor.Waiting && !a.isSnoozed(s) {
					a.waitingCard(c.Key(sessionKey(s)), p, s, now)
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
						sessionRow(c.Key(sessionKey(s)), p, s, regularRows, now)
					}
				})
			}
		})
	})
}

// sessionKey identifies a session across sources: two agents can hand out the same ID string, and
// the snooze map and view keys must not confuse them
func sessionKey(s monitor.SessionItem) string {
	return s.Source.String() + ":" + s.ID
}

func (a *app) isSnoozed(s monitor.SessionItem) bool {
	at, ok := a.snoozed[sessionKey(s)]
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
			if !s.Jumpable() {
				// pi answers a question in its own terminal, and spark has no way to bring
				// that window forward, so the card only explains itself and offers Later. The
				// session still shows up here: you asked to be told
				ui.Text(c, "Waiting in a terminal spark can’t reach").FontSize(12).LineHeight(1.5).TextColor(p.sec)
			}
		})
		ui.Row(c).Gap(8).Wrap().Children(func() {
			if s.Jumpable() {
				pillButton(c, p, answerLabel(s), true, 36, 14).OnClick(func() { jumpToSession(c, s) })
			}
			pillButton(c, p, "Later", false, 36, 14).OnClick(func() {
				if a.snoozed == nil {
					a.snoozed = map[string]time.Time{}
				}
				a.snoozed[sessionKey(s)] = s.ActivityAt
			})
		})
	})
}

// answerLabel names the agent the session is waiting in, since the button goes straight to
// it: a question is answered, a permission prompt is responded to
func answerLabel(s monitor.SessionItem) string {
	verb := "Respond"
	if s.WaitReason == monitor.WaitQuestion {
		verb = "Answer"
	}
	return verb + " in " + s.Source.String()
}

func projectSummary(g monitor.ProjectGroup) string {
	var question, permission, working, unread, unknown, done int
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
		case monitor.Unknown:
			unknown++
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
	if len(parts) == 0 && unknown > 0 {
		// Nothing here can be described, and "0 done" would describe it wrongly
		return "Status unavailable"
	}
	add(unknown, fmt.Sprintf("%d unknown", unknown))
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
				sessionRow(c.Key(sessionKey(s)), p, s, regularRows, now)
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
	row := ui.Row(c).Height(st.height).Gap(st.gap).Padding(0, st.padding).Radius(st.radius).
		Label(s.Title + ", " + s.State.String())
	hovered := row.Hovered()
	// A row is a target only where clicking goes somewhere: a pi session outside Herdr has
	// no window for spark to bring forward
	if s.Jumpable() {
		row.Cursor(ui.CursorPointer)
		row.OnClick(func() { jumpToSession(c, s) })
		hoverFill(row, hovered, p.rowHover)
	}

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
		case monitor.Completed, monitor.Unknown:
			title.TextColor(p.sec)
		}
		if when != "" || hovered {
			trailing(c, p, hovered, when, st.meta, s)
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

// trailing shows how long ago by default, swapping to a jump arrow on hover when clicking
// the row goes somewhere
func trailing(c *ui.Context, p palette, hovered bool, when string, size float32, s monitor.SessionItem) {
	label := when
	if hovered && s.Jumpable() {
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
	case monitor.Unknown:
		// Faint and still: nothing is spinning, and nothing claims to be finished
		dot.Radius(markerSize/2).Border(1.5, p.sec.Alpha(0.45))
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
}{{character{1, 0}, 38}, {character{6, 4}, 46}, {character{0, 3}, 42}, {character{4, 7}, 38}, {character{2, 6}, 36}}

const (
	// restingOverlap is how far each character in the empty state is pulled over the one
	// before it. The five are 200pt of boxes inside a card that is only 186pt wide at the
	// narrowest window, and a fixed Size() box cannot shrink, so without an overlap the row
	// paints straight past the card. 6pt is the most that still leaves every character's
	// expression readable
	restingOverlap = 6
	// restingInk is what the five characters span at 1x: 200pt of boxes less four 6pt
	// overlaps, plus the 1-2pt each outline shape overflows its own box. It is a
	// measurement rather than a sum because those ink extents are worked out while drawing
	// and are not stored on the body
	restingInk = 177.1
	// restingFill is how much of the card the row aims to span once the window is wide
	// enough to grow into, and restingCap stops a wide window from turning the same screen
	// into a different one
	restingFill = 0.72
	restingCap  = 1.3
)

// restingScale is how much the empty state's cast grows in a wide window. The row is the
// only thing there whose size the type does not set, so it is what keeps the card from
// looking empty; it never shrinks below the app's real sizes
func restingScale(width float32) float32 {
	// the card's content box: window − margins (24) − padding (48)
	inner := width - 72
	return min(restingCap, max(1, inner*restingFill/restingInk))
}

func (a *app) emptyState(c *ui.Context, p palette, l layout, filter int, width float32) {
	ui.Column(c).Width(width).Children(func() {
		ui.Column(c).Grow(1).Margin(4, 12, 0, 12).Background(p.tileHot).Radius(tileRadius).Padding(24).Gap(24).
			AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			scale := restingScale(width)
			ui.Row(c).AlignItems(ui.End).Children(func() {
				for i, who := range restingCast {
					cast := avatarOf(c.Key(i), who.who, uint32(i), moodCaughtUp,
						prop{sep: p.tileHot}, who.size*scale)
					if i > 0 {
						// A negative left margin pulls this character over the one before it,
						// so the five overlap rather than running past the card. They paint in
						// order, so the right-hand character is the one on top
						cast.Margin(0, 0, 0, -restingOverlap*scale)
					}
				}
			})
			ui.Column(c).Gap(8).AlignItems(ui.Center).Children(func() {
				title, body := "Everyone’s resting.", "Nothing ran today. Sessions you start in your agents show up here."
				if filter == filterAll {
					title, body = "No sessions yet.", "Sessions you start in your agents show up here."
				}
				ui.Text(c, title).FontSize(28).LineHeight(34.0 / 28).FontWeight(590).LetterSpacing(-0.75).TextAlign(ui.Center).TextColor(p.strong)
				ui.Text(c, body).FontSize(16).LineHeight(1.5).TextAlign(ui.Center).TextColor(p.sec)
			})
			if filter == filterToday {
				pillButton(c, p, "Show all sessions", true, 40, 14).OnClick(func() { a.filter = filterAll })
			}
		})
		a.cursorSetupNotice(c, p)
		if !a.hookInstalled || !a.codexHookInstalled || !a.piHookInstalled || a.piNeedsReload() || a.cursorNeedsSetup() {
			a.hookBanner(c, p, l)
		} else {
			ui.Box(c).Height(12)
		}
	})
}

// piNeedsReload reports whether spark still has to say that pi sessions already running
// haven't loaded the extension: the file is in place, but no receipt has been written since
// it landed. pi loads extensions when it starts, so writing the file changes nothing for a
// session that is already up
func (a *app) piNeedsReload() bool {
	return a.piInstallWaiting()
}

// applySideState records what the scan read about the hooks and pi's setup, so a view build
// never has to touch the disk
func (a *app) applySideState(side sideState) {
	a.hookInstalled, a.codexHookInstalled = side.hooked, side.codexHooked
	a.applyPiSetup(side)
	a.applyCursorSetup(side)
}

// applyCursorSetup is the Cursor half of the side state: the hooks file, whether Cursor is there
// at all, and whether its helper has reported anything yet. "Installed" only ever means the file
// and the helper, never that Cursor is reporting
func (a *app) applyCursorSetup(side sideState) {
	if side.cursorState != a.cursorState {
		// A change of state is a fresh subject for the notice, and an old failure has been dealt
		// with one way or another
		a.cursorNoticeDismissed = false
		a.cursorError = ""
	}
	a.cursorState, a.cursorDetected = side.cursorState, side.cursorDetected
	a.cursorReported = side.cursorReported
}

// applyPiSetup is the pi half, which is what installing or updating touches
func (a *app) applyPiSetup(side sideState) {
	a.piHookInstalled, a.piState, a.piReported = side.piInstalled, side.piState, side.piReported
	a.piInstalledAt = side.piInstalledAt
}

// piInstallWaiting reports whether the current extension is in place but no pi session has
// loaded it yet, which is what the reload line is about
func (a *app) piInstallWaiting() bool {
	return a.piHookInstalled && a.piState == monitor.PiExtensionCurrent && !a.piReported
}

// userHome is where the window looks for the files it reports on. Tests point it at a temporary
// directory, so a click never reads or writes the real one
var userHome = os.UserHomeDir

// installCursorHooks copies the executable to Cursor's fixed helper path and merges spark's
// events into the user's own hooks file. Tests replace it, so a click doesn't run this build's
// binary or edit a real home
var installCursorHooks = func(home string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return monitor.InstallCursorHooks(home, exe)
}

// cursorNeedsSetup reports whether Cursor is installed and its hooks are missing or stale. A file
// spark can't parse is not something to rewrite, so it asks for nothing here
func (a *app) cursorNeedsSetup() bool {
	if !a.cursorDetected {
		return false
	}
	return a.cursorState == monitor.CursorHooksMissing || a.cursorState == monitor.CursorHooksOutdated
}

func (a *app) cursorInstallLabel() string {
	if a.cursorState == monitor.CursorHooksOutdated {
		return "Update for Cursor"
	}
	return "Install for Cursor"
}

// installCursor writes the helper and the hooks, then re-reads what landed. A failure leaves the
// state as it was: the button must not start saying the install is current when it isn't
func (a *app) installCursor() {
	home, err := userHome()
	if err != nil {
		return
	}
	if err := installCursorHooks(home); err != nil {
		log.Printf("[Cursor] installing the hooks failed: %v", err)
		a.cursorError = "Spark couldn't install its Cursor hooks."
		return
	}
	a.cursorError = ""
	a.applyCursorSetup(a.sideStateNow())
}

// sideStateNow re-reads the side state. Only a click does this; a view build never touches the
// disk
func (a *app) sideStateNow() sideState {
	home, err := userHome()
	if err != nil {
		return sideState{}
	}
	if a.scanner == nil {
		return readSideState(home)
	}
	return readSideStateFor(a.scanner, home)
}

// cursorSetupNotice is the line under a page's heading for Cursor: the install spark can do
// itself, and the honest statement that nothing has been heard from it yet. The banner only
// shows on an empty list, so a user with sessions in front of them needs this
func (a *app) cursorSetupNotice(c *ui.Context, p palette) {
	if !a.cursorDetected {
		return
	}
	body, label := "", ""
	switch {
	case a.cursorError != "":
		body = a.cursorError
	case a.cursorState == monitor.CursorHooksBroken:
		body = "Cursor’s hooks file can’t be read, so spark left it alone."
	case a.cursorState == monitor.CursorHooksMissing:
		body, label = "Spark can show Cursor’s desktop agents once its hooks are installed.", "Install for Cursor"
	case a.cursorState == monitor.CursorHooksOutdated:
		body, label = "The Spark hooks for Cursor are out of date.", "Update"
	case a.cursorState == monitor.CursorHooksCurrent && !a.cursorReported:
		if a.cursorNoticeDismissed {
			return
		}
		body, label = "Cursor is set up. Start a new turn to verify reporting.", "Dismiss"
	default:
		return
	}

	ui.Row(c).Background(p.tile).Radius(14).Padding(10, 12).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, body).FontSize(12).LineHeight(1.5).TextColor(p.sec).Grow(1).MinWidth(0)
		if label == "" {
			return
		}
		pillButton(c, p, label, false, 24, 11).OnClick(func() {
			if label == "Dismiss" {
				a.cursorNoticeDismissed = true
				return
			}
			a.installCursor()
		})
	})
}

// piSetupNotice is the line under a page's heading for the two things pi still needs from
// outside it: an outdated extension, which spark can write itself, and a reload, which it
// can't. The install button lives in the banner, which only an empty list shows, so without
// this a user with sessions in front of them would have no way to either
func (a *app) piSetupNotice(c *ui.Context, p palette) {
	body, label := "", ""
	switch a.piState {
	case monitor.PiExtensionOutdated:
		// Reloading would load that same file, so the only way out is writing the new one
		body, label = "The Spark extension for pi is out of date. Update it so pi reports what it is doing.", "Update"
	case monitor.PiExtensionCurrent:
		if a.piReported || a.piNoticeDismissed.Equal(a.piInstalledAt) {
			return
		}
		body, label = "pi is set up, but sessions that are already running need /reload or a restart before they report.", "Dismiss"
	default:
		return
	}

	ui.Row(c).Background(p.tile).Radius(14).Padding(10, 12).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, body).FontSize(12).LineHeight(1.5).TextColor(p.sec).Grow(1).MinWidth(0)
		pillButton(c, p, label, false, 24, 11).OnClick(func() {
			home, err := os.UserHomeDir()
			if err != nil {
				return
			}
			if label == "Update" {
				if err := monitor.InstallPiExtension(home); err != nil {
					log.Printf("[Hooks] updating the pi extension failed: %v", err)
					return
				}
				// The file is the one this build ships now; the reload it asks for next is what
				// proves a session loaded it
				a.applyPiSetup(readSideState(home))
				return
			}
			a.piNoticeDismissed = a.piInstalledAt
		})
	})
}

// hookBanner prompts for the reports spark can't get on its own: without them an approval
// prompt, or whether a pi session is running at all, can only be guessed at. Claude, Codex
// and pi install separately, so whichever is missing gets offered
func (a *app) hookBanner(c *ui.Context, p palette, l layout) {
	// In a narrow window, or when several buttons leave too little room for the prose, stack
	// the actions below it. The prose column holds MinWidth(0), so it gives way first:
	// squeezed further, it wraps to a character a line and grows tall enough to push the card
	// above it out of the window.
	width, _ := c.Size()
	missing := 0
	if !a.hookInstalled {
		missing++
	}
	if !a.codexHookInstalled {
		missing++
	}
	if !a.piHookInstalled {
		missing++
	}
	if a.cursorNeedsSetup() {
		missing++
	}
	stacked := l == layoutNarrow || (missing > 1 && width < hookBannerInlineMinWidth)
	reload := a.piNeedsReload()
	prose := func(grow bool) {
		text := ui.Column(c).MinWidth(0)
		if grow {
			text.Grow(1)
		}
		title, body := "Hear about prompts right away", "Install the hook each agent needs, so spark doesn’t have to guess."
		if missing == 0 && reload {
			// Nothing left to install: the card stays up for the one step spark can't take
			title, body = "One more step for pi", "Restart pi, or run /reload in it, so sessions that are running start reporting."
		}
		text.Children(func() {
			ui.Text(c, title).FontSize(14).LineHeight(20.0 / 14).FontWeight(510)
			ui.Text(c, body).FontSize(12).LineHeight(1.5).TextColor(p.sec)
		})
	}
	install := func() {
		if !a.hookInstalled {
			pillButton(c, p, "Install for Claude", false, 32, 14).OnClick(func() { openURL(c, hookGuideURL) })
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
		if a.cursorNeedsSetup() {
			pillButton(c, p, a.cursorInstallLabel(), false, 32, 14).OnClick(func() { a.installCursor() })
		}
		if !a.piHookInstalled {
			pillButton(c, p, "Install for pi", false, 32, 14).OnClick(func() {
				home, _ := os.UserHomeDir()
				if err := monitor.InstallPiExtension(home); err != nil {
					log.Printf("[Hooks] installing the pi extension failed: %v", err)
					return
				}
				a.applyPiSetup(readSideState(home))
			})
		}
	}

	ui.Column(c).Margin(12).Radius(16).Border(1, p.line).Padding(12, 12, 12, 16).Gap(10).Children(func() {
		if stacked {
			prose(false)
			// The buttons can still be wider than the banner between them, so let them wrap
			// rather than push out past its edge
			ui.Row(c).Gap(8).Wrap().Children(install)
			return
		}
		ui.Row(c).Gap(12).Children(func() {
			prose(true)
			install()
		})
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
	case moodUnknown:
		return "Status unavailable"
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

// ordered puts running first, then unread, then unknown, then seen; newest first within a tier
func ordered(sessions []monitor.SessionItem) []monitor.SessionItem {
	rank := func(s monitor.TaskState) int {
		switch s {
		case monitor.Running:
			return 0
		case monitor.Waiting:
			return 1
		case monitor.Unread:
			return 2
		case monitor.Unknown:
			return 3
		default:
			return 4
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

// jumpToSession takes the user back to a session: Claude and Codex have desktop deep links,
// and a pi session is a pane in Herdr
func jumpToSession(c *ui.Context, s monitor.SessionItem) {
	// Each source goes to its own app: a Cursor ID must never reach Claude's link builder
	var (
		url string
		ok  bool
	)
	switch s.Source {
	case monitor.SourcePi:
		jumpToPi(s)
		return
	case monitor.SourceCodex:
		url, ok = monitor.CodexDeeplink(s.ID)
	case monitor.SourceCursor:
		url, ok = monitor.CursorDeeplink(s.ID)
	default:
		url, ok = monitor.SessionDeeplink(s.ID)
	}
	if !ok {
		log.Printf("[Jump] session ID doesn't match the deep link format, skipping: %s", s.ID)
		return
	}
	openURL(c, url)
}

// jumpToPi focuses the pane a pi session runs in, which also marks it seen in Herdr. It
// runs off the UI thread: a socket that stopped answering must not freeze the window
func jumpToPi(s monitor.SessionItem) {
	if s.Pane == "" {
		log.Printf("[Jump] no Herdr pane for %s, skipping", s.ID)
		return
	}
	go func() {
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		client := monitor.HerdrClient{SocketPath: monitor.HerdrSocketPath(home)}
		if err := client.FocusPane(s.Pane); err != nil {
			log.Printf("[Jump] focusing %s failed: %v", s.Pane, err)
			return
		}
		// Herdr focuses the pane inside its own window; the window itself still has to come
		// forward
		activateApp("Herdr")
	}()
}

// activateApp raises an app by name. Tests replace it so a click doesn't open windows
var activateApp = func(name string) {
	if err := exec.Command("/usr/bin/open", "-a", name).Run(); err != nil {
		log.Printf("[Jump] raising %s failed: %v", name, err)
	}
}

func openURL(c *ui.Context, url string) {
	c.OpenURLThen(url, func(err error) {
		if err != nil {
			log.Printf("[Open] %v", err)
		}
	})
}
