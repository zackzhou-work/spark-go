package main

import (
	"fmt"
	"hash/fnv"
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zackzhou-work/spark-go/internal/monitor"
)

// mood 是项目角色的表情，取项目里最紧急的那个会话状态
type mood int

const (
	moodNeedsYou mood = iota
	moodWorking
	moodToRead
	moodCaughtUp
	moodCount
)

// prop 是角色身体外面的小物件：等你时头顶的气泡，待读时右下角的数字
type prop struct {
	glyph string
	count int
	// ring 是数字角标的描边色，跟角色所在卡片的底色一致；fill 和 ink 是角标本身，暗色界面里反过来用浅色。都留空就用设计稿的颜色
	ring, fill, ink ui.Color
}

// projectLook 按 needs you › working › to read › caught up 的优先级取表情。
// 气泡里 ! 是等授权，? 是等回答；角标数的是没看过的会话
func projectLook(g monitor.ProjectGroup) (mood, prop) {
	m, p := moodCaughtUp, prop{}
	for _, s := range g.Sessions {
		switch s.State {
		case monitor.Waiting:
			if m != moodNeedsYou {
				m, p.glyph = moodNeedsYou, waitGlyph(s.WaitReason)
			}
		case monitor.Running:
			if m != moodNeedsYou {
				m = moodWorking
			}
		case monitor.Unread:
			p.count++
			if m == moodCaughtUp {
				m = moodToRead
			}
		}
	}
	return m, p
}

func waitGlyph(r monitor.WaitReason) string {
	if r == monitor.WaitQuestion {
		return "?"
	}
	return "!"
}

// corner 是一个角的椭圆半径，按身体宽高的比例
type corner struct{ rx, ry float32 }

// body 是一种身体的形状
type body struct {
	height float32 // 64 宽的画框里身体有多高，居中放
	rotate float32 // 度，负数逆时针
	radii  [4]corner
}

func even(r float32) corner { return corner{r, r} }

// 依次是圆、水滴、豆子、方块、团子
var bodies = [5]body{
	{height: 64, radii: [4]corner{even(.5), even(.5), even(.5), even(.5)}},
	{height: 64, rotate: -15, radii: [4]corner{even(.5), even(.5), even(.5), even(14.0 / 64)}},
	{height: 64, radii: [4]corner{{.5, .6}, {.5, .6}, {.5, .4}, {.5, .4}}},
	{height: 54, radii: [4]corner{{16.0 / 64, 16.0 / 54}, {16.0 / 64, 16.0 / 54}, {16.0 / 64, 16.0 / 54}, {16.0 / 64, 16.0 / 54}}},
	{height: 64, rotate: -15, radii: [4]corner{even(.45), even(.55), even(.5), even(.5)}},
}

// 依次是黄、蓝、红、棕、灰
var hues = [5]ui.Color{ui.Hex("#FFBF01"), ui.Hex("#419CFB"), ui.Hex("#F75655"), ui.Hex("#B2774B"), ui.Hex("#8C8C8C")}

// character 是一个角色：颜色和形状各取各的，五乘五共 25 种
type character struct {
	color ui.Color
	body
}

// characterOf 用种子的不同位数分别挑颜色和形状，两者互不牵连
func characterOf(seed uint32) character {
	return character{color: hues[seed%uint32(len(hues))], body: bodies[seed/uint32(len(hues))%uint32(len(bodies))]}
}

// castSeed 是第 hue 种颜色配第 shape 种形状的种子，给预览和空状态摆指定的角色用
func castSeed(hue, shape int) uint32 {
	return uint32(hue + shape*len(hues))
}

var (
	eyeWhite  = ui.Hex("#FFFFFF")
	eyeInk    = ui.Hex("#0D0D0D")
	badgeFill = ui.Hex("#1A1A19")
)

func projectSeed(name string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(name))
	return h.Sum32()
}

const (
	faceShift  = 240 * time.Millisecond
	idleFrame  = 16 * time.Millisecond
	blinkSpan  = 180 * time.Millisecond
	glanceSpan = 260 * time.Millisecond
	nudgeEvery = 1400 * time.Millisecond
)

// avatar 画项目的角色，尺寸按 64px 的设计稿等比缩放；气泡和角标会画到方框外面一点，周围要留空
func avatar(c *ui.Context, project string, m mood, p prop, size float32) ui.Element {
	return avatarOf(c, projectSeed(project), m, p, size).Label(project)
}

func avatarOf(c *ui.Context, seed uint32, m mood, p prop, size float32) ui.Element {
	ch := characterOf(seed)
	box := ui.Box(c).Size(size, size)

	// 每种表情各有一个出场程度，换表情时旧的压扁消失、新的从扁处长出来，像眨了一下眼
	var shown [moodCount]float32
	for i := range shown {
		target := float32(0)
		if mood(i) == m {
			target = 1
		}
		shown[i] = box.AnimateWith(i, target, faceShift, ui.EaseInOut)
	}
	still := c.Preferences().ReduceMotion

	return box.Draw(func(pt *ui.Painter, r ui.Rect) {
		pose := settle(shown)
		if !still {
			pt.After(idle(&pose, seed, pt.Now()))
		}
		paintAvatar(pt, r, ch, pose, p)
	})
}

// pose 是某一刻画出来的样子
type pose struct {
	mood   mood
	squash float32 // 五官竖直方向的缩放，换表情时经过 0
	blink  float32 // 只作用在眼睛上
	glance float32 // 工作中瞳孔左右的偏移
	nudge  float32 // 气泡上下浮动
	bubble float32 // 气泡出场程度
	badge  float32 // 角标出场程度
}

func settle(shown [moodCount]float32) pose {
	top, second := 0, -1
	for i := 1; i < len(shown); i++ {
		if shown[i] > shown[top] {
			top, second = i, top
		} else if second < 0 || shown[i] > shown[second] {
			second = i
		}
	}
	return pose{
		mood:   mood(top),
		squash: shown[top] - shown[second],
		blink:  1,
		bubble: shown[moodNeedsYou],
		badge:  shown[moodToRead],
	}
}

// idle 加上随时间的小动作：睁眼的表情会眨眼，工作中左右瞟，气泡上下浮；返回下一次需要重画的时间
func idle(p *pose, seed uint32, now time.Time) time.Duration {
	next := time.Hour
	ms := now.UnixMilli() + int64(seed%7919)

	if p.mood == moodNeedsYou || p.mood == moodWorking {
		period := int64(3400 + seed%1800)
		at := time.Duration(ms%period) * time.Millisecond
		if at < blinkSpan {
			p.blink = float32(1 - math.Sin(float64(at)/float64(blinkSpan)*math.Pi))
			next = idleFrame
		} else {
			next = min(next, time.Duration(period)*time.Millisecond-at)
		}
	}
	if p.mood == moodWorking {
		var wait time.Duration
		p.glance, wait = glance(ms)
		next = min(next, wait)
	}
	if p.bubble > 0 {
		t := float64(ms%nudgeEvery.Milliseconds()) / float64(nudgeEvery.Milliseconds())
		p.nudge = ease(1 - math.Abs(2*t-1))
		next = idleFrame
	}
	return next
}

// glance 让工作中的角色大部分时间盯着右边的活，偶尔瞟一眼左边
func glance(ms int64) (float32, time.Duration) {
	const hold, look, reach = 2200 * time.Millisecond, 700 * time.Millisecond, -7
	period := hold + glanceSpan + look + glanceSpan
	at := time.Duration(ms%period.Milliseconds()) * time.Millisecond
	switch {
	case at < hold:
		return 0, hold - at
	case at < hold+glanceSpan:
		return reach * ease(float64(at-hold)/float64(glanceSpan)), idleFrame
	case at < hold+glanceSpan+look:
		return reach, hold + glanceSpan + look - at
	default:
		return reach * (1 - ease(float64(at-hold-glanceSpan-look)/float64(glanceSpan))), idleFrame
	}
}

func ease(t float64) float32 { return float32(0.5 - 0.5*math.Cos(t*math.Pi)) }

// shape 把以画框中心为原点、64px 设计稿单位的坐标换算到屏幕上，再写进路径。
// 五官先绕 pivot 竖直缩放、再随身体旋转，和设计稿里五官是身体的子元素一致
type shape struct {
	path     ui.Path
	cx, cy   float32
	u        float32
	cos, sin float32
	pivot    float32
	sy       float32
	// spin 是单个部件自己的旋转，比如斜着的眉毛
	spin *[4]float32
}

func newShape(cx, cy, u, degrees float32) *shape {
	cos, sin := turn(degrees)
	return &shape{cx: cx, cy: cy, u: u, cos: cos, sin: sin, sy: 1}
}

func turn(degrees float32) (float32, float32) {
	rad := float64(degrees) * math.Pi / 180
	return float32(math.Cos(rad)), float32(math.Sin(rad))
}

func (s *shape) at(x, y float32) (float32, float32) {
	if s.spin != nil {
		ox, oy, cos, sin := s.spin[0], s.spin[1], s.spin[2], s.spin[3]
		dx, dy := x-ox, y-oy
		x, y = ox+dx*cos-dy*sin, oy+dx*sin+dy*cos
	}
	y = s.pivot + (y-s.pivot)*s.sy
	x, y = x*s.u, y*s.u
	return s.cx + x*s.cos - y*s.sin, s.cy + x*s.sin + y*s.cos
}

func (s *shape) move(x, y float32) { s.path.MoveTo(s.at(x, y)) }
func (s *shape) line(x, y float32) { s.path.LineTo(s.at(x, y)) }
func (s *shape) cube(x1, y1, x2, y2, x, y float32) {
	ax, ay := s.at(x1, y1)
	bx, by := s.at(x2, y2)
	ex, ey := s.at(x, y)
	s.path.CubeTo(ax, ay, bx, by, ex, ey)
}

const kappa = 0.5522847498

// roundRect 加一个以 (x, y) 为中心、每个角半径不同的圆角矩形；角的顺序是左上、右上、右下、左下
func (s *shape) roundRect(x, y, w, h float32, radii [4][2]float32) {
	l, t, r, b := x-w/2, y-h/2, x+w/2, y+h/2
	tl, tr, br, bl := radii[0], radii[1], radii[2], radii[3]
	s.move(l+tl[0], t)
	s.line(r-tr[0], t)
	s.cube(r-tr[0]*(1-kappa), t, r, t+tr[1]*(1-kappa), r, t+tr[1])
	s.line(r, b-br[1])
	s.cube(r, b-br[1]*(1-kappa), r-br[0]*(1-kappa), b, r-br[0], b)
	s.line(l+bl[0], b)
	s.cube(l+bl[0]*(1-kappa), b, l, b-bl[1]*(1-kappa), l, b-bl[1])
	s.line(l, t+tl[1])
	s.cube(l, t+tl[1]*(1-kappa), l+tl[0]*(1-kappa), t, l+tl[0], t)
	s.path.Close()
}

func (s *shape) ellipse(x, y, rx, ry float32) {
	c := [2]float32{rx, ry}
	s.roundRect(x, y, rx*2, ry*2, [4][2]float32{c, c, c, c})
}

// pill 是两头圆的短横条，degrees 是它绕自己中心的旋转
func (s *shape) pill(x, y, w, h, degrees float32) {
	cos, sin := turn(degrees)
	s.spin = &[4]float32{x, y, cos, sin}
	c := [2]float32{h / 2, h / 2}
	s.roundRect(x, y, w, h, [4][2]float32{c, c, c, c})
	s.spin = nil
}

// arc 是半个椭圆的线条，ry 为负时拱起（∩），为正时下弯（∪）
func (s *shape) arc(x, base, rx, ry float32) {
	s.move(x-rx, base)
	s.cube(x-rx, base+ry*kappa, x-rx*kappa, base+ry, x, base+ry)
	s.cube(x+rx*kappa, base+ry, x+rx, base+ry*kappa, x+rx, base)
}

func paintAvatar(pt *ui.Painter, r ui.Rect, ch character, p pose, pr prop) {
	u := r.W / 64
	cx, cy := r.X+r.W/2, r.Y+r.H/2

	body := newShape(cx, cy, u, ch.rotate)
	var radii [4][2]float32
	for i, k := range ch.radii {
		radii[i] = [2]float32{k.rx * 64, k.ry * ch.height}
	}
	body.roundRect(0, 0, 64, ch.height, radii)
	pt.FillPath(&body.path, ch.color)

	paintFace(pt, newShape(cx, cy, u, ch.rotate), p)

	if p.bubble > 0.01 && pr.glyph != "" {
		paintBubble(pt, r, u, pr.glyph, p.bubble, p.nudge)
	}
	if p.badge > 0.01 && pr.count > 0 {
		paintBadge(pt, r, u, pr, p.badge)
	}
}

// paintFace 照设计稿画四种表情，坐标是 64px 画框里相对中心 (32, 32) 的偏移
func paintFace(pt *ui.Painter, base *shape, p pose) {
	layer := func(blink bool) *shape {
		s := *base
		s.pivot, s.sy = -2, p.squash
		if blink {
			s.sy *= p.blink
		}
		return &s
	}
	if p.squash < 0.02 {
		return
	}

	switch p.mood {
	case moodNeedsYou:
		brows := layer(false)
		brows.pill(-11.5, -17.5, 13, 3, -16)
		brows.pill(10.5, -17.5, 13, 3, 16)
		pt.FillPath(&brows.path, eyeInk)
		paintEyes(pt, layer(true), 11, 8, 10, 0, -3, 4, 5)
	case moodWorking:
		paintEyes(pt, layer(true), 10.5, 7.5, 9, 3.5+p.glance, 1.5, 4, 4.5)
	case moodToRead:
		eyes := layer(false)
		eyes.arc(-11.5, 0, 4.75, -6.25)
		eyes.arc(11.5, 0, 4.75, -6.25)
		pt.StrokePath(&eyes.path, 3.5*base.u, eyeInk)
		mouth := layer(false)
		mouth.arc(0, 5, 4.5, 4.5)
		pt.StrokePath(&mouth.path, 3*base.u, eyeInk)
	default:
		lids := layer(false)
		lids.pill(-11.5, -0.5, 13, 3, 0)
		lids.pill(11.5, -0.5, 13, 3, 0)
		pt.FillPath(&lids.path, eyeInk)
	}
}

// paintEyes 画一对眼白加瞳孔；dx 是两眼中心离中线的距离，(px, py) 是瞳孔在眼白里的偏移
func paintEyes(pt *ui.Painter, s *shape, dx, rx, ry, px, py, prx, pry float32) {
	pupils := *s
	for _, side := range []float32{-1, 1} {
		s.ellipse(side*dx, -2, rx, ry)
		pupils.ellipse(side*dx+px, -2+py, prx, pry)
	}
	pt.FillPath(&s.path, eyeWhite)
	pt.FillPath(&pupils.path, eyeInk)
}

// paintBubble 画头顶右上角的白色气泡，左下角收尖指向角色
func paintBubble(pt *ui.Painter, r ui.Rect, u float32, glyph string, shown, nudge float32) {
	w, h := 28*u*shown, 26*u*shown
	cx, cy := r.X+64*u, r.Y+(-1-3*nudge)*u
	box := ui.Rect{X: cx - w/2, Y: cy - h/2, W: w, H: h}

	pt.Shadow(box, h/2, 0, 4*u, 8*u, 0, eyeInk.Alpha(0.1*shown))
	s := newShape(cx, cy, 1, 0)
	round, tip := [2]float32{h / 2, h / 2}, [2]float32{4 * u * shown, 4 * u * shown}
	s.roundRect(0, 0, w, h, [4][2]float32{round, round, round, tip})
	pt.FillPath(&s.path, eyeWhite)
	pt.StrokePath(&s.path, max(u, 0.5), eyeInk.Alpha(0.08))

	centeredText(pt, glyph, 17*u*shown, 700, eyeInk, cx, cy)
}

// paintBadge 画右下角的黑色数字角标，外圈描一道卡片底色把它和身体分开
func paintBadge(pt *ui.Painter, r ui.Rect, u float32, pr prop, shown float32) {
	label := fmt.Sprint(pr.count)
	fill, ink := pr.fill, pr.ink
	if fill.A == 0 {
		fill, ink = badgeFill, eyeWhite
	}
	size := 13 * u * shown
	h := 26 * u * shown
	w := max(h, textWidth(shaped(label, size, 600))+20*u*shown)
	cx, cy := r.X+73*u-w/2, r.Y+(71-13)*u
	ring := pr.ring
	if ring.A == 0 {
		ring = eyeWhite
	}
	pt.Fill(ui.Rect{X: cx - w/2, Y: cy - h/2, W: w, H: h}, ring, h/2)
	inset := 3 * u * shown
	pt.Fill(ui.Rect{X: cx - w/2 + inset, Y: cy - h/2 + inset, W: w - 2*inset, H: h - 2*inset}, fill, h/2-inset)
	centeredText(pt, label, size, 600, ink, cx, cy)
}

// capHeight 是系统字体里数字和 ! ? 的笔画高度占字号的比例。
// 按笔画而不是按行框居中：Painter 画文字时行框有个最小高度，小字号会整体偏下
const capHeight = 0.71

func centeredText(pt *ui.Painter, s string, size float32, weight int, c ui.Color, cx, cy float32) {
	glyphs := shaped(s, size, weight)
	pt.Glyphs(glyphs, cx-textWidth(glyphs)/2, cy+capHeight*size/2, c)
}

type shapeKey struct {
	text   string
	size   float32
	weight int
}

// glyphCache 留着排好的字形：气泡每帧都在动，不必每帧重新排版。字号取到 0.25 DIP，弹出动画里也不会攒太多
var glyphCache = map[shapeKey][]ui.Glyph{}

func shaped(s string, size float32, weight int) []ui.Glyph {
	key := shapeKey{s, float32(math.Round(float64(size)*4)) / 4, weight}
	if g, ok := glyphCache[key]; ok {
		return g
	}
	if len(glyphCache) > 512 {
		clear(glyphCache)
	}
	g := ui.Shape(s, ui.Font{Size: key.size, Weight: weight})
	glyphCache[key] = g
	return g
}

func textWidth(glyphs []ui.Glyph) float32 {
	if len(glyphs) == 0 {
		return 0
	}
	last := glyphs[len(glyphs)-1]
	return last.X + last.Advance
}
