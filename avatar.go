package main

import (
	"fmt"
	"hash/fnv"
	"math"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zackzhou-work/spark-go/internal/monitor"
)

// mood is a project character's expression, taken from the most urgent session state in
// the project
type mood int

const (
	moodNeedsYou mood = iota
	moodWorking
	moodToRead
	moodCaughtUp
	moodCount
)

// prop is the small mark outside a character's body: a bubble overhead when you're needed,
// a count at the lower right when there's something to read
type prop struct {
	glyph string
	count int
	// ring is the badge outline, matching the card the character sits on; fill and ink are
	// the badge itself, inverted to light on a dark interface. All empty uses the design's
	// colours
	ring, fill, ink ui.Color
	// sep, when set, is the ground the character stands on, stroked around its body before
	// the body is filled. Only the outer half of that stroke survives, so it cuts the
	// character out of whatever is stacked behind it. Empty draws no separator
	sep ui.Color
}

// projectLook picks the expression by priority: needs you > working > to read > caught up.
// A ! in the bubble is an approval, ? a question; the badge counts unseen sessions
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

// colour is one of the colours a character can wear: the id the saved choices store, the
// name read out to assistive technology, and the body colour
type colour struct {
	id   string
	name string
	hex  string
	body ui.Color
}

// In choice order: black, brown, red, orange, yellow, green, teal, blue, purple, pink, grey
var colours = [...]colour{
	{id: "black", name: "Black", hex: "#000000", body: ui.Hex("#000000")},
	{id: "brown", name: "Brown", hex: "#A16C39", body: ui.Hex("#A16C39")},
	{id: "red", name: "Red", hex: "#FB2C3F", body: ui.Hex("#FB2C3F")},
	{id: "orange", name: "Orange", hex: "#FE6A10", body: ui.Hex("#FE6A10")},
	{id: "yellow", name: "Yellow", hex: "#FFBF01", body: ui.Hex("#FFBF01")},
	{id: "green", name: "Green", hex: "#13C16F", body: ui.Hex("#13C16F")},
	{id: "teal", name: "Teal", hex: "#15B8A3", body: ui.Hex("#15B8A3")},
	{id: "blue", name: "Blue", hex: "#429CFB", body: ui.Hex("#429CFB")},
	{id: "purple", name: "Purple", hex: "#905AFA", body: ui.Hex("#905AFA")},
	{id: "pink", name: "Pink", hex: "#F93498", body: ui.Hex("#F93498")},
	{id: "gray", name: "Gray", hex: "#959595", body: ui.Hex("#959595")},
}

// tick is the check drawn on the chosen dot. White reads on every colour but the light
// yellow, where the dark ink shows
func (c colour) tick() ui.Color {
	if c.id == "yellow" {
		return eyeInk
	}
	return eyeWhite
}

func colourID(i int) string {
	if i < 0 || i >= len(colours) {
		return ""
	}
	return colours[i].id
}

// colourIndex is the colour a saved id names, or -1
func colourIndex(id string) int {
	for i, c := range colours {
		if c.id == id {
			return i
		}
	}
	return -1
}

// character is one character: the hue-th colour from colours paired with the shape-th shape
// from bodies
type character struct{ hue, shape int }

func (ch character) color() ui.Color { return colours[ch.hue].body }
func (ch character) body() body      { return bodies[ch.shape] }

// characterOf takes colour and shape from different digits of the seed so the two stay
// independent
func characterOf(seed uint32) character {
	return character{hue: int(seed % uint32(len(colours))), shape: int(seed / uint32(len(colours)) % uint32(len(bodies)))}
}

// castCharacters hands out characters to a set of projects. A project with a saved character
// keeps it, and two of them may share one; the rest walk in name order from the character each
// name hashes to, avoiding what the saved ones took. A project whose character is already
// taken keeps its shape and changes colour, preferring a colour nobody has yet, and only then
// takes any free pairing. Once every pairing is used up nothing is staggered and projects share
func castCharacters(groups []monitor.ProjectGroup, saved characterSettings) map[string]character {
	names := make([]string, len(groups))
	for i, g := range groups {
		names[i] = g.ProjectName
	}
	slices.Sort(names)
	cast := make(map[string]character, len(names))
	taken := map[character]bool{}
	var hueUsed [len(colours)]bool
	for _, name := range names {
		ch, ok := saved.character(name)
		if !ok {
			continue
		}
		cast[name] = ch
		taken[ch], hueUsed[ch.hue] = true, true
	}
	for _, name := range names {
		if _, ok := cast[name]; ok {
			continue
		}
		ch := characterOf(projectSeed(name))
		if taken[ch] {
			ch = freeCharacter(ch, taken, hueUsed)
		}
		cast[name] = ch
		taken[ch], hueUsed[ch.hue] = true, true
	}
	return cast
}

// freeCharacter moves a wanted character off a clash: first to an unused colour with the same
// shape, then to any free colour with that shape, then to any free pairing at all
func freeCharacter(want character, taken map[character]bool, hueUsed [len(colours)]bool) character {
	free := func(hue, shape int) bool { return !taken[character{hue, shape}] }
	nextHue := func(i int) int { return (want.hue + i) % len(colours) }
	for i := 1; i < len(colours); i++ {
		if h := nextHue(i); !hueUsed[h] && free(h, want.shape) {
			return character{h, want.shape}
		}
	}
	for i := 1; i < len(colours); i++ {
		if h := nextHue(i); free(h, want.shape) {
			return character{h, want.shape}
		}
	}
	for j := 1; j < len(bodies); j++ {
		shape := (want.shape + j) % len(bodies)
		for i := range len(colours) {
			if h := nextHue(i); free(h, shape) {
				return character{h, shape}
			}
		}
	}
	return want
}

var (
	eyeWhite  = ui.Hex("#FFFFFF")
	eyeInk    = ui.Hex("#0D0D0D")
	badgeFill = ui.Hex("#1A1A19")
)

// faceInk is how much white the lines drawn on a body need: 1 on a body so dark that ink
// would be lost in it, 0 on one where ink shows. The body's relative luminance decides, by
// the contrast it gives each of black and white
func faceInk(body ui.Color) float32 {
	lum := 0.2126*linear(body.R) + 0.7152*linear(body.G) + 0.0722*linear(body.B)
	if (lum+0.05)/0.05 >= 1.05/(lum+0.05) {
		return 0
	}
	return 1
}

// faceLine mixes the body-drawn ink with white, so a colour change carries the lines with it
func faceLine(white float32) ui.Color {
	mix := func(a, b uint8) uint8 { return uint8(float32(a) + (float32(b)-float32(a))*white) }
	return ui.RGB(mix(eyeInk.R, eyeWhite.R), mix(eyeInk.G, eyeWhite.G), mix(eyeInk.B, eyeWhite.B))
}

// linear converts one sRGB channel to light, as the standard says
func linear(c uint8) float64 {
	v := float64(c) / 255
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

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

// avatar draws a project's character, scaled from the 64px design. The bubble and badge
// spill a little past the box, so leave room around it. The cast decides which character a
// project gets; a project missing from it falls back to its own name
func (a *app) avatar(c *ui.Context, project string, m mood, p prop, size float32) ui.Element {
	return avatarOf(c, a.character(project), projectSeed(project), m, p, size).Label(project)
}

// avatarOf draws one given character; the seed only staggers when each character blinks
func avatarOf(c *ui.Context, ch character, seed uint32, m mood, p prop, size float32) ui.Element {
	box := ui.Box(c).Size(size, size)

	// Each expression has its own entry progress: on a change the old one squashes away and
	// the new one grows out of the squash, like a blink
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

// pose is what gets drawn at one instant
type pose struct {
	mood   mood
	squash float32 // vertical scale of the features, passing through 0 on an expression change
	blink  float32 // applies to the eyes only
	glance float32 // left/right pupil offset while working
	nudge  float32 // the bubble bobbing up and down
	bubble float32 // bubble entry progress
	badge  float32 // badge entry progress
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

// idle adds the small time-driven motions: open-eyed expressions blink, a working one
// glances side to side, a bubble bobs. It returns when the next repaint is due
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

// glance keeps a working character looking right at the task most of the time, with an
// occasional glance left
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

// shape maps coordinates in 64px design units, origin at the frame centre, onto the screen
// and into the path. Features scale vertically about pivot first, then rotate with the
// body, matching the design where features are children of the body
type shape struct {
	path     ui.Path
	cx, cy   float32
	u        float32
	cos, sin float32
	pivot    float32
	sy       float32
	// spin is a part's own rotation, such as a slanted eyebrow
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

// sepRatio is the separator stroke as a fraction of a character's size. The stroke is
// centred on the body and its inner half is covered by the fill, so the band that shows
// between two stacked characters is sepRatio/2 of a size
const sepRatio = 0.075

// roundRect adds a rounded rectangle centred on (x, y) with a different radius per corner,
// ordered top-left, top-right, bottom-right, bottom-left
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

// pill is a bar with rounded ends; degrees rotates it about its own centre
func (s *shape) pill(x, y, w, h, degrees float32) {
	cos, sin := turn(degrees)
	s.spin = &[4]float32{x, y, cos, sin}
	c := [2]float32{h / 2, h / 2}
	s.roundRect(x, y, w, h, [4][2]float32{c, c, c, c})
	s.spin = nil
}

// arc is a half-ellipse line: a negative ry arches up (∩), positive curves down (∪)
func (s *shape) arc(x, base, rx, ry float32) {
	s.move(x-rx, base)
	s.cube(x-rx, base+ry*kappa, x-rx*kappa, base+ry, x, base+ry)
	s.cube(x+rx*kappa, base+ry, x+rx, base+ry*kappa, x+rx, base)
}

func paintAvatar(pt *ui.Painter, r ui.Rect, ch character, p pose, pr prop) {
	u := r.W / 64
	cx, cy := r.X+r.W/2, r.Y+r.H/2

	b := ch.body()
	body := newShape(cx, cy, u, b.rotate)
	if len(b.outline) > 0 {
		body.move(b.outline[0].x, b.outline[0].y)
		for _, q := range b.outline[1:] {
			body.line(q.x, q.y)
		}
		body.path.Close()
	} else {
		var radii [4][2]float32
		for i, k := range b.radii {
			radii[i] = [2]float32{k.rx * 64, k.ry * b.height}
		}
		body.roundRect(0, 0, 64, b.height, radii)
	}
	// The separator is stroked first so the fill covers its inner half: what is left is a
	// band of sepRatio/2 around the body, which cuts it out of the character behind it.
	// Characters paint left to right, so the right-hand one is the one that cuts
	if pr.sep.A > 0 {
		pt.StrokePath(&body.path, sepRatio*r.W, pr.sep)
	}
	pt.FillPath(&body.path, ch.color())

	paintFace(pt, newShape(cx, cy+b.faceY*u, u, b.rotate), p, faceLine(faceInk(ch.color())))

	if p.bubble > 0.01 && pr.glyph != "" {
		paintBubble(pt, r, u, pr.glyph, p.bubble, p.nudge)
	}
	if p.badge > 0.01 && pr.count > 0 {
		paintBadge(pt, r, u, pr, p.badge)
	}
}

// paintFace draws the four expressions from the design; coordinates are offsets from the
// centre (32, 32) of the 64px frame. line is what is drawn straight onto the body, which a
// dark body would swallow; eye whites and the pupils inside them keep their own colours
func paintFace(pt *ui.Painter, base *shape, p pose, line ui.Color) {
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
		pt.FillPath(&brows.path, line)
		paintEyes(pt, layer(true), 11, 8, 10, 0, -3, 4, 5)
	case moodWorking:
		paintEyes(pt, layer(true), 10.5, 7.5, 9, 3.5+p.glance, 1.5, 4, 4.5)
	case moodToRead:
		eyes := layer(false)
		eyes.arc(-11.5, 0, 4.75, -6.25)
		eyes.arc(11.5, 0, 4.75, -6.25)
		pt.StrokePath(&eyes.path, 3.5*base.u, line)
		mouth := layer(false)
		mouth.arc(0, 5, 4.5, 4.5)
		pt.StrokePath(&mouth.path, 3*base.u, line)
	default:
		lids := layer(false)
		lids.pill(-11.5, -0.5, 13, 3, 0)
		lids.pill(11.5, -0.5, 13, 3, 0)
		pt.FillPath(&lids.path, line)
	}
}

// paintEyes draws a pair of eye whites with pupils; dx is each centre's distance from the
// midline and (px, py) the pupil's offset within the white
func paintEyes(pt *ui.Painter, s *shape, dx, rx, ry, px, py, prx, pry float32) {
	pupils := *s
	for _, side := range []float32{-1, 1} {
		s.ellipse(side*dx, -2, rx, ry)
		pupils.ellipse(side*dx+px, -2+py, prx, pry)
	}
	pt.FillPath(&s.path, eyeWhite)
	pt.FillPath(&pupils.path, eyeInk)
}

// paintBubble draws the white bubble at the upper right, tapering to a point at its lower
// left toward the character
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

// paintBadge draws the black count badge at the lower right, outlined in the card colour to
// separate it from the body
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

// capHeight is the stroke height of digits and ! ? as a fraction of the font size in the
// system font. Centre on the strokes rather than the line box: Painter gives a line box a
// minimum height, which pushes small sizes downward
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

// glyphCache keeps laid-out glyphs: the bubble moves every frame and doesn't need
// re-laying-out each time. Sizes are quantised to 0.25 DIP so the pop animation doesn't
// accumulate many
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
