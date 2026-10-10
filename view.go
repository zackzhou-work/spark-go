package main

import (
	_ "embed"
	"fmt"
	"log"
	"math"
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

// palette 是单色的界面，颜色只来自角色；亮暗两套跟随系统外观
type palette struct {
	bg               ui.Color
	ink, strong, sec ui.Color
	// tileHot 是需要你处理的卡片，tile 是其余的卡片
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

	// 切换 Today / All 时页面和按钮滑块一起滑：先快后慢，停得柔和
	slideDuration = 260 * time.Millisecond

	// 红绿灯的位置是建窗口时定死的，各种宽度下的标题栏都用这个高度，才能和图钉对齐
	titleBarHeight = 48
	tileRadius     = 24
	markerSize     = 8

	// 窗口宽度分两档：不到 narrowWidth 时卡片竖排。高度不参与，矮了就滚动
	narrowWidth = 340
	// minWidth 是窗口能缩到的最窄宽度，再窄窄窗口的卡片也排不下
	minWidth = 260

	// 工作中的转圈，30 帧足够看清又不必按屏幕刷新率重画
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
	// project 不为空时显示这个项目的详情页
	project string
	pinned  bool
	// snoozed 记下点了 Later 的等待会话，值是当时的 ActivityAt；会话再有动静就重新出现
	snoozed       map[string]time.Time
	hookInstalled bool
}

func (a *app) view(c *ui.Context) {
	c.Root().Background(ui.Transparent)
	p := lightPalette
	if c.Theme().Dark {
		p = darkPalette
	}
	w, _ := c.Size()
	l := layoutFor(w)

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
	// 按字宽定死每段的宽度，滑块才能和文字对齐
	var widths [2]float32
	for i, label := range labels {
		widths[i] = float32(math.Ceil(float64(textWidth(shaped(label, size, 510))))) + 2*pad
	}

	seg := ui.SegmentedBase(c, &a.filter, 2)
	track := seg.Track.Gap(gap).Label("Filter")
	// 选中的底色是画在轨道上的一块滑块，和页面用同一条曲线一起滑过去
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

// pages 把 Today 和 All 并排放在一条轨道上，切换时整条轨道滑过去；静止时只建当前这一页
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

// fitColumns 是 CSS 的 repeat(auto-fill, minmax(min, 1fr))：放得下几列就几列，至少一列
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

// scroller 照常滚动，但把滚动条挪到窗口外面：不画出来，也不占右边那一条的点击
func scroller(c *ui.Context) ui.Element {
	return ui.Scroll(c).ScrollbarInsets(0, -40, 0, 0)
}

// pageHeading 是 Today、All 页顶上那句话；窄窗口里小一号，少占点高度
func pageHeading(c *ui.Context, p palette, l layout, s string) ui.Element {
	if l == layoutNarrow {
		// 竖排的卡片之间已经有 8 的间距，这里再补 2 凑成 10
		return ui.Text(c, s).FontSize(20).LineHeight(26.0/20).FontWeight(590).LetterSpacing(-0.4).TextColor(p.strong).Padding(0, 4, 2, 4)
	}
	return heading(c, p, s).Padding(0, 4, 14, 4)
}

// needsRow 是等你处理的项目：一整行，右边一个直接回到会话的按钮
func (a *app) needsRow(c *ui.Context, p palette, card projectCard) {
	tile := ui.Row(c).Background(p.tileHot).Radius(tileRadius).Padding(14, 12, 14, 16).Gap(14).
		Cursor(ui.CursorPointer).Label(card.group.ProjectName + ", needs you")
	tile.OnClick(func() { a.project = card.group.ProjectName })
	tile.Children(func() {
		avatar(c, card.group.ProjectName, card.mood, dressed(card.prop, p, p.tileHot), 40)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, card.group.ProjectName).SingleLine().FontSize(16).LineHeight(20.0 / 16).FontWeight(510)
			ui.Text(c, waitLine(card.waiting.WaitReason)).SingleLine().FontSize(14).LineHeight(20.0 / 14).TextColor(p.sec)
		})
		a.respondButton(c, p, card.waiting, 32, 14).Shrink(0)
	})
}

// needsStack 是窄窗口里的等你卡片：头像和名字一行，按钮占满下一行
func (a *app) needsStack(c *ui.Context, p palette, card projectCard) {
	tile := ui.Column(c).Background(p.tileHot).Radius(20).Padding(14).Gap(10).
		Cursor(ui.CursorPointer).Label(card.group.ProjectName + ", needs you")
	tile.OnClick(func() { a.project = card.group.ProjectName })
	tile.Children(func() {
		ui.Row(c).Gap(12).Children(func() {
			avatar(c, card.group.ProjectName, card.mood, dressed(card.prop, p, p.tileHot), 34)
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, card.group.ProjectName).SingleLine().FontSize(15).LineHeight(20.0 / 15).FontWeight(510)
				ui.Text(c, waitLine(card.waiting.WaitReason)).SingleLine().FontSize(13).LineHeight(18.0 / 13).TextColor(p.sec)
			})
		})
		a.respondButton(c, p, card.waiting, 32, 13).FillWidth()
	})
}

// respondButton 跳回卡住的会话：等授权是黑色的 Respond，等回答是浅色的 Answer
func (a *app) respondButton(c *ui.Context, p palette, s monitor.SessionItem, height, size float32) ui.Element {
	label, primary := "Respond", true
	if s.WaitReason == monitor.WaitQuestion {
		label, primary = "Answer", false
	}
	return pillButton(c, p, label, primary, height, size).OnClick(func() { jumpToSession(c, s.ID) })
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
			avatar(c, card.group.ProjectName, card.mood, dressed(card.prop, p, p.tile), 30)
		})
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, card.group.ProjectName).SingleLine().FontSize(14).LineHeight(20.0 / 14).FontWeight(510)
			ui.Text(c, status).SingleLine().FontSize(12).LineHeight(1.5).TextColor(p.sec)
		})
	})
}

// projectRow 是窄窗口里不用你处理的项目，几个项目共用一张卡片，一行一个
func (a *app) projectRow(c *ui.Context, p palette, card projectCard) {
	row := ui.Row(c).Height(44).Padding(0, 12).Gap(12).Radius(16).Cursor(ui.CursorPointer).
		Label(card.group.ProjectName + ", " + card.status())
	hoverFill(row, row.Hovered(), p.rowHover)
	row.OnClick(func() { a.project = card.group.ProjectName })
	row.Children(func() {
		// 每行给头像留同样宽的一列，带角标的那行名字才不会往右错开
		ui.Box(c).Width(26 + badgeRoom(projectCard{mood: moodToRead}, 26)).Children(func() {
			avatar(c, card.group.ProjectName, card.mood, dressed(card.prop, p, p.tile), 26)
		})
		ui.Text(c, card.group.ProjectName).SingleLine().Grow(1).MinWidth(0).FontSize(14).FontWeight(510)
		ui.Text(c, card.status()).Shrink(0).FontSize(12).TextColor(p.sec)
	})
}

// dressed 让角色身上的数字角标跟卡片底色和亮暗配色一致
func dressed(pr prop, p palette, ground ui.Color) prop {
	pr.ring, pr.fill, pr.ink = ground, p.primary, p.onPrimary
	return pr
}

// badgeRoom 是数字角标伸出头像右边的那一截，给它让出位置
func badgeRoom(card projectCard, size float32) float32 {
	if card.mood == moodToRead {
		return size * 9 / 64
	}
	return 0
}

// castTile 是 All 页的角色墙，每个项目一格
func (a *app) castTile(c *ui.Context, p palette, card projectCard) {
	bg := p.tile
	if card.mood == moodNeedsYou {
		bg = p.tileHot
	}
	tile := ui.Column(c).Background(bg).Radius(tileRadius).Padding(14, 10, 12, 10).Gap(8).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Label(card.group.ProjectName + ", " + card.status())
	tile.OnClick(func() { a.project = card.group.ProjectName })
	tile.Children(func() {
		avatar(c, card.group.ProjectName, card.mood, dressed(card.prop, p, bg), 36)
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
				ui.Box(c).Padding(8, 0, 0, 0).Children(func() { avatar(c, g.ProjectName, m, dressed(pr, p, p.tileHot), 72) })
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

// waitingCard 把一个卡住的会话单独拿出来：等的是什么、多久了，回到 Claude 处理或者先放一放
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
			pillButton(c, p, label, true, 36, 14).OnClick(func() { jumpToSession(c, s.ID) })
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

// card 是带一圈细线和浅阴影的白卡片
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

// rowStyle 是会话行在不同宽度下的尺寸；窄窗口放不下时间，待读只留 New
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
	row.OnClick(func() { jumpToSession(c, s.ID) })

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

// newPill 是待读会话行尾的黑色 New
func newPill(c *ui.Context, p palette, height float32) {
	ui.Row(c).Height(height).Padding(0, height*0.4).Radius(height / 2).Center().Shrink(0).Background(p.primary).Children(func() {
		ui.Text(c, "New").FontSize(height * 0.6).FontWeight(510).TextColor(p.onPrimary)
	})
}

// hoverFill 让行的悬停底色淡入淡出，而不是一下子跳出来
func hoverFill(row ui.Element, hovered bool, color ui.Color) {
	target := float32(0)
	if hovered {
		target = 1
	}
	if t := row.Animate("hover", target, rowHoverFade); t > 0 {
		row.Background(color.Alpha(t))
	}
}

// trailing 平时显示距今多久，悬停时换成跳转箭头
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

// marker 是行首的记号，只用墨色：进行中是转圈，待读是实心点，看过的是空心圈，等你的是一圈实线
func marker(c *ui.Context, p palette, state monitor.TaskState) {
	dot := ui.Box(c).Size(markerSize, markerSize)
	switch state {
	case monitor.Running:
		still := c.Preferences().ReduceMotion
		// 不用 Loop：它让整个窗口按屏幕刷新率重绘。在 Draw 里按固定间隔推进，只重画、不重建视图
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

// paintSpinner 画一圈淡淡的轨道，上面一段四分之一的弧用墨色，turn 是转过的圈数
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

// restingCast 是空状态里睡着的五个角色，高矮错开排
var restingCast = []struct {
	seed uint32
	size float32
}{{castSeed(0, 0), 38}, {castSeed(1, 1), 46}, {castSeed(4, 4), 42}, {castSeed(2, 2), 38}, {castSeed(3, 3), 36}}

func (a *app) emptyState(c *ui.Context, p palette, filter int, width float32) {
	ui.Column(c).Width(width).Children(func() {
		ui.Column(c).Grow(1).Margin(4, 12, 0, 12).Background(p.tileHot).Radius(tileRadius).Padding(24).Gap(24).
			AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.End).Children(func() {
				for i, who := range restingCast {
					avatarOf(c.Key(i), who.seed, moodCaughtUp, prop{}, who.size)
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
		if !a.hookInstalled {
			hookBanner(c, p)
		} else {
			ui.Box(c).Height(12)
		}
	})
}

// hookBanner 提示装上 hook：没有它，授权框只能靠猜
func hookBanner(c *ui.Context, p palette) {
	ui.Row(c).Margin(12).Radius(16).Border(1, p.line).Padding(12, 12, 12, 16).Gap(12).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, "Hear about prompts right away").FontSize(14).LineHeight(20.0 / 14).FontWeight(510)
			ui.Text(c, "Install the hook so spark doesn’t have to guess.").FontSize(12).LineHeight(1.5).TextColor(p.sec)
		})
		pillButton(c, p, "Install", false, 32, 14).OnClick(func() { openURL(c, hookGuideURL) })
	})
}

// projectCard 是一个项目在卡片上要显示的东西
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

// projectCards 按表情的紧急程度排：等你的、工作中的、待读的、都处理完的；同一档里保持原来的顺序
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

// unwaited 收集等你以外的会话：等你的会话已经在卡片上了
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

// ordered 把进行中的排最前，再是待读、看过的；同一档里新的在前
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

// byDay 按最后活动的日子分成 Today、Yesterday 和 Earlier，保持传进来的顺序
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

// jumpToSession 唤起 Claude Desktop 并切到指定会话；窗口的置前由应用自己处理
func jumpToSession(c *ui.Context, sessionID string) {
	url, ok := monitor.SessionDeeplink(sessionID)
	if !ok {
		log.Printf("[Jump] 不是桌面端会话 ID，跳过：%s", sessionID)
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
