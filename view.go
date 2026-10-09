package main

import (
	_ "embed"
	"fmt"
	"log"
	"math"
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

	pinFilledIcon  = ui.MustParseSVG(pinFilledSVG)
	pinOutlineIcon = ui.MustParseSVG(pinOutlineSVG)
	resizeGripIcon = ui.MustParseSVG(resizeGripSVG)
)

// palette 是暖色单色调的一套颜色，亮暗两套跟随系统外观
type palette struct {
	bg, track, thumb ui.Color
	ink, sec         ui.Color
	border, warm     ui.Color
	ring             ui.Color
	hover, pinHover  ui.Color
	needsHover       ui.Color
	waiting, unread  ui.Color
	buttonBorder     ui.Color
}

func newPalette(bg, track, thumb, ink, sec, warm, needsHover, waiting, unread string, hover, pinHover float32) palette {
	inkColor := ui.Hex(ink)
	return palette{
		bg: ui.Hex(bg), track: ui.Hex(track), thumb: ui.Hex(thumb),
		ink: inkColor, sec: ui.Hex(sec),
		border: inkColor.Alpha(0.1), warm: ui.Hex(warm), needsHover: ui.Hex(needsHover),
		ring:  inkColor.Alpha(0.45),
		hover: inkColor.Alpha(hover), pinHover: inkColor.Alpha(pinHover),
		waiting: ui.Hex(waiting), unread: ui.Hex(unread),
		buttonBorder: inkColor.Alpha(0.2),
	}
}

var (
	lightPalette = newPalette("#FAF9F6", "#EBEAE5", "#FFFFFF", "#26251E", "#68675F", "#F3EDE6", "#EBE2D6", "#CF2D56", "#E2A110", 0.05, 0.06)
	darkPalette  = newPalette("#14120B", "#24221B", "#35332B", "#EDECEC", "#A3A19B", "#221C15", "#2C241B", "#E0446A", "#F0B429", 0.07, 0.08)
)

const (
	filterToday = 0
	filterAll   = 1

	// 切换 Today / All 的滑动时长，滑块和翻页共用
	slideDuration = 180 * time.Millisecond
	segmentWidth  = 56
	segmentHeight = 22
	thumbRadius   = 4

	monoFont         = "SF Mono, Menlo, monospace"
	titleBarHeight   = 44
	headerHeight     = 22
	rowHeight        = 26
	rowRadius        = 4
	groupGap         = 12
	rowGap           = 1
	dotColumnWidth   = 8
	dotSize          = 7
	trafficLightRoom = 60

	breathPeriod = 2 * time.Second
	breathTick   = 80 * time.Millisecond
	breathLow    = 0.22

	rowHoverFade = 140 * time.Millisecond
)

type app struct {
	win    *mygo.Window
	groups []monitor.ProjectGroup
	filter int
	pinned bool
}

func (a *app) view(c *ui.Context) {
	c.Root().Background(ui.Transparent)
	p := lightPalette
	if c.Theme().Dark {
		p = darkPalette
	}
	today := monitor.TodayGroups(a.groups)
	visible := a.groups
	if a.filter == filterToday {
		visible = today
	}

	ui.Column(c).Fill().Radius(12).Border(1, p.border).Background(p.bg).TextColor(p.ink).Clip().Children(func() {
		a.titleBar(c, p)
		a.filterBar(c, p, monitor.CountSessions(visible))
		a.pages(c, p, today)
		ui.Box(c).Absolute().Bottom(3).Right(3).Size(14, 14).Center().Cursor(ui.CursorResizeNWSE).Children(func() {
			ui.Icon(c, resizeGripIcon).Size(10, 10).TextColor(p.sec.Alpha(0.5))
		})
	})
}

func (a *app) titleBar(c *ui.Context, p palette) {
	ui.Row(c).Height(titleBarHeight).Padding(0, 10, 0, 14).DragWindow().Children(func() {
		ui.Box(c).Size(trafficLightRoom, 22)
		ui.Spacer(c)
		pin := ui.Box(c).Size(28, 28).Center().Radius(4).Cursor(ui.CursorPointer).Label("Keep window on top")
		if pin.Hovered() {
			pin.Background(p.pinHover)
		}
		pin.OnClick(func() {
			a.pinned = !a.pinned
			if a.win != nil {
				a.win.SetAlwaysOnTop(a.pinned)
			}
		})
		pin.Children(func() {
			if a.pinned {
				ui.Icon(c, pinFilledIcon).Size(14, 14).TextColor(p.ink)
			} else {
				ui.Icon(c, pinOutlineIcon).Size(14, 14).TextColor(p.sec)
			}
		})
	})
}

func (a *app) filterBar(c *ui.Context, p palette, count int) {
	ui.Row(c).Padding(0, 14, 10, 14).Justify(ui.SpaceBetween).Children(func() {
		seg := ui.SegmentedBase(c, &a.filter, 2)
		track := seg.Track.Padding(2).Radius(6).Background(p.track).Label("Filter")
		// 滑块画在轨道背景上而不是做成绝对定位的子元素：绝对定位的子元素总是盖在文字上面
		x := track.AnimateWith("thumb", float32(2+a.filter*(segmentWidth+2)), slideDuration, ui.EaseInOut)
		track.Draw(func(pt *ui.Painter, r ui.Rect) {
			thumb := ui.Rect{X: r.X + x, Y: r.Y + 2, W: segmentWidth, H: segmentHeight}
			pt.Fill(thumb, p.thumb, thumbRadius)
			pt.Stroke(thumb, p.border, thumbRadius, 1)
		})
		track.Gap(2).Children(func() {
			for i, label := range []string{"Today", "All"} {
				s := seg.Segment(i).Size(segmentWidth, segmentHeight).Center().Radius(thumbRadius).Cursor(ui.CursorPointer).Label(label)
				active := i == a.filter
				color := p.sec
				if active || s.Hovered() {
					color = p.ink
				}
				weight := 400
				if active {
					weight = 500
				}
				s.Children(func() {
					ui.Text(c, label).FontSize(11).FontWeight(weight).TextColor(color)
				})
			}
		})

		ui.Text(c, sessionCount(count)).FontSize(11).FontFeatures("tnum").TextColor(p.sec)
	})
}

func sessionCount(n int) string {
	if n == 1 {
		return "1 session"
	}
	return fmt.Sprintf("%d sessions", n)
}

// pages 把两页并排放在一条轨道上，切换时整条轨道滑过去；静止时只建当前这一页
func (a *app) pages(c *ui.Context, p palette, today []monitor.ProjectGroup) {
	w, _ := c.Size()
	pageWidth := max(w-2, 200)

	ui.Box(c).Grow(1).ClipX().Children(func() {
		track := ui.Row(c).Absolute().Top(0).Bottom(0).AlignItems(ui.Stretch)
		progress := track.AnimateWith("slide", float32(a.filter), slideDuration, ui.EaseInOut)
		sliding := progress != 0 && progress != 1

		showToday := sliding || a.filter == filterToday
		showAll := sliding || a.filter == filterAll
		offset := -progress * pageWidth
		if !sliding {
			offset = 0
		}
		track.Left(offset).Children(func() {
			if showToday {
				a.page(c.Key("today"), p, today, filterToday, pageWidth)
			}
			if showAll {
				a.page(c.Key("all"), p, a.groups, filterAll, pageWidth)
			}
		})
	})
}

func (a *app) page(c *ui.Context, p palette, groups []monitor.ProjectGroup, filter int, width float32) {
	ui.Scroll(c).Width(width).Children(func() {
		if len(groups) == 0 {
			a.emptyState(c, p, filter)
			return
		}
		now := time.Now()
		waiting, rest := monitor.SplitWaiting(groups)
		ui.Column(c).Padding(0, 8, 10, 8).Gap(groupGap).Children(func() {
			if len(waiting) > 0 {
				needsYou(c.Key("needs-you"), p, waiting, now)
			}
			for _, g := range rest {
				ui.Column(c.Key(g.ProjectName)).Gap(rowGap).Children(func() {
					ui.Row(c).Height(headerHeight).Padding(0, 10).Children(func() {
						ui.Text(c, g.ProjectName).SingleLine().MinWidth(0).Font(monoFont).FontSize(10.5).TextColor(p.sec)
					})
					for _, s := range g.Sessions {
						sessionRow(c.Key(s.ID), p, s, now)
					}
				})
			}
		})
	})
}

// needsYou 把卡在授权或提问上的会话单独放在顶上，带上项目和原因
func needsYou(c *ui.Context, p palette, sessions []monitor.PendingSession, now time.Time) {
	ui.Column(c).Background(p.warm).Radius(6).Padding(6, 4, 4, 4).Label("Needs you").Children(func() {
		ui.Row(c).Height(20).Padding(0, 6).Justify(ui.SpaceBetween).Children(func() {
			ui.Text(c, "Needs you").FontSize(11).FontWeight(500)
			ui.Textf(c, "%d", len(sessions)).FontSize(11).FontFeatures("tnum").TextColor(p.sec)
		})
		for _, s := range sessions {
			row := ui.Row(c.Key(s.ID)).AlignItems(ui.Start).Gap(8).Padding(5, 6, 6, 6).Radius(rowRadius).
				Cursor(ui.CursorPointer).Label(s.Title + ", needs you: " + s.WaitReason.String())
			hovered := row.Hovered()
			hoverFill(row, hovered, p.needsHover)
			row.OnClick(func() { jumpToSession(c, s.ID) })
			row.Children(func() {
				ui.Row(c).Size(dotColumnWidth, 17).Center().Children(func() {
					ui.Box(c).Size(dotSize, dotSize).Radius(dotSize / 2).Background(p.waiting)
				})
				ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
					ui.Text(c, s.Title).SingleLine().FontSize(12).LineHeight(17.0 / 12).FontWeight(500)
					ui.Row(c).MinWidth(0).Children(func() {
						ui.Text(c, s.ProjectName).SingleLine().Shrink(0).Font(monoFont).FontSize(10.5).TextColor(p.sec)
						ui.Text(c, " · "+s.WaitReason.String()).SingleLine().MinWidth(0).FontSize(11).TextColor(p.sec)
					})
				})
				trailing(c, p, hovered, relativeTime(s.ActivityAt, now))
			})
		}
	})
}

func sessionRow(c *ui.Context, p palette, s monitor.SessionItem, now time.Time) {
	row := ui.Row(c).Height(rowHeight).Gap(8).Padding(0, 10).Radius(rowRadius).Cursor(ui.CursorPointer).
		Label(s.Title + ", " + s.State.String())
	hovered := row.Hovered()
	hoverFill(row, hovered, p.hover)
	row.OnClick(func() { jumpToSession(c, s.ID) })

	color := p.ink
	if s.State == monitor.Completed {
		color = p.sec
	}
	when := relativeTime(s.ActivityAt, now)
	if s.State == monitor.Running {
		when = "now"
	}
	row.Children(func() {
		ui.Row(c).Width(dotColumnWidth).Center().Children(func() { statusDot(c, p, s.State) })
		ui.Text(c, s.Title).SingleLine().Grow(1).MinWidth(0).FontSize(12).TextColor(color)
		trailing(c, p, hovered, when)
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
func trailing(c *ui.Context, p palette, hovered bool, when string) {
	label := when
	if hovered {
		label = "→"
	}
	ui.Text(c, label).Shrink(0).FontSize(11).LineHeight(17.0 / 11).FontFeatures("tnum").TextColor(p.sec)
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

func statusDot(c *ui.Context, p palette, state monitor.TaskState) {
	dot := ui.Box(c).Size(dotSize, dotSize).Radius(dotSize / 2)
	switch state {
	case monitor.Running:
		// 不用 Loop：它让整个窗口按屏幕刷新率重绘，一颗 7px 的慢速渐变不值这个开销。
		// 在 Draw 里按固定间隔推进，只重画、不重建视图
		dot.Draw(func(pt *ui.Painter, r ui.Rect) {
			pt.Fill(r, p.ink.Alpha(breathAlpha(ui.EaseInOut(breathPhase(pt.Now())))), dotSize/2)
			pt.After(breathTick)
		})
	case monitor.Waiting:
		dot.Background(p.waiting)
	case monitor.Unread:
		dot.Background(p.unread)
	default:
		dot.Border(1.5, p.ring)
	}
}

func breathPhase(now time.Time) float32 {
	period := breathPeriod.Milliseconds()
	return float32(now.UnixMilli()%period) / float32(period)
}

// 不透明度在 1 和 breathLow 之间呼吸，phase 是已经缓动过的周期位置。所有点共用一个相位，一起明暗
func breathAlpha(phase float32) float32 {
	factor := float32(math.Cos(float64(phase)*2*math.Pi))*0.5 + 0.5
	return breathLow + (1-breathLow)*factor
}

func (a *app) emptyState(c *ui.Context, p palette, filter int) {
	main := "Nothing ran today."
	if filter == filterAll {
		main = "No sessions yet."
	}
	ui.Column(c).FillWidth().AlignItems(ui.Center).Padding(48, 24, 24, 24).Gap(6).Children(func() {
		ui.Box(c).Size(9, 9).Radius(4.5).Border(1.5, p.ring).Margin(0, 0, 6, 0)
		ui.Text(c, main).FontSize(13).TextAlign(ui.Center)
		ui.Text(c, "Sessions you start in the Code tab show up here.").FontSize(11).TextAlign(ui.Center).TextColor(p.sec)
		if filter == filterToday {
			btn := ui.Row(c).Height(26).Padding(0, 10).Margin(8, 0, 0, 0).Radius(8).Border(1, p.buttonBorder).
				Cursor(ui.CursorPointer).Label("Show all sessions")
			if btn.Hovered() {
				btn.Background(p.hover)
			}
			btn.OnClick(func() { a.filter = filterAll })
			btn.Children(func() {
				ui.Text(c, "Show all sessions →").FontSize(11)
			})
		}
	})
}

// jumpToSession 唤起 Claude Desktop 并切到指定会话；窗口的置前由应用自己处理
func jumpToSession(c *ui.Context, sessionID string) {
	url, ok := monitor.SessionDeeplink(sessionID)
	if !ok {
		log.Printf("[Jump] 不是桌面端会话 ID，跳过：%s", sessionID)
		return
	}
	c.OpenURLThen(url, func(err error) {
		if err != nil {
			log.Printf("[Jump] %v", err)
		}
	})
}
