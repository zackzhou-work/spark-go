package main

import (
	_ "embed"
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
	//go:embed icons/folder.svg
	folderSVG []byte
	//go:embed icons/jump.svg
	jumpSVG []byte
	//go:embed icons/resize-grip.svg
	resizeGripSVG []byte

	pinFilledIcon  = ui.MustParseSVG(pinFilledSVG)
	pinOutlineIcon = ui.MustParseSVG(pinOutlineSVG)
	folderIcon     = ui.MustParseSVG(folderSVG)
	jumpIcon       = ui.MustParseSVG(jumpSVG)
	resizeGripIcon = ui.MustParseSVG(resizeGripSVG)
)

var (
	colorCard       = ui.Hex("#FFFFFF")
	colorCardEdge   = ui.Hex("#E8E8E8")
	colorTrack      = ui.Hex("#EDEDED")
	colorHover      = ui.Hex("#F3F4F6")
	colorText       = ui.Hex("#1F2328")
	colorTextStrong = ui.Hex("#111827")
	colorTextMuted  = ui.Hex("#6B7280")
	colorTextFaint  = ui.Hex("#9CA3AF")
	colorRing       = ui.Hex("#D1D5DB")
	colorWaiting    = ui.Hex("#EF4444")
	colorUnread     = ui.Hex("#F59E0B")
)

const (
	filterToday = 0
	filterAll   = 1

	// 切换 Today / All 的滑动时长，滑块和翻页共用
	slideDuration = 180 * time.Millisecond
	segmentWidth  = 48
	thumbRadius   = 5

	headerHeight     = 20
	rowHeight        = 24
	groupGap         = 10
	rowGap           = 2
	iconColumnWidth  = 24
	iconCellSize     = 12
	rowRadius        = 6
	jumpFadeWidth    = 48
	trafficLightRoom = 60

	breathPeriod = 2 * time.Second
	breathTick   = 80 * time.Millisecond
)

type app struct {
	win    *mygo.Window
	groups []monitor.ProjectGroup
	filter int
	pinned bool
}

func (a *app) view(c *ui.Context) {
	c.Root().Background(ui.Transparent)
	today := monitor.TodayGroups(a.groups)
	visible := a.groups
	if a.filter == filterToday {
		visible = today
	}

	ui.Column(c).Fill().Radius(12).Border(1, colorCardEdge).Background(colorCard).Clip().Children(func() {
		a.titleBar(c)
		a.filterBar(c, monitor.CountSessions(visible))
		a.pages(c, today)
		ui.Box(c).Absolute().Bottom(2).Right(2).Size(14, 14).Center().Cursor(ui.CursorResizeNWSE).Children(func() {
			ui.Icon(c, resizeGripIcon).Size(10, 10).TextColor(colorRing)
		})
	})
}

func (a *app) titleBar(c *ui.Context) {
	ui.Row(c).Padding(12, 14, 8, 14).DragWindow().Children(func() {
		ui.Box(c).Size(trafficLightRoom, 22)
		ui.Spacer(c)
		pin := ui.Box(c).Size(22, 22).Center().Radius(6).Cursor(ui.CursorPointer).Label("Pin window")
		if pin.Hovered() {
			pin.Background(colorHover)
		}
		pin.OnClick(func() {
			a.pinned = !a.pinned
			if a.win != nil {
				a.win.SetAlwaysOnTop(a.pinned)
			}
		})
		pin.Children(func() {
			if a.pinned {
				ui.Icon(c, pinFilledIcon).Size(13, 13).TextColor(colorText)
			} else {
				ui.Icon(c, pinOutlineIcon).Size(13, 13).TextColor(colorTextFaint)
			}
		})
	})
}

func (a *app) filterBar(c *ui.Context, count int) {
	ui.Row(c).Padding(1, 14, 6, 14).Justify(ui.SpaceBetween).Children(func() {
		seg := ui.SegmentedBase(c, &a.filter, 2)
		track := seg.Track.Padding(2).Radius(thumbRadius + 2).Background(colorTrack).Label("Filter")
		// 滑块画在轨道背景上而不是做成绝对定位的子元素：绝对定位的子元素总是盖在文字上面
		x := track.AnimateWith("thumb", float32(2+a.filter*segmentWidth), slideDuration, ui.EaseInOut)
		track.Draw(func(p *ui.Painter, r ui.Rect) {
			thumb := ui.Rect{X: r.X + x, Y: r.Y + 2, W: segmentWidth, H: 20}
			p.Shadow(thumb, thumbRadius, 0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.05))
			p.Fill(thumb, colorCard, thumbRadius)
		})
		track.Children(func() {
			for i, label := range []string{"Today", "All"} {
				s := seg.Segment(i).Size(segmentWidth, 20).Center().Radius(thumbRadius).Cursor(ui.CursorPointer).Label(label)
				active := i == a.filter
				color := colorTextMuted
				if active || s.Hovered() {
					color = colorTextStrong
				}
				weight := 400
				if active {
					weight = 600
				}
				s.Children(func() {
					ui.Text(c, label).FontSize(11).FontWeight(weight).TextColor(color)
				})
			}
		})

		ui.Row(c).MinWidth(20).Height(18).Padding(0, 6).Radius(9).Background(colorTrack).Justify(ui.Center).Children(func() {
			ui.Textf(c, "%d", count).FontSize(10).FontWeight(600).TextColor(colorTextMuted)
		})
	})
}

// pages 把两页并排放在一条轨道上，切换时整条轨道滑过去；静止时只建当前这一页
func (a *app) pages(c *ui.Context, today []monitor.ProjectGroup) {
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
				a.page(c.Key("today"), today, filterToday, pageWidth)
			}
			if showAll {
				a.page(c.Key("all"), a.groups, filterAll, pageWidth)
			}
		})
	})
}

func (a *app) page(c *ui.Context, groups []monitor.ProjectGroup, filter int, width float32) {
	ui.Scroll(c).Width(width).Children(func() {
		if len(groups) == 0 {
			emptyState(c, filter)
			return
		}
		ui.Column(c).Margin(6, 10, 10, 10).Gap(groupGap).Children(func() {
			for _, g := range groups {
				ui.Column(c.Key(g.ProjectName)).Gap(rowGap).Children(func() {
					projectHeader(c, g.ProjectName)
					for _, s := range g.Sessions {
						sessionRow(c.Key(s.ID), s)
					}
				})
			}
		})
	})
}

func projectHeader(c *ui.Context, name string) {
	ui.Row(c).Height(headerHeight).Children(func() {
		iconCell(c, func() {
			ui.Icon(c, folderIcon).Size(iconCellSize, iconCellSize).TextColor(colorTextFaint)
		})
		ui.Text(c, name).SingleLine().Grow(1).MinWidth(0).Padding(0, 6, 0, 1).FontSize(11).TextColor(colorTextFaint)
	})
}

func sessionRow(c *ui.Context, s monitor.SessionItem) {
	row := ui.Row(c).Height(rowHeight).Radius(rowRadius).Cursor(ui.CursorPointer).Label(s.Title)
	hovered := row.Hovered()
	if hovered {
		row.Background(colorHover)
	}
	row.OnClick(func() { jumpToSession(c, s.ID) })
	row.Children(func() {
		iconCell(c, func() { statusDot(c, s.State) })
		ui.Text(c, s.Title).SingleLine().Grow(1).MinWidth(0).Padding(0, 6, 0, 1).FontSize(12).TextColor(colorText)
		if hovered {
			// 跳转图标连同它底下的渐变一起盖在标题尾部，让长标题在图标左侧淡出
			ui.Row(c).Absolute().Top(0).Bottom(0).Right(0).Width(jumpFadeWidth).Radius(0, rowRadius, rowRadius, 0).
				Justify(ui.End).Padding(0, 6, 0, 0).
				LinearGradient(ui.LinearGradient{From: colorHover.Alpha(0), To: colorHover, Angle: 90, End: 0.55}).
				Children(func() {
					ui.Icon(c, jumpIcon).Size(iconCellSize, iconCellSize).TextColor(colorTextFaint)
				})
		}
	})
}

// 文件夹图标和状态点共用同一个格子，保证两者中心在同一根竖线上
func iconCell(c *ui.Context, content func()) {
	ui.Row(c).Width(iconColumnWidth).Padding(0, 0, 0, 8).Children(func() {
		ui.Box(c).Size(iconCellSize, iconCellSize).Center().Children(content)
	})
}

func statusDot(c *ui.Context, state monitor.TaskState) {
	dot := ui.Box(c).Size(6, 6).Radius(3)
	switch state {
	case monitor.Running:
		// 不用 Loop：它让整个窗口按屏幕刷新率重绘，一颗 6px 的慢速渐变不值这个开销。
		// 在 Draw 里按固定间隔推进，只重画、不重建视图
		dot.Draw(func(p *ui.Painter, r ui.Rect) {
			p.Fill(r, breathColor(ui.EaseInOut(breathPhase(p.Now()))), 3)
			p.After(breathTick)
		})
	case monitor.Waiting:
		dot.Background(colorWaiting)
	case monitor.Unread:
		dot.Background(colorUnread)
	default:
		dot.Border(1, colorRing)
	}
}

func breathPhase(now time.Time) float32 {
	period := breathPeriod.Milliseconds()
	return float32(now.UnixMilli()%period) / float32(period)
}

// 灰度呼吸：#D1D5DB ↔ #374151，phase 是已经缓动过的周期位置。所有点共用一个相位，一起明暗
func breathColor(phase float32) ui.Color {
	factor := float32(math.Cos(float64(phase)*2*math.Pi))*0.5 + 0.5
	mix := func(light, dark float32) uint8 { return uint8(light*factor + dark*(1-factor)) }
	return ui.RGB(mix(209, 55), mix(213, 65), mix(219, 81))
}

func emptyState(c *ui.Context, filter int) {
	main, sub := "No active Claude sessions today", `Switch to "All" to view session history`
	if filter == filterAll {
		main, sub = "No Claude sessions found", "Run claude in terminal to start monitoring"
	}
	ui.Column(c).FillWidth().AlignItems(ui.Center).Padding(40, 0).Gap(6).Children(func() {
		ui.Text(c, main).FontSize(12).TextColor(colorTextMuted)
		ui.Text(c, sub).FontSize(10).TextColor(colorTextFaint)
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
