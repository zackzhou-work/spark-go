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
	darkGround := c.Theme().Dark

	return box.Draw(func(pt *ui.Painter, r ui.Rect) {
		pose := settle(shown)
		pose.darkGround = darkGround
		if !still {
			idle(&pose, seed, pt.Now())
			// a pointer passing close turns the face toward it, easing out of the drift
			x, y, _ := box.PointerPosition()
			share := pose.applyPointer(x-r.W/2, y-r.H/2, size)
			if share > 0 || pose.fastMotion || pose.blink < 0.99 {
				// fast saccades, blinks, spins, and pointer tracking need display-synced frames;
				// ambient dwell can sleep on the drift timer
				pt.AnimationFrame()
			} else {
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
	gaze   gaze    // where the face (or body, when decoupled) is turned
	breath float32 // how far the body has breathed out, as a share of its size
	lift   float32 // how far the body has risen, in design units
	nudge  float32 // the bubble bobbing up and down
	bubble float32 // bubble entry progress
	badge  float32 // badge entry progress
	// working mood decouples eye gaze and pupil lead from body lag, plus a periodic 360° orbit spin
	decoupled    bool
	eyeGaze      gaze
	pupilX       float32
	pupilY       float32
	eyeScaleY    float32
	fastMotion   bool
	orbitClip    bool
	spinProgress float32
	spinDir      float32
	darkGround   bool
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
	// the body bends as it turns, so the eyes ride the same bend: without it a turned body
	// pulls in from under the eye that is going round with it
	_, turn := g.bodyTurn()
	eyes, _ := g.projectEyes(turn, dx, rx, ry, false)
	return eyes
}

// projectEyes projects a pair of eyes onto the face sphere for the given body turn. When
// clipBack is true, eyes rotated past the horizon (during 360° orbit) hide cleanly behind
// the back rather than sliding across it
func (g gaze) projectEyes(bodyTurnVal, dx, rx, ry float32, clipBack bool) ([2]eyeOnFace, [2]bool) {
	forward, right, up := vec3{0, 0, 1}, vec3{1, 0, 0}, vec3{0, 1, 0}
	forward, right = spin3(forward, up, g.yaw), spin3(right, up, g.yaw)
	up, forward = spin3(up, right, g.pitch), spin3(forward, right, g.pitch)

	split := float32(math.Asin(float64(dx/faceRadius))) * 180 / math.Pi
	// an eye is drawn at the size it is given when the face looks straight ahead, so the
	// rest face is the design's and only a turn narrows it
	rest := float32(math.Cos(float64(split)*math.Pi/180)) * (0.85 + 0.15*float32(math.Cos(float64(split)*math.Pi/180)))
	var out [2]eyeOnFace
	var vis [2]bool
	for i, side := range [2]float32{-1, 1} {
		on := spin3(forward, up, side*split)
		if clipBack && on.z <= 0.04 {
			continue
		}
		across := spin3(right, up, side*split)
		narrow := float32(math.Hypot(float64(across.x), float64(across.y)))
		flat := float32(math.Hypot(float64(up.x), float64(up.y)))
		deep := 0.85 + 0.15*max(0, on.z)
		rim := float32(1)
		if clipBack {
			rim = min(float32(1), (on.z-0.04)/0.18)
		}
		width, height := rx*narrow*deep/rest*rim, ry*flat*deep/rest
		// the eye is drawn flat on the surface, so near the rim it would poke past the
		// outline: hold it inside the bent body, which is invisible and keeps it on it
		x, y := on.x*faceRadius, -on.y*faceRadius+eyeDrop
		x -= bodyTurnVal * x * x / faceRadius
		rightEdge := faceRadius * (1 - bodyTurnVal)
		leftEdge := -faceRadius * (1 + bodyTurnVal)
		x = min(max(x, leftEdge+width), rightEdge-width)
		y = clampTo(y, faceRadius-height)
		out[i] = eyeOnFace{
			x:    x,
			y:    y,
			rx:   width,
			ry:   height,
			tilt: float32(math.Atan2(float64(-across.y), float64(across.x))) * 180 / math.Pi,
		}
		vis[i] = !clipBack || width > 0.35
	}
	return out, vis
}

// pointerAim computes the cursor-tracking gaze target and proximity blend share [0, 1]
func pointerAim(dx, dy, size float32) (gaze, float32) {
	const reach = 3 // how many body widths away the pointer starts to matter
	far := size * reach
	dist := float32(math.Hypot(float64(dx), float64(dy)))
	share := 1 - dist/far
	if share <= 0 {
		return gaze{}, 0
	}
	share = share * share * (3 - 2*share) // smoothstep, so it doesn't snap
	// a dead zone right around the character: a pointer that close means "look at me", and it
	// keeps a hand resting on the character from flicking its eyes back and forth
	dx, dy = soft(dx, size*0.4), soft(dy, size*0.4)
	return gaze{
		yaw:   clampTo(float32(math.Atan2(float64(dx), float64(size*0.9)))*180/math.Pi, 30),
		pitch: clampTo(float32(math.Atan2(float64(dy), float64(size*0.9)))*180/math.Pi, 22),
	}, share
}

// toward turns the gaze onto a pointer (dx, dy) from the character's centre, taking the drift
// over entirely by the time the pointer is close: a character that notices you. It returns how
// much of the drift the pointer took over, 0 when it is out of reach
func (g gaze) toward(dx, dy, size float32) (gaze, float32) {
	aim, share := pointerAim(dx, dy, size)
	if share <= 0 {
		return g, 0
	}
	return gaze{
		yaw:   lerp32(g.yaw, aim.yaw, share),
		pitch: lerp32(g.pitch, aim.pitch, share),
		roll:  g.roll,
	}, share
}

// applyPointer turns the character (and decoupled pupils/eyes when working) toward a nearby pointer
func (p *pose) applyPointer(dx, dy, size float32) float32 {
	if !p.decoupled {
		var share float32
		p.gaze, share = p.gaze.toward(dx, dy, size)
		return share
	}
	aimed, share := pointerAim(dx, dy, size)
	if share <= 0 {
		return 0
	}
	// Normalize yaws into (-180, 180] so hovering mid-orbit turns back via the shortest angle.
	baseEyeYaw := wrapDeg(p.eyeGaze.yaw)
	baseBodyYaw := wrapDeg(p.gaze.yaw)
	p.eyeGaze = gaze{
		yaw:   lerp32(baseEyeYaw, aimed.yaw, share),
		pitch: lerp32(p.eyeGaze.pitch, aimed.pitch, share),
		roll:  lerp32(p.eyeGaze.roll, aimed.roll*0.6, share),
	}
	p.gaze = gaze{
		yaw:   lerp32(baseBodyYaw, aimed.yaw*0.78, share),
		pitch: lerp32(p.gaze.pitch, aimed.pitch*0.75, share),
		roll:  lerp32(p.gaze.roll, aimed.roll*0.6, share),
	}
	px, py := aimed.pupilLook()
	p.pupilX = lerp32(p.pupilX, px, share)
	p.pupilY = lerp32(p.pupilY, py, share)
	eyeH := p.eyeScaleY
	if eyeH <= 0 {
		eyeH = 1
	}
	p.eyeScaleY = lerp32(eyeH, 1.06, share)
	p.spinProgress *= 1 - share
	return share
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

// avatarEpoch anchors working-state schedules so animations stay deterministic across calls and tests
var avatarEpoch = time.Unix(1700000000, 0)

// idle adds the small time-driven motions: working characters scan calmly with an occasional
// 360° orbit loop, needs-you bobs and blinks, and drift gently moves resting faces
func idle(p *pose, seed uint32, now time.Time) {
	ms := now.UnixMilli() + int64(seed%7919)
	p.blink = 1

	if p.mood == moodWorking {
		applyWorkingOrbit(p, seed, now.Sub(avatarEpoch).Seconds())
	} else {
		if p.mood == moodNeedsYou {
			period := int64(3400 + seed%1800)
			at := time.Duration(ms%period) * time.Millisecond
			if at < blinkSpan {
				p.blink = float32(1 - math.Sin(float64(at)/float64(blinkSpan)*math.Pi))
			}
		}
		p.gaze, p.breath, p.lift = drift(p.mood, seed, float64(ms)/1000)
	}

	if p.bubble > 0 {
		t := float64(ms%nudgeEvery.Milliseconds()) / float64(nudgeEvery.Milliseconds())
		p.nudge = ease(1 - math.Abs(2*t-1))
	}
}

const workingBeatCount = 24

type workingBeat struct {
	dur, trans       float64
	yaw, pitch, roll float32
	eyeScaleY, lift  float32
}

type curveSample struct {
	yaw, pitch, roll float32
	eyeScaleY, lift  float32
	velYaw           float32
	inTrans          bool
}

// applyWorkingOrbit populates p with Variant 4 (Orbit Loop): ~5.2s of calm ambient scanning
// with eye-lead / body-lag and natural double-blinks, followed by a brisk 0.56s 360° spin
func applyWorkingOrbit(p *pose, seed uint32, seconds float64) {
	const scanDur = 5.20
	const spinDur = 0.56
	const cycleDur = scanDur + spinDur
	const anchorYaw = float32(8)
	const anchorPitch = float32(1)

	t := seconds + float64(seed%53)*0.37
	cycleIdx := int(math.Floor(t / cycleDur))
	local := t - float64(cycleIdx)*cycleDur

	beats := buildWorkingBeats(seed ^ uint32(cycleIdx*2654435761))
	anchorPX, anchorPY := (gaze{yaw: anchorYaw, pitch: anchorPitch}).pupilLook()

	if local < scanDur {
		sampleDecoupledWorking(p, seed, t, beats[:])
		if local > scanDur-0.35 {
			blend := easeOutCubic((local - (scanDur - 0.35)) / 0.35)
			p.eyeGaze.yaw = lerp32(p.eyeGaze.yaw, anchorYaw, blend)
			p.eyeGaze.pitch = lerp32(p.eyeGaze.pitch, anchorPitch, blend)
			p.gaze.yaw = lerp32(p.gaze.yaw, anchorYaw*0.50, blend)
			p.gaze.pitch = lerp32(p.gaze.pitch, anchorPitch*0.50, blend)
			p.gaze.roll = lerp32(p.gaze.roll, 0, blend)
			p.eyeGaze.roll = p.gaze.roll
			p.pupilX = lerp32(p.pupilX, anchorPX, blend)
			p.pupilY = lerp32(p.pupilY, anchorPY, blend)
			p.eyeScaleY = lerp32(p.eyeScaleY, 1, blend)
			p.fastMotion = true
		} else if local < 0.35 {
			blend := easeOutCubic(local / 0.35)
			p.eyeGaze.yaw = lerp32(anchorYaw, p.eyeGaze.yaw, blend)
			p.eyeGaze.pitch = lerp32(anchorPitch, p.eyeGaze.pitch, blend)
			p.gaze.yaw = lerp32(anchorYaw*0.50, p.gaze.yaw, blend)
			p.gaze.pitch = lerp32(anchorPitch*0.50, p.gaze.pitch, blend)
			p.pupilX = lerp32(anchorPX, p.pupilX, blend)
			p.pupilY = lerp32(anchorPY, p.pupilY, blend)
			p.eyeScaleY = lerp32(1, p.eyeScaleY, blend)
			p.fastMotion = true
		}
		return
	}

	u := (local - scanDur) / spinDur
	spinEase := easeInOutQuart(u)
	dir := float32(1)
	if (cycleIdx+int(seed))%2 == 1 {
		dir = -1
	}

	// Eyes lead slightly into the spin, then re-lock at the anchor when returning to the front.
	eyeSpin := easeInOutQuart(min(1, u*1.04))
	hop := float32(math.Sin(math.Pi * u))
	roll := dir * -6.5 * float32(math.Sin(2*math.Pi*u))

	// Punctuate landing with a crisp blink right as the character faces front again.
	blink := float32(1)
	if u > 0.85 {
		b := (u - 0.85) / 0.15
		blink = float32(1 - 0.92*math.Sin(b*math.Pi))
	}
	spinPX, spinPY := (gaze{yaw: dir * 18 * hop}).pupilLook()
	px := lerp32(anchorPX, spinPX, min(1, hop*2))
	py := lerp32(anchorPY, spinPY, min(1, hop*2))

	p.decoupled = true
	p.eyeGaze = gaze{yaw: anchorYaw + dir*360*eyeSpin, pitch: anchorPitch * (1 - hop), roll: roll}
	p.gaze = gaze{yaw: anchorYaw*0.50 + dir*360*spinEase, pitch: anchorPitch * 0.50 * (1 - hop), roll: roll}
	p.pupilX, p.pupilY = px, py
	p.eyeScaleY = 1 + 0.04*hop
	p.blink = blink
	p.breath = -0.024*hop + 0.014*float32(math.Sin(2*math.Pi*u))
	p.lift = 2.2 * hop
	p.fastMotion = true
	p.orbitClip = true
	p.spinProgress = float32(u)
	p.spinDir = dir
}

func sampleDecoupledWorking(p *pose, seed uint32, t float64, beats []workingBeat) {
	total := beatsDuration(beats)
	tb := t + float64(seed%97)*0.31

	// Pupils lead first (fastest transition), eyeballs follow 40ms behind, body follows 150ms behind.
	pupilS := evalBeats(beats, total, tb, 0.72, easeOutQuint)
	eyeS := evalBeats(beats, total, tb-0.04, 1.0, easeOutQuint)
	bodyS := evalBeats(beats, total, tb-0.15, 2.1, easeOutCubic)

	micro := float32(math.Sin(2 * math.Pi * wrapCycle(tb, 4.3) / 4.3))
	p.decoupled = true
	p.eyeGaze = gaze{
		yaw:   eyeS.yaw + 0.7*micro,
		pitch: eyeS.pitch + 0.4*float32(math.Cos(2*math.Pi*wrapCycle(tb, 5.7)/5.7)),
		roll:  bodyS.roll,
	}
	p.gaze = gaze{
		yaw:   bodyS.yaw * 0.50,
		pitch: bodyS.pitch * 0.50,
		roll:  bodyS.roll,
	}
	px, py := (gaze{yaw: pupilS.yaw, pitch: pupilS.pitch}).pupilLook()
	p.pupilX = clampTo(px+clampTo(pupilS.velYaw*0.10, 0.16), 0.45)
	p.pupilY = clampTo(py, 0.36)
	p.eyeScaleY = eyeS.eyeScaleY
	var blinking bool
	p.blink, blinking = seededBlink(seed, tb)
	p.breath = float32(0.011 * math.Sin(2*math.Pi*wrapCycle(tb, 3.4)/3.4+float64(seed%13)))
	p.lift = bodyS.lift
	p.fastMotion = pupilS.inTrans || eyeS.inTrans || blinking
}

func buildWorkingBeats(seed uint32) [workingBeatCount]workingBeat {
	var out [workingBeatCount]workingBeat
	for i := range workingBeatCount {
		h := hash32(seed ^ uint32(i*0x9e3779b9+17))
		r0 := unitRand(h)
		r1 := unitRand(hash32(h ^ 0x85ebca6b))
		r2 := unitRand(hash32(h ^ 0xc2b2ae35))

		var yaw, pitch, roll, lift float32
		scaleY := float32(1)
		// Calm ambient dwell (1.15s..2.35s) and gentle transitions (0.19s..0.28s) so it never steals focus.
		dur := 1.15 + float64(r0)*1.10
		trans := 0.19 + float64(r1)*0.08

		switch i % 5 {
		case 0: // Settle left & slightly down (checking editor/terminal).
			yaw = -8 - r1*9
			pitch = 3 + r2*6
			roll = 3.0
		case 1: // Calm center-down intermediate stop (avoids full L->R whip).
			yaw = (r1 - 0.5) * 10
			pitch = 4 + r2*5
			roll = (r1 - 0.5) * -3.0
			scaleY = 0.97
			lift = -0.3
		case 2: // Look right (reading output).
			yaw = 8 + r1*10
			pitch = 1 + r2*6
			roll = -3.0
		case 3: // Look up-sideways thinking ("hmmm...").
			side := float32(1)
			if h&1 == 0 {
				side = -1
			}
			yaw = side * (7 + r1*8)
			pitch = -8 - r2*4
			roll = -side * 4.5
			scaleY = 0.90
			lift = 0.9
			dur = 1.45 + float64(r0)*0.90
			trans = 0.25
		default: // Gentle mid-range re-focus.
			yaw = (r1 - 0.5) * 20
			pitch = -1 + r2*4
			roll = -yaw * 0.18
			scaleY = 1.03
			lift = 0.6
			dur = 1.05 + float64(r0)*0.70
			trans = 0.22
		}

		out[i] = workingBeat{
			dur:       dur,
			trans:     trans,
			yaw:       yaw,
			pitch:     pitch,
			roll:      roll,
			eyeScaleY: scaleY,
			lift:      lift,
		}
	}
	return out
}

func beatsDuration(beats []workingBeat) float64 {
	var total float64
	for _, b := range beats {
		total += b.dur
	}
	return total
}

func evalBeats(beats []workingBeat, total, t, transScale float64, easeFn func(float64) float32) curveSample {
	t = wrapCycle(t, total)
	acc := 0.0
	idx := 0
	for i, b := range beats {
		if t < acc+b.dur || i == len(beats)-1 {
			idx = i
			break
		}
		acc += b.dur
	}
	prev := beats[(idx-1+len(beats))%len(beats)]
	cur := beats[idx]
	elapsed := t - acc
	tr := min(cur.dur*0.78, cur.trans*transScale)
	if tr > 0 && elapsed < tr {
		k := easeFn(elapsed / tr)
		bell := float32(math.Sin(math.Pi * elapsed / tr))
		return curveSample{
			yaw:       lerp32(prev.yaw, cur.yaw, k),
			pitch:     lerp32(prev.pitch, cur.pitch, k),
			roll:      lerp32(prev.roll, cur.roll, k),
			eyeScaleY: lerp32(prev.eyeScaleY, cur.eyeScaleY, k),
			lift:      lerp32(prev.lift, cur.lift, k),
			velYaw:    (cur.yaw - prev.yaw) * bell / 20,
			inTrans:   true,
		}
	}
	return curveSample{
		yaw:       cur.yaw,
		pitch:     cur.pitch,
		roll:      cur.roll,
		eyeScaleY: cur.eyeScaleY,
		lift:      cur.lift,
	}
}

func seededBlink(seed uint32, t float64) (float32, bool) {
	// 12 seeded blink events over ~32s with ~25% double-blinks.
	const slots = 12
	var durs [slots]float64
	var total float64
	for i := range slots {
		r := float64(unitRand(hash32(seed ^ uint32(i*0x517cc1b7+31))))
		durs[i] = 1.9 + r*2.3
		total += durs[i]
	}
	local := wrapCycle(t, total)
	acc := 0.0
	idx := 0
	for i := range slots {
		if local < acc+durs[i] || i == slots-1 {
			idx = i
			break
		}
		acc += durs[i]
	}
	dt := local - acc
	const span = 0.165
	if dt < span {
		return blinkCurve(dt / span), true
	}
	double := hash32(seed^uint32(idx*0x94d049bb))%4 == 0
	if double && dt >= 0.22 && dt < 0.22+0.145 {
		return blinkCurve((dt - 0.22) / 0.145), true
	}
	return 1, false
}

func blinkCurve(u float64) float32 {
	if u < 0.42 {
		return 1 - float32(math.Sin(u/0.42*math.Pi/2))
	}
	return easeOutCubic((u - 0.42) / 0.58)
}

func hash32(x uint32) uint32 {
	x ^= x >> 16
	x *= 0x7feb352d
	x ^= x >> 15
	x *= 0x846ca68b
	x ^= x >> 16
	return x
}

func unitRand(h uint32) float32 { return float32(float64(h) / float64(1<<32)) }

func wrapCycle(t, total float64) float64 {
	if total <= 0 {
		return 0
	}
	m := math.Mod(t, total)
	if m < 0 {
		m += total
	}
	return m
}

func wrapDeg(a float32) float32 {
	m := math.Mod(float64(a)+180, 360)
	if m < 0 {
		m += 360
	}
	return float32(m - 180)
}

func lerp32(a, b, t float32) float32 { return a + (b-a)*t }

func easeOutCubic(t float64) float32 {
	u := 1 - max(0, min(1, t))
	return float32(1 - u*u*u)
}

func easeOutQuint(t float64) float32 {
	u := 1 - max(0, min(1, t))
	return float32(1 - u*u*u*u*u)
}

func easeInOutQuart(t float64) float32 {
	t = max(0, min(1, t))
	if t < 0.5 {
		return float32(8 * t * t * t * t)
	}
	u := -2*t + 2
	return float32(1 - u*u*u*u/2)
}

// drift is where non-working characters look, breathe, and rise at idle. Each expression moves
// on its own tempo and leans on its own axis, using incommensurate periods so motion never loops
func drift(m mood, seed uint32, seconds float64) (gaze, float32, float32) {
	phase := float64(seed%360) * math.Pi / 180
	wobble := func(period, offset float64) float64 {
		return math.Sin(2*math.Pi*seconds/period + phase + offset)
	}

	// how far each expression looks, along which axis, and how fast it does it
	var yaw, pitch, roll, tempo float64
	switch m {
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

// addBody appends a body's outline or rounded rectangle onto s.
func (s *shape) addBody(b body) {
	if len(b.outline) > 0 {
		s.move(b.outline[0].x, b.outline[0].y)
		for _, q := range b.outline[1:] {
			s.line(q.x, q.y)
		}
		s.path.Close()
		return
	}
	var radii [4][2]float32
	for i, k := range b.radii {
		radii[i] = [2]float32{k.rx * 64, k.ry * b.height}
	}
	s.roundRect(0, 0, 64, b.height, radii)
}

func workingBodyTurn(g gaze, orbit bool) (squeeze, turn float32) {
	if !orbit {
		return g.bodyTurn()
	}
	rad := float64(g.yaw) * math.Pi / 180
	return 0.78 + 0.22*float32(math.Abs(math.Cos(rad))), 0.22 * float32(math.Sin(rad))
}

func paintAvatar(pt *ui.Painter, r ui.Rect, ch character, p pose, pr prop) {
	u := r.W / 64
	cx, cy := r.X+r.W/2, r.Y+r.H/2+(p.gaze.lift()-p.lift)*u

	showArcs := p.squash > 0.02 && p.spinProgress > 0.02 && p.spinProgress < 0.98
	if showArcs {
		paintOrbitArcs(pt, cx, cy, u, ch.color(), p, false)
	}

	b := ch.body()
	body := newShape(cx, cy, u, b.rotate+p.gaze.lean())
	squeeze, turn := workingBodyTurn(p.gaze, p.orbitClip)
	// Breathing keeps the body's volume: it widens by what it flattens
	body.pivot, body.sx, body.sy = 0, (1+p.breath)*squeeze, 1-p.breath
	body.turn = turn
	body.addBody(b)
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

	if showArcs {
		paintOrbitArcs(pt, cx, cy, u, ch.color(), p, true)
	}
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
		eyeG := p.gaze
		px, py := eyeG.pupilLook()
		if p.decoupled {
			eyeG = p.eyeGaze
			px, py = p.pupilX, p.pupilY
		}
		eyesLayer := layer(true)
		if p.eyeScaleY > 0 {
			eyesLayer.sy *= p.eyeScaleY
		}
		var eyes [2]eyeOnFace
		var vis [2]bool
		if p.orbitClip {
			_, turn := workingBodyTurn(p.gaze, true)
			eyes, vis = eyeG.projectEyes(turn, 10.5, 7.5, 9, true)
		} else {
			eyes = eyeG.eyes(10.5, 7.5, 9)
			vis = [2]bool{true, true}
		}
		for i, e := range eyes {
			if vis[i] {
				paintEye(pt, eyesLayer, e, px, py, 0.53, 0.5)
			}
		}
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

type orbitArcSpec struct {
	cyOff    float32
	rx, ry   float32
	tiltDeg  float32
	startDeg float32
	sweepDeg float32
	maxHalfW float32
	alpha    float32
	planar   bool
}

func paintOrbitArcs(pt *ui.Painter, cx, cy, u float32, bodyCol ui.Color, p pose, front bool) {
	dir := p.spinDir
	if dir == 0 {
		dir = 1
	}
	env := float32(math.Pow(math.Sin(math.Pi*float64(p.spinProgress)), 0.65)) * p.squash
	if env < 0.02 {
		return
	}
	base := bodyCol
	if p.darkGround && faceInk(bodyCol) > 0.5 {
		base = eyeWhite
	}
	phase := easeInOutQuart(float64(p.spinProgress)) * 450 * dir

	arcs := [3]orbitArcSpec{
		// Lower orbital ribbon wrapping cleanly below eye level.
		{
			cyOff:    19,
			rx:       42,
			ry:       7.5,
			tiltDeg:  -10 * dir,
			startDeg: phase - 130*dir,
			sweepDeg: 135 * dir,
			maxHalfW: 1.85,
			alpha:    0.76 * env,
		},
		// Counter-tilted upper crown halo arc (stays well above eye level).
		{
			cyOff:    -24,
			rx:       39,
			ry:       6.0,
			tiltDeg:  12 * dir,
			startDeg: phase + 55*dir,
			sweepDeg: 120 * dir,
			maxHalfW: 1.55,
			alpha:    0.62 * env,
		},
		// Outer silhouette speed arc in the screen plane (back pass only).
		{
			cyOff:    0,
			rx:       40,
			ry:       40,
			tiltDeg:  0,
			startDeg: phase*0.8 + 145*dir,
			sweepDeg: 80 * dir,
			maxHalfW: 1.45,
			alpha:    0.50 * env,
			planar:   true,
		},
	}

	for _, a := range arcs {
		col := base.Alpha(a.alpha)
		if a.planar {
			if !front {
				paintOrbitRibbon(pt, cx, cy, u, a, col, false, true)
			}
			continue
		}
		paintOrbitRibbon(pt, cx, cy, u, a, col, front, false)
	}
}

type ribbonSample struct {
	x, y float32
	nx   float32
	ny   float32
	hw   float32
	sinA float32
}

func paintOrbitRibbon(pt *ui.Painter, cx, cy, u float32, a orbitArcSpec, col ui.Color, wantFront, ignoreDepth bool) {
	const steps = 28
	tiltRad := float64(a.tiltDeg) * math.Pi / 180
	ct, st := float32(math.Cos(tiltRad)), float32(math.Sin(tiltRad))

	eval := func(t float32) ribbonSample {
		ang := float64(a.startDeg+a.sweepDeg*t) * math.Pi / 180
		ca, sa := float32(math.Cos(ang)), float32(math.Sin(ang))
		lx, ly := a.rx*ca, a.ry*sa
		sign := float32(1)
		if a.sweepDeg < 0 {
			sign = -1
		}
		tx, ty := -a.rx*sa*sign, a.ry*ca*sign
		rxp, ryp := lx*ct-ly*st, lx*st+ly*ct
		rtx, rty := tx*ct-ty*st, tx*st+ty*ct
		tl := float32(math.Hypot(float64(rtx), float64(rty)))
		var nx, ny float32
		if tl > 0 {
			nx, ny = -rty/tl, rtx/tl
		}
		taper := float32(math.Sin(math.Pi*float64(t))) * (0.4 + 0.6*t)
		return ribbonSample{
			x:    cx + rxp*u,
			y:    cy + (ryp+a.cyOff)*u,
			nx:   nx,
			ny:   ny,
			hw:   a.maxHalfW * taper * u,
			sinA: sa,
		}
	}

	var samples [steps + 1]ribbonSample
	for i := 0; i <= steps; i++ {
		samples[i] = eval(float32(i) / steps)
	}

	emitRun := func(run []ribbonSample) {
		if len(run) < 2 {
			return
		}
		var path ui.Path
		for i, s := range run {
			px, py := s.x+s.nx*s.hw, s.y+s.ny*s.hw
			if i == 0 {
				path.MoveTo(px, py)
			} else {
				path.LineTo(px, py)
			}
		}
		for i := len(run) - 1; i >= 0; i-- {
			s := run[i]
			path.LineTo(s.x-s.nx*s.hw, s.y-s.ny*s.hw)
		}
		path.Close()
		pt.FillPath(&path, col)
	}

	if ignoreDepth {
		emitRun(samples[:])
		return
	}

	inHalf := func(sinA float32) bool {
		if wantFront {
			return sinA >= 0
		}
		return sinA <= 0
	}

	var runBuf [steps + 2]ribbonSample
	run := runBuf[:0]
	for i, s := range samples {
		curIn := inHalf(s.sinA)
		if i > 0 {
			prev := samples[i-1]
			prevIn := inHalf(prev.sinA)
			if curIn != prevIn && s.sinA != prev.sinA {
				// Interpolate exact sin(a)==0 crossing so back & front halves meet cleanly outside body.
				frac := -prev.sinA / (s.sinA - prev.sinA)
				tCross := (float32(i-1) + frac) / steps
				cross := eval(tCross)
				if prevIn {
					run = append(run, cross)
					emitRun(run)
					run = runBuf[:0]
				} else {
					run = append(runBuf[:0], cross)
				}
			}
		}
		if curIn {
			run = append(run, s)
		}
	}
	emitRun(run)
}

// paintEyes draws a pair of eye whites with pupils, each one placed, narrowed and tilted by
// the sphere the face sits on; ox and oy are the pupil's offset as a share of the eye, prx
// and pry its size as a share of the eye's
func paintEyes(pt *ui.Painter, s *shape, eyes [2]eyeOnFace, ox, oy, prx, pry float32) {
	for _, e := range eyes {
		paintEye(pt, s, e, ox, oy, prx, pry)
	}
}

func paintEye(pt *ui.Painter, s *shape, e eyeOnFace, ox, oy, prx, pry float32) {
	white := s.tilted(e.x, e.y, e.tilt)
	white.ellipse(e.x, e.y, e.rx, e.ry)
	pt.FillPath(&white.path, eyeWhite)

	px, py := e.x+e.rx*ox, e.y+e.ry*oy
	pupil := s.tilted(px, py, e.tilt)
	pupil.ellipse(px, py, e.rx*prx, e.ry*pry)
	pt.FillPath(&pupil.path, eyeInk)
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
