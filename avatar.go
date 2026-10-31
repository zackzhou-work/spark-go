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
	// moodUnknown is a project whose sessions spark can't read: it must not wear the caught-up
	// face, because nothing says those sessions are done
	moodUnknown
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
			if m == moodCaughtUp || m == moodUnknown {
				m = moodToRead
			}
		case monitor.Unknown:
			if m == moodCaughtUp {
				m = moodUnknown
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
	driftFrame = 33 * time.Millisecond
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
			idle(&pose, seed, pt.Now())
			// a pointer passing close turns the face toward it, easing out of the drift
			x, y, _ := box.PointerPosition()
			var share float32
			pose.gaze, share = pose.gaze.toward(x-r.W/2, y-r.H/2, size)
			if share > 0 {
				// answering a pointer is fast motion, and a timer lands between refreshes and
				// waits for the next one: only the display's own frames are even enough
				pt.AnimationFrame()
			} else {
				// the drift is slow enough that a timer's uneven landing can't be seen, and it
				// leaves the window most of the time it would otherwise spend repainting
				pt.After(frameFor(pose.mood))
			}
		}
		paintAvatar(pt, r, ch, pose, p)
	})
}

// pose is what gets drawn at one instant
type pose struct {
	mood   mood
	squash float32 // vertical scale of the features, passing through 0 on an expression change
	blink  float32 // applies to the eyes only
	gaze   gaze    // where the face is turned, and so where its eyes land
	breath float32 // how far the body has breathed out, as a share of its size
	lift   float32 // how far the body has risen, in design units
	nudge  float32 // the bubble bobbing up and down
	bubble float32 // bubble entry progress
	badge  float32 // badge entry progress
}

// gaze is how a character's face is turned, in degrees: yaw looks right, pitch looks down,
// roll leans it counter-clockwise on screen
type gaze struct{ yaw, pitch, roll float32 }

// faceRadius is the sphere the 64px design's eyes sit on. Projecting them onto it is what
// makes a small turn read as a turn: the eyes travel along the surface, and the one nearer
// the edge narrows
const (
	faceRadius = 32
	eyeDrop    = -2 // eyes sit a little above the midline
)

// eyeOnFace is one eye as it lands on the 64px design plane, in design units and degrees
type eyeOnFace struct {
	x, y float32
	rx   float32
	ry   float32
	tilt float32
}

type vec3 struct{ x, y, z float32 }

// spin3 rotates v by degrees about axis
func spin3(v, axis vec3, degrees float32) vec3 {
	rad := float64(degrees) * math.Pi / 180
	c, s := float32(math.Cos(rad)), float32(math.Sin(rad))
	dot := v.x*axis.x + v.y*axis.y + v.z*axis.z
	cross := vec3{
		axis.y*v.z - axis.z*v.y,
		axis.z*v.x - axis.x*v.z,
		axis.x*v.y - axis.y*v.x,
	}
	return vec3{
		v.x*c + cross.x*s + axis.x*dot*(1-c),
		v.y*c + cross.y*s + axis.y*dot*(1-c),
		v.z*c + cross.z*s + axis.z*dot*(1-c),
	}
}

// eyes places a pair of eyes on the face: dx is their resting distance from the midline, rx
// and ry their resting half sizes. The turn moves them along the sphere and narrows the one
// that has gone round the side
func (g gaze) eyes(dx, rx, ry float32) [2]eyeOnFace {
	forward, right, up := vec3{0, 0, 1}, vec3{1, 0, 0}, vec3{0, 1, 0}
	forward, right = spin3(forward, up, g.yaw), spin3(right, up, g.yaw)
	up, forward = spin3(up, right, g.pitch), spin3(forward, right, g.pitch)

	// the body bends as it turns, so the eyes ride the same bend: without it a turned body
	// pulls in from under the eye that is going round with it
	_, turn := g.bodyTurn()
	split := float32(math.Asin(float64(dx/faceRadius))) * 180 / math.Pi
	// an eye is drawn at the size it is given when the face looks straight ahead, so the
	// rest face is the design's and only a turn narrows it
	rest := float32(math.Cos(float64(split)*math.Pi/180)) * (0.85 + 0.15*float32(math.Cos(float64(split)*math.Pi/180)))
	var out [2]eyeOnFace
	for i, side := range [2]float32{-1, 1} {
		on := spin3(forward, up, side*split)
		across := spin3(right, up, side*split)
		narrow := float32(math.Hypot(float64(across.x), float64(across.y)))
		flat := float32(math.Hypot(float64(up.x), float64(up.y)))
		deep := 0.85 + 0.15*max(0, on.z)
		width, height := rx*narrow*deep/rest, ry*flat*deep/rest
		// the eye is drawn flat on the surface, so near the rim it would poke past the
		// outline: hold it inside the bent body, which is invisible and keeps it on it
		x, y := on.x*faceRadius, -on.y*faceRadius+eyeDrop
		x -= turn * x * x / faceRadius
		right := faceRadius * (1 - turn)
		left := -faceRadius * (1 + turn)
		x = min(max(x, left+width), right-width)
		y = clampTo(y, faceRadius-height)
		out[i] = eyeOnFace{
			x:    x,
			y:    y,
			rx:   width,
			ry:   height,
			tilt: float32(math.Atan2(float64(-across.y), float64(across.x))) * 180 / math.Pi,
		}
	}
	return out
}

// toward turns the gaze onto a pointer (dx, dy) from the character's centre, taking the drift
// over entirely by the time the pointer is close: a character that notices you. It returns how
// much of the drift the pointer took over, 0 when it is out of reach
func (g gaze) toward(dx, dy, size float32) (gaze, float32) {
	const reach = 3 // how many body widths away the pointer starts to matter
	far := size * reach
	dist := float32(math.Hypot(float64(dx), float64(dy)))
	share := 1 - dist/far
	if share <= 0 {
		return g, 0
	}
	share = share * share * (3 - 2*share) // smoothstep, so it doesn't snap
	// a dead zone right around the character: a pointer that close means "look at me", and it
	// keeps a hand resting on the character from flicking its eyes back and forth
	dx, dy = soft(dx, size*0.4), soft(dy, size*0.4)
	aim := gaze{
		yaw:   clampTo(float32(math.Atan2(float64(dx), float64(size*0.9)))*180/math.Pi, 30),
		pitch: clampTo(float32(math.Atan2(float64(dy), float64(size*0.9)))*180/math.Pi, 22),
	}
	return gaze{
		yaw:   g.yaw + (aim.yaw-g.yaw)*share,
		pitch: g.pitch + (aim.pitch-g.pitch)*share,
		roll:  g.roll,
	}, share
}

// faceOffset is how far a feature on the face has moved from where it rests, in design units:
// what the brows, the mouth and the closed lids follow
func faceOffset(g gaze) (dx, dy float32) {
	eyes := g.eyes(11.5, 0, 0)
	return (eyes[0].x + eyes[1].x) / 2, (eyes[0].y+eyes[1].y)/2 - eyeDrop
}

// pupilLook is how far the pupils lean toward where the face is looking, as a share of the
// eye. The eyes travel with the head; the pupils lead it, which is what reads as aiming
func (g gaze) pupilLook() (x, y float32) {
	return clampTo(g.yaw*0.014, 0.42), clampTo(g.pitch*0.016, 0.38)
}

// soft blanks out an offset smaller than dead, so a value resting near the middle stays put
func soft(v, dead float32) float32 {
	switch {
	case v > dead:
		return v - dead
	case v < -dead:
		return v + dead
	}
	return 0
}

// clampTo keeps v inside ±limit
func clampTo(v, limit float32) float32 {
	if v > limit {
		return limit
	}
	if v < -limit {
		return -limit
	}
	return v
}

// lift is how far the body sits lower while the face looks down, in design units
func (g gaze) lift() float32 {
	return float32(math.Sin(float64(g.pitch)*math.Pi/180)) * 1.8
}

// bodyTurn is how the body itself answers a turn: the side going round compresses while the
// near side keeps its width, which is what a projection does. A circle only gets lopsided
// slightly, a square, a star or a heart clearly turns
func (g gaze) bodyTurn() (squeeze, turn float32) {
	swing := float32(math.Sin(float64(g.yaw) * math.Pi / 180))
	return 1 - 0.1*float32(math.Abs(float64(swing))), 0.55 * swing
}

// lean is the body's own turn in degrees, in the drawing's clockwise sense: the face follows
// the body, so a roll tilts the whole character rather than only its eyes
func (g gaze) lean() float32 { return -g.roll }

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

// frameFor is how often an expression's drift needs drawing. The two that have nothing to
// report move so little that a third of the frames would be the same, and a window with a
// few dozen characters in it should not pay to draw them
func frameFor(m mood) time.Duration {
	if m == moodCaughtUp || m == moodUnknown {
		return 3 * driftFrame
	}
	return driftFrame
}

// idle adds the small time-driven motions: open-eyed expressions blink, the bubble bobs, and
// drift decides where the character is looking and how it holds itself
func idle(p *pose, seed uint32, now time.Time) {
	ms := now.UnixMilli() + int64(seed%7919)
	p.blink = 1

	if p.mood == moodNeedsYou || p.mood == moodWorking {
		period := int64(3400 + seed%1800)
		at := time.Duration(ms%period) * time.Millisecond
		if at < blinkSpan {
			p.blink = float32(1 - math.Sin(float64(at)/float64(blinkSpan)*math.Pi))
		}
	}

	p.gaze, p.breath, p.lift = drift(p.mood, seed, float64(ms)/1000)

	if p.bubble > 0 {
		t := float64(ms%nudgeEvery.Milliseconds()) / float64(nudgeEvery.Milliseconds())
		p.nudge = ease(1 - math.Abs(2*t-1))
	}
}

// drift is where a character is looking, how far it has breathed out and how far it has risen.
// Each expression moves on its own tempo and leans on its own axis, so a working character
// scans, one that needs you bobs at you, and one that is caught up dozes; every period is a
// different length, so the same small motion never comes round again
func drift(m mood, seed uint32, seconds float64) (gaze, float32, float32) {
	phase := float64(seed%360) * math.Pi / 180
	wobble := func(period, offset float64) float64 {
		return math.Sin(2*math.Pi*seconds/period + phase + offset)
	}

	// how far each expression looks, along which axis, and how fast it does it
	var yaw, pitch, roll, tempo float64
	switch m {
	case moodWorking: // reading down a task: wide, mostly sideways, unhurried
		yaw, pitch, roll, tempo = 9.0, 2.0, 3.0, 1.0
	case moodNeedsYou: // trying to catch your eye: quicker, more vertical, with a bob
		yaw, pitch, roll, tempo = 7.0, 6.0, 3.5, 0.62
	case moodToRead: // waiting to be read: slow, and it tilts its head
		yaw, pitch, roll, tempo = 5.0, 3.0, 6.0, 1.5
	case moodUnknown: // nothing to report, and it doesn't pretend otherwise
		yaw, pitch, roll, tempo = 1.2, 0.8, 0.5, 1.8
	default: // caught up: dozing
		yaw, pitch, roll, tempo = 3.5, 2.0, 1.5, 1.9
	}

	// every character rests a little off centre, so a row of them isn't a row of stares
	rest := float64(seed%7)*0.9 - 2.7
	g := gaze{
		yaw:   float32(rest + yaw*(0.68*wobble(6.3*tempo, 0)+0.32*wobble(2.9*tempo, 1.1))),
		pitch: float32(-1.2 + pitch*(0.7*wobble(5.1*tempo, 2.1)+0.3*wobble(3.1*tempo, 0.6))),
		roll:  float32(roll * wobble(8.7*tempo, 0.9)),
	}
	var lift float64
	if m == moodWorking {
		// the slow sweep is what reads as reading down the task
		g.yaw += float32(4.5 * wobble(5.7, 2.4))
	}
	if m == moodNeedsYou {
		// a small bob, the way something waves at you
		lift = 1.1 * math.Sin(2*math.Pi*seconds/2.2+phase)
	}
	return g, float32(0.01 * math.Sin(2*math.Pi*seconds/3.4)), float32(lift)
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
	sx, sy   float32
	// turn bends the shape across its width, so the side that has gone round compresses
	// while the side facing you keeps its size: the flat stand-in for a projection
	turn float32
	// spin is a part's own rotation, such as a slanted eyebrow
	spin *[4]float32
}

func newShape(cx, cy, u, degrees float32) *shape {
	cos, sin := turn(degrees)
	return &shape{cx: cx, cy: cy, u: u, cos: cos, sin: sin, sx: 1, sy: 1}
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
	if s.turn != 0 {
		x -= s.turn * x * x / faceRadius
	}
	x, y = x*s.sx*s.u, y*s.u
	return s.cx + x*s.cos - y*s.sin, s.cy + x*s.sin + y*s.cos
}

// tilted copies the shape so a part is turned about its own centre
func (s *shape) tilted(x, y, degrees float32) *shape {
	out := *s
	cos, sin := turn(degrees)
	out.spin = &[4]float32{x, y, cos, sin}
	return &out
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

// arcBand adds a filled line along an arc, with round ends, keeping the width of a stroke.
// It is drawn by hand because StrokePath outlines an open curve by running the polyline back
// over itself, and on a line this small the filler's winding at the round ends was a coin
// toss: the ends came and went as the drawing moved by less than a pixel
func (s *shape) arcBand(x, base, rx, ry, w float32) {
	r := w / 2
	side := float32(1) // the way the arc bows: down for a smile, up for a closed eye
	if ry < 0 {
		side = -1
	}
	// the edges keep the line's width all the way along, so they are the arc with the radius
	// grown and shrunk by half of it, not the arc moved down: moving it would squeeze the line
	// to nothing where the arc stands on end
	outer, inner := rx+r, rx-r
	high, low := ry+side*r, ry-side*r
	turn := -side * r // which way the round ends go round

	s.move(x-outer, base)
	s.cube(x-outer, base+high*kappa, x-outer*kappa, base+high, x, base+high)
	s.cube(x+outer*kappa, base+high, x+outer, base+high*kappa, x+outer, base)
	s.cube(x+outer, base+turn*kappa, x+rx+r*kappa, base+turn, x+rx, base+turn)
	s.cube(x+rx-r*kappa, base+turn, x+inner, base+turn*kappa, x+inner, base)
	s.cube(x+inner, base+low*kappa, x+inner*kappa, base+low, x, base+low)
	s.cube(x-inner*kappa, base+low, x-inner, base+low*kappa, x-inner, base)
	s.cube(x-inner, base+turn*kappa, x-rx-r*kappa, base+turn, x-rx, base+turn)
	s.cube(x-rx+r*kappa, base+turn, x-outer, base+turn*kappa, x-outer, base)
	s.path.Close()
}

func paintAvatar(pt *ui.Painter, r ui.Rect, ch character, p pose, pr prop) {
	u := r.W / 64
	cx, cy := r.X+r.W/2, r.Y+r.H/2+(p.gaze.lift()-p.lift)*u

	b := ch.body()
	body := newShape(cx, cy, u, b.rotate+p.gaze.lean())
	squeeze, turn := p.gaze.bodyTurn()
	// Breathing keeps the body's volume: it widens by what it flattens
	body.pivot, body.sx, body.sy = 0, (1+p.breath)*squeeze, 1-p.breath
	body.turn = turn
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

	face := newShape(cx, cy+b.faceY*u, u, b.rotate+p.gaze.lean())
	face.sx = squeeze
	paintFace(pt, face, p, faceLine(faceInk(ch.color())))

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
		eyes := p.gaze.eyes(11, 8, 10)
		// the brows sit on the face too, so they travel with the eyes
		dx, dy := faceOffset(p.gaze)
		brows := layer(false)
		brows.pill(-11.5+dx, -17.5+dy, 13, 3, -16)
		brows.pill(10.5+dx, -17.5+dy, 13, 3, 16)
		pt.FillPath(&brows.path, line)
		lx, ly := p.gaze.pupilLook()
		paintEyes(pt, layer(true), eyes, lx, -0.2+ly, 0.5, 0.5)
	case moodWorking:
		// The pupil sits forward, against the eye's far edge, as the design has it: a working
		// character stares at its task rather than at you, and the gaze turns the eyes instead
		paintEyes(pt, layer(true), p.gaze.eyes(10.5, 7.5, 9), 0.47, 0.17, 0.53, 0.5)
	case moodToRead:
		// closed eyes still sit on the face, so they travel and tilt with it
		dx, dy := faceOffset(p.gaze)
		for _, e := range p.gaze.eyes(11.5, 4.75, 6.25) {
			eye := layer(false).tilted(e.x, e.y, e.tilt)
			eye.arcBand(e.x, e.y, e.rx, -e.ry, 3.5)
			pt.FillPath(&eye.path, line)
		}
		mouth := layer(false)
		mouth.arcBand(dx, 5+dy, 4.5, 4.5, 3)
		pt.FillPath(&mouth.path, line)
	default:
		// A level stare and a flat mouth, for both what spark can't read and a project with
		// nothing left to do: it doesn't pretend otherwise either way
		dx, dy := faceOffset(p.gaze)
		for _, e := range p.gaze.eyes(11.5, 5.5, 1.5) {
			lid := layer(false).tilted(e.x, e.y, e.tilt)
			lid.pill(e.x, e.y+2.5, 11, 3, 0)
			pt.FillPath(&lid.path, line)
		}
		mouth := layer(false)
		mouth.pill(dx, 6+dy, 9, 2.5, 0)
		pt.FillPath(&mouth.path, line)
	}
}

// paintEyes draws a pair of eye whites with pupils, each one placed, narrowed and tilted by
// the sphere the face sits on; ox and oy are the pupil's offset as a share of the eye, prx
// and pry its size as a share of the eye's
func paintEyes(pt *ui.Painter, s *shape, eyes [2]eyeOnFace, ox, oy, prx, pry float32) {
	for _, e := range eyes {
		white := s.tilted(e.x, e.y, e.tilt)
		white.ellipse(e.x, e.y, e.rx, e.ry)
		pt.FillPath(&white.path, eyeWhite)

		px, py := e.x+e.rx*ox, e.y+e.ry*oy
		pupil := s.tilted(px, py, e.tilt)
		pupil.ellipse(px, py, e.rx*prx, e.ry*pry)
		pt.FillPath(&pupil.path, eyeInk)
	}
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
