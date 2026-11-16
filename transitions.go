package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

const (
	listTransitionDuration = 220 * time.Millisecond
	heroFlightDuration     = 240 * time.Millisecond
	overlayFadeDuration    = 180 * time.Millisecond
	pressFadeDuration      = 80 * time.Millisecond
	moodResetAfter         = 2 * time.Second
)

var (
	// collapseTransition slides/resizes existing siblings and expands/collapses
	// entering/exiting rows in columns.
	collapseTransition = ui.ElementTransition{
		Duration: listTransitionDuration,
		Ease:     ui.EaseOut,
		Position: true,
		Size:     true,
		Enter:    &ui.Motion{Collapse: true},
		Exit:     &ui.Motion{Collapse: true},
	}

	// reflowTransition animates sibling position/size shifts without enter/exit collapse.
	reflowTransition = ui.ElementTransition{
		Duration: listTransitionDuration,
		Ease:     ui.EaseOut,
		Position: true,
		Size:     true,
	}

	// gridTileTransition animates grid tiles without collapsing single cells out of row alignment.
	gridTileTransition = ui.ElementTransition{
		Duration: listTransitionDuration,
		Ease:     ui.EaseOut,
		Position: true,
		Size:     true,
		Enter:    &ui.Motion{Y: 6},
		Exit:     &ui.Motion{Y: -6},
	}

	// pillCollapseTransition collapses status pills along their row width.
	pillCollapseTransition = ui.ElementTransition{
		Duration: overlayFadeDuration,
		Ease:     ui.EaseOut,
		Position: true,
		Size:     true,
		Enter:    &ui.Motion{Collapse: true},
		Exit:     &ui.Motion{Collapse: true},
	}

	// pillReflowTransition smoothly slides trailing row metadata alongside pill enter/exit.
	pillReflowTransition = ui.ElementTransition{
		Duration: overlayFadeDuration,
		Ease:     ui.EaseOut,
		Position: true,
		Size:     true,
	}

	// fadeTransition crossfades mutually exclusive full-area views (e.g. empty state).
	fadeTransition = ui.ElementTransition{
		Duration: overlayFadeDuration,
		Ease:     ui.EaseOut,
		Position: true,
		Enter:    &ui.Motion{},
		Exit:     &ui.Motion{},
	}

	// tabsTransition fades and subtly nudges header tabs on project push/pop.
	tabsTransition = ui.ElementTransition{
		Duration: overlayFadeDuration,
		Ease:     ui.EaseOut,
		Position: true,
		Size:     true,
		Enter:    &ui.Motion{X: -8},
		Exit:     &ui.Motion{X: -8},
	}
)

func bool01(v bool) float32 {
	if v {
		return 1
	}
	return 0
}

// animatedStateColor smoothly blends base -> hover (140ms) -> press (80ms) on e.
// When canHover is false, base is returned unchanged with no animations registered.
func animatedStateColor(e ui.Element, key string, base, hover, press ui.Color, canHover bool) ui.Color {
	if !canHover {
		return base
	}
	h := e.Animate(key+":hover", bool01(e.Hovered()), rowHoverFade)
	pr := e.Animate(key+":press", bool01(e.Pressed()), pressFadeDuration)
	return base.Mix(hover, h).Mix(press, pr)
}

type moodChannel struct {
	from, to, value float32
	start           time.Time
	dur             time.Duration
	init            bool
}

type projectMoodState struct {
	channels [moodCount]moodChannel
	lastSeen time.Time
}

// moodTracker keeps per-project expression morphs running continuously across
// container changes (such as Needs -> Running card moves).
type moodTracker struct {
	byProject map[string]*projectMoodState
}

func (mt *moodTracker) eval(project string, now time.Time, target mood, reduceMotion bool) ([moodCount]float32, bool) {
	if mt.byProject == nil {
		mt.byProject = map[string]*projectMoodState{}
	}
	st := mt.byProject[project]
	if st == nil {
		st = &projectMoodState{}
		mt.byProject[project] = st
	}
	if !st.lastSeen.IsZero() && now.Sub(st.lastSeen) > moodResetAfter {
		st.channels = [moodCount]moodChannel{}
	}
	st.lastSeen = now

	var shown [moodCount]float32
	animating := false
	for i := range st.channels {
		ch := &st.channels[i]
		want := bool01(i == int(target))
		if !ch.init || (ch.to != want && reduceMotion) {
			ch.from, ch.to, ch.value = want, want, want
			ch.dur, ch.init = 0, true
		} else if ch.to != want {
			ch.from, ch.to, ch.start, ch.dur = ch.value, want, now, faceShift
		}
		if ch.value != ch.to {
			t := float32(now.Sub(ch.start)) / float32(max(ch.dur, time.Millisecond))
			if t >= 1 {
				ch.value = ch.to
			} else {
				ch.value = ch.from + (ch.to-ch.from)*ui.EaseInOut(max(t, 0))
				animating = true
			}
		}
		shown[i] = ch.value
	}
	return shown, animating
}

type todayBucket uint8

const (
	bucketNone todayBucket = iota
	bucketNeeds
	bucketRest
)

type committedHeroObs struct {
	bucket   todayBucket
	winRect  ui.Rect
	relRect  ui.Rect
	ground   ui.Color
	hasRel   bool
	seq      uint64
	hasPaint bool
}

type heroSlotObservation struct {
	bucket  todayBucket
	winRect ui.Rect
	ground  ui.Color
	seq     uint64
}

type heroFlight struct {
	project    string
	fromRect   ui.Rect
	fromColor  ui.Color
	toBucket   todayBucket
	toHandle   ui.Handle
	toRect     ui.Rect
	hasToRect  bool
	lastRect   ui.Rect
	lastColor  ui.Color
	start      time.Time
	done       bool
	destProp   prop
	destMood   mood
	destGround ui.Color
}

// heroCoordinator orchestrates cross-bucket hero avatar flights on Today.
// View builds only read committed state and bind handles; Draw captures start
// timestamps and committed positions so multi-pass layout cannot skew motion.
type heroCoordinator struct {
	layerHandle   ui.Handle
	activeBuckets map[string]todayBucket
	committed     map[string]committedHeroObs
	pending       map[string]heroSlotObservation
	flights       map[string]*heroFlight
	lastLayout    int
	lastWinW      float32
	seq           uint64
}

func (hc *heroCoordinator) reset() {
	hc.activeBuckets = nil
	hc.committed = nil
	hc.pending = nil
	hc.flights = nil
	hc.lastWinW = 0
}

// beginToday inspects current bucket assignments and starts/reverses hero flights
// when a project moves between Needs and Running/Resting without a layout swap.
func (hc *heroCoordinator) beginToday(layout int, winW float32, reduceMotion bool, buckets map[string]todayBucket) {
	hc.activeBuckets = buckets
	if hc.flights == nil {
		hc.flights = map[string]*heroFlight{}
	}
	if reduceMotion || (hc.lastWinW > 0 && (hc.lastWinW != winW || hc.lastLayout != layout)) {
		hc.flights = map[string]*heroFlight{}
		hc.committed = nil
		hc.pending = nil
		hc.lastLayout = layout
		hc.lastWinW = winW
		return
	}
	hc.lastLayout = layout
	hc.lastWinW = winW

	for name, b := range buckets {
		if f := hc.flights[name]; f != nil && !f.done {
			if f.toBucket != b && f.lastRect.W > 0 && f.lastRect.H > 0 {
				// Reverse seamlessly from the avatar's current in-flight bounds.
				hc.flights[name] = &heroFlight{
					project:   name,
					fromRect:  f.lastRect,
					fromColor: f.lastColor,
					toBucket:  b,
					lastRect:  f.lastRect,
					lastColor: f.lastColor,
				}
			}
			continue
		}
		obs, ok := hc.committed[name]
		if !ok || !obs.hasRel || obs.seq != hc.seq {
			continue
		}
		if obs.bucket != bucketNone && b != bucketNone && obs.bucket != b {
			hc.flights[name] = &heroFlight{
				project:   name,
				fromRect:  obs.relRect,
				fromColor: obs.ground,
				toBucket:  b,
				lastRect:  obs.relRect,
				lastColor: obs.ground,
			}
		}
	}

	for name, f := range hc.flights {
		b, exists := buckets[name]
		if !exists || (f.done && f.toBucket == b) {
			delete(hc.flights, name)
		}
	}
}

// slotHandle returns the ui.Handle to bind on a card's avatar when it is the
// destination of an active hero flight, along with whether that card renders it.
func (hc *heroCoordinator) slotHandle(project string, bucket todayBucket) *ui.Handle {
	f := hc.flights[project]
	if f == nil || f.done || f.toBucket != bucket {
		return nil
	}
	return &f.toHandle
}

// syncFlightTarget updates a flight's destination presentation metadata and reads
// committed destination bounds from the prior frame.
func (hc *heroCoordinator) syncFlightTarget(c *ui.Context, project string, bucket todayBucket, m mood, pr prop, ground ui.Color) {
	f := hc.flights[project]
	if f == nil || f.done || f.toBucket != bucket {
		return
	}
	f.destMood = m
	f.destProp = pr
	f.destGround = ground

	layerR := hc.layerHandle.Bounds(c)
	toR := f.toHandle.Bounds(c)
	if layerR.W > 0 && toR.W > 0 && toR.H > 0 {
		f.toRect = ui.Rect{
			X: toR.X - layerR.X,
			Y: toR.Y - layerR.Y,
			W: toR.W,
			H: toR.H,
		}
		f.hasToRect = true
	}
}

func (hc *heroCoordinator) isFlying(project string) bool {
	f := hc.flights[project]
	return f != nil && !f.done
}

func (hc *heroCoordinator) recordPaint(project string, bucket todayBucket, winRect ui.Rect, ground ui.Color) {
	if hc.activeBuckets[project] != bucket {
		return
	}
	if hc.pending == nil {
		hc.pending = map[string]heroSlotObservation{}
	}
	hc.pending[project] = heroSlotObservation{
		bucket:  bucket,
		winRect: winRect,
		ground:  ground,
		seq:     hc.seq + 1,
	}
}

type heroPaintFrame struct {
	project  string
	rect     ui.Rect
	ground   ui.Color
	destMood mood
	destProp prop
}

// advanceOverlay commits current frame observations relative to layerR and returns
// active hero flights ready to paint for this frame.
func (hc *heroCoordinator) advanceOverlay(pt *ui.Painter, layerR ui.Rect, reduceMotion bool) []heroPaintFrame {
	hc.seq++
	if hc.committed == nil {
		hc.committed = map[string]committedHeroObs{}
	}
	for name, obs := range hc.pending {
		hc.committed[name] = committedHeroObs{
			bucket:  obs.bucket,
			winRect: obs.winRect,
			relRect: ui.Rect{
				X: obs.winRect.X - layerR.X,
				Y: obs.winRect.Y - layerR.Y,
				W: obs.winRect.W,
				H: obs.winRect.H,
			},
			ground:   obs.ground,
			hasRel:   true,
			seq:      hc.seq,
			hasPaint: true,
		}
	}
	clear(hc.pending)

	if len(hc.flights) == 0 {
		return nil
	}
	now := pt.Now()
	var frames []heroPaintFrame
	for name, f := range hc.flights {
		if f.done {
			continue
		}
		if reduceMotion {
			f.done = true
			pt.AnimationFrame()
			continue
		}
		if f.start.IsZero() {
			f.start = now
		}
		rawT := float32(now.Sub(f.start)) / float32(heroFlightDuration)
		if rawT < 0 {
			rawT = 0
		}
		var u float32
		targetRect := f.fromRect
		if f.hasToRect {
			targetRect = f.toRect
			u = ui.EaseOut(min(rawT, 1))
		}
		r := lerpRect(f.fromRect, targetRect, u)
		ground := f.fromColor.Mix(f.destGround, u)
		f.lastRect = r
		f.lastColor = ground

		absR := ui.Rect{
			X: layerR.X + r.X,
			Y: layerR.Y + r.Y,
			W: r.W,
			H: r.H,
		}
		frames = append(frames, heroPaintFrame{
			project:  name,
			rect:     absR,
			ground:   ground,
			destMood: f.destMood,
			destProp: f.destProp,
		})

		// Commit final slot coordinates once landed so subsequent hops start accurately.
		if f.hasToRect && rawT >= 1 {
			f.done = true
			hc.committed[name] = committedHeroObs{
				bucket:   f.toBucket,
				relRect:  f.toRect,
				ground:   f.destGround,
				hasRel:   true,
				seq:      hc.seq,
				hasPaint: true,
			}
			pt.AnimationFrame()
		} else {
			pt.AnimationFrame()
		}
	}
	return frames
}

func lerpRect(a, b ui.Rect, t float32) ui.Rect {
	return ui.Rect{
		X: a.X + (b.X-a.X)*t,
		Y: a.Y + (b.Y-a.Y)*t,
		W: a.W + (b.W-a.W)*t,
		H: a.H + (b.H-a.H)*t,
	}
}
