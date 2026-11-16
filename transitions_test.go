package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/zackzhou-work/spark-go/internal/monitor"
)

func testFig1Groups(base time.Time) []monitor.ProjectGroup {
	return []monitor.ProjectGroup{
		{
			ProjectName: "fix_avatar_eyes_demo",
			Sessions: []monitor.SessionItem{
				{
					ID:         "fix-wait",
					Source:     monitor.SourceClaude,
					Title:      "Fix eye blink alignment",
					State:      monitor.Waiting,
					WaitReason: monitor.WaitPermission,
					PID:        100,
					Terminal:   "t1",
					ActivityAt: base.Add(-2 * time.Minute),
					IsToday:    true,
				},
			},
		},
		{
			ProjectName: "official-knowledge",
			Sessions: []monitor.SessionItem{
				{
					ID:         "ok-1",
					Source:     monitor.SourceClaude,
					Title:      "帮我看一下目录树的接口。",
					State:      monitor.Unread,
					PID:        101,
					Terminal:   "t2",
					ActivityAt: base.Add(-5 * time.Minute),
					IsToday:    true,
				},
			},
		},
	}
}

func testFig2Groups(base time.Time) []monitor.ProjectGroup {
	return []monitor.ProjectGroup{
		{
			ProjectName: "fix_avatar_eyes_demo",
			Sessions: []monitor.SessionItem{
				{
					ID:         "fix-run",
					Source:     monitor.SourceClaude,
					Title:      "Live2D Working State Animation...",
					State:      monitor.Running,
					PID:        100,
					Terminal:   "t1",
					ActivityAt: base,
					IsToday:    true,
				},
			},
		},
		{
			ProjectName: "official-knowledge",
			Sessions: []monitor.SessionItem{
				{
					ID:         "ok-1",
					Source:     monitor.SourceClaude,
					Title:      "帮我看一下目录树的接口。",
					State:      monitor.Unread,
					PID:        101,
					Terminal:   "t2",
					ActivityAt: base.Add(-5 * time.Minute),
					IsToday:    true,
				},
			},
		},
	}
}

func testTransitionApp(groups []monitor.ProjectGroup) *app {
	return &app{
		groups:             groups,
		hookInstalled:      true,
		codexHookInstalled: true,
		piHookInstalled:    true,
	}
}

func TestMoodTrackerContinuousMorphAndReset(t *testing.T) {
	var mt moodTracker
	t0 := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

	shown, anim := mt.eval("demo", t0, moodNeedsYou, false)
	if anim || shown[moodNeedsYou] != 1 {
		t.Fatalf("initial mood = (%v, %v), want exact moodNeedsYou", shown, anim)
	}

	// Switching target begins a smooth 240ms EaseInOut morph without resetting on identical timestamps.
	shown, anim = mt.eval("demo", t0, moodWorking, false)
	if !anim || shown[moodNeedsYou] != 1 || shown[moodWorking] != 0 {
		t.Fatalf("at t=0 start = (%v, %v), want animating 1->0 just started", shown, anim)
	}
	shownPass2, animPass2 := mt.eval("demo", t0, moodWorking, false)
	if shownPass2 != shown || animPass2 != anim {
		t.Fatalf("multi-pass at same timestamp mutated morph: %v vs %v", shownPass2, shown)
	}

	midShown, midAnim := mt.eval("demo", t0.Add(faceShift/2), moodWorking, false)
	if !midAnim || midShown[moodNeedsYou] <= 0.4 || midShown[moodWorking] <= 0.4 {
		t.Fatalf("midpoint morph = (%v, %v), want blended ~0.5/0.5", midShown, midAnim)
	}

	endShown, endAnim := mt.eval("demo", t0.Add(faceShift), moodWorking, false)
	if endAnim || endShown[moodWorking] != 1 || endShown[moodNeedsYou] != 0 {
		t.Fatalf("settled morph = (%v, %v), want exact moodWorking", endShown, endAnim)
	}

	// ReduceMotion settles instantaneously.
	rmShown, rmAnim := mt.eval("demo", t0.Add(faceShift+time.Millisecond), moodCaughtUp, true)
	if rmAnim || rmShown[moodCaughtUp] != 1 {
		t.Fatalf("reduce-motion morph = (%v, %v), want instant moodCaughtUp", rmShown, rmAnim)
	}

	// Idle gap longer than moodResetAfter snaps on reappearance.
	snapShown, snapAnim := mt.eval("demo", t0.Add(moodResetAfter+time.Second), moodNeedsYou, false)
	if snapAnim || snapShown[moodNeedsYou] != 1 {
		t.Fatalf("stale morph = (%v, %v), want clean reset", snapShown, snapAnim)
	}
}

func TestHeroCoordinatorFlightAndReversal(t *testing.T) {
	var hc heroCoordinator
	hc.seq = 1
	hc.committed = map[string]committedHeroObs{
		"demo": {
			bucket:  bucketNeeds,
			relRect: ui.Rect{X: 20, Y: 90, W: 34, H: 34},
			ground:  lightPalette.tileHot,
			hasRel:  true,
			seq:     1,
		},
	}

	buckets := map[string]todayBucket{"demo": bucketRest}
	hc.beginToday(int(layoutNarrow), 360, false, buckets)
	if !hc.isFlying("demo") {
		t.Fatal("expected hero flight when demo moved needs -> rest")
	}
	f := hc.flights["demo"]
	f.toRect = ui.Rect{X: 22, Y: 96, W: 26, H: 26}
	f.hasToRect = true
	f.destMood = moodWorking
	f.destGround = lightPalette.tile

	// Simulate first flight frame, then mid-flight frame, verifying size + color + reversal.
	t0 := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	f.start = t0
	uMid := ui.EaseOut(0.15)
	midRect := lerpRect(f.fromRect, f.toRect, uMid)
	midColor := f.fromColor.Mix(f.destGround, uMid)
	f.lastRect = midRect
	f.lastColor = midColor

	if midRect.W >= 34 || midRect.W <= 26 {
		t.Fatalf("mid-flight width = %v, want strictly between 34 and 26", midRect.W)
	}
	if midColor == f.fromColor || midColor == f.destGround {
		t.Fatalf("mid-flight color %v should blend %v -> %v", midColor, f.fromColor, f.destGround)
	}

	// Reverse direction mid-flight: must start from current lastRect and lastColor.
	hc.beginToday(int(layoutNarrow), 360, false, map[string]todayBucket{"demo": bucketNeeds})
	rev := hc.flights["demo"]
	if rev == nil || rev.toBucket != bucketNeeds {
		t.Fatalf("expected reversed flight to bucketNeeds, got %+v", rev)
	}
	if rev.fromRect != midRect || rev.fromColor != midColor {
		t.Fatalf("reversed flight started at (%+v, %v), want (%+v, %v)", rev.fromRect, rev.fromColor, midRect, midColor)
	}

	// Window width or layout resize must suppress hero flights cleanly.
	hc.beginToday(int(layoutRegular), 520, false, map[string]todayBucket{"demo": bucketNeeds})
	if hc.isFlying("demo") {
		t.Fatal("expected layout/window width resize to cancel active hero flight")
	}
}

func TestTransitionFig1ToFig2NarrowHeroAndLayout(t *testing.T) {
	base := time.Now()
	a := testTransitionApp(testFig1Groups(base))

	var seenFlights []ui.Rect
	var seenFlying bool
	view := func(c *ui.Context) {
		a.view(c)
		if f := a.hero.flights["fix_avatar_eyes_demo"]; f != nil && !f.done {
			seenFlying = true
			if f.lastRect.W > 0 {
				seenFlights = append(seenFlights, f.lastRect)
			}
		}
	}

	tt := ui.NewTester(view, 320, 640)
	tt.SetPreferences(ui.Preferences{}) // ReduceMotion: false
	tt.Frame()

	avatarBefore, ok := tt.Find("fix_avatar_eyes_demo")
	if !ok {
		t.Fatal("Fig 1 avatar label not found")
	}
	if avatarBefore.W != 34 {
		t.Fatalf("Fig 1 narrow needs avatar width = %v, want 34", avatarBefore.W)
	}

	// Optionally save visual PNG snapshots if SPARK_UX_DUMP_DIR is set.
	dumpDir := os.Getenv("SPARK_UX_DUMP_DIR")
	saveShot := func(name string) {
		if dumpDir == "" {
			return
		}
		_ = os.MkdirAll(dumpDir, 0o755)
		f, err := os.Create(filepath.Join(dumpDir, name))
		if err != nil {
			return
		}
		defer f.Close()
		_ = png.Encode(f, tt.Image())
	}
	saveShot("fig1_settled.png")

	// Transition to Fig 2: Waiting -> Running, Live2D row enters Recent.
	a.groups = testFig2Groups(base)
	for step, delay := range []time.Duration{
		20 * time.Millisecond,
		50 * time.Millisecond,
		70 * time.Millisecond,
		80 * time.Millisecond,
		80 * time.Millisecond,
	} {
		time.Sleep(delay)
		tt.Frame()
		if step == 1 {
			saveShot("fig1_to_fig2_mid.png")
		}
	}
	saveShot("fig2_settled.png")

	if !seenFlying {
		t.Fatal("expected hero flight when moving Fig 1 Needs -> Fig 2 Running")
	}
	if len(seenFlights) == 0 {
		t.Fatal("expected recorded hero flight frames")
	}
	if a.hero.isFlying("fix_avatar_eyes_demo") {
		t.Fatal("hero flight should have settled after >= 240ms")
	}

	avatarAfter, ok := tt.Find("fix_avatar_eyes_demo")
	if !ok {
		t.Fatal("Fig 2 avatar label not found")
	}
	if avatarAfter.W != 26 {
		t.Fatalf("Fig 2 narrow running avatar width = %v, want 26", avatarAfter.W)
	}
	if !tt.HasText("Live2D Working State Animation...") || !tt.HasText("帮我看一下目录树的接口。") {
		t.Fatalf("expected both recent rows after transition, got %v", tt.Texts())
	}
	// Verify settled geometry matches ReduceMotion layout exactly.
	aStill := testTransitionApp(testFig2Groups(base))
	ttStill := newViewTester(aStill, 320, 640)
	avatarStill, _ := ttStill.Find("fix_avatar_eyes_demo")
	if avatarAfter != avatarStill {
		t.Fatalf("animated Fig 2 settled avatar rect %+v != reduce-motion rect %+v", avatarAfter, avatarStill)
	}
}

func TestTransitionRegularGridProjectSlideSnoozeNoticeAndOverlays(t *testing.T) {
	base := time.Now()
	a := testTransitionApp([]monitor.ProjectGroup{
		{
			ProjectName: "alpha",
			Sessions: []monitor.SessionItem{{
				ID: "a1", Source: monitor.SourceClaude, Title: "Alpha task",
				State: monitor.Waiting, WaitReason: monitor.WaitPermission,
				PID: 10, Terminal: "t1", ActivityAt: base.Add(-time.Minute), IsToday: true,
			}},
		},
		{
			ProjectName: "beta",
			Sessions: []monitor.SessionItem{
				{
					ID: "b1", Source: monitor.SourceClaude, Title: "Beta task",
					State: monitor.Unread, PID: 11, Terminal: "t2",
					ActivityAt: base.Add(-3 * time.Minute), IsToday: true,
				},
				{
					ID: "b2", Source: monitor.SourcePi, Title: "Pi unjumpable",
					State: monitor.Completed, ActivityAt: base.Add(-4 * time.Minute), IsToday: true,
				},
			},
		},
		{
			ProjectName: "gamma",
			Sessions: []monitor.SessionItem{{
				ID: "g1", Source: monitor.SourceClaude, Title: "Gamma task",
				State: monitor.Completed, ActivityAt: base.Add(-26 * time.Hour), IsToday: false,
			}},
		},
	})
	a.piState = monitor.PiExtensionCurrent
	a.piInstalledAt = base
	a.piInstallID = "pi-1"

	tt := ui.NewTester(func(c *ui.Context) {
		a.view(c)
	}, 520, 640)
	tt.SetPreferences(ui.Preferences{})
	tt.Frame()

	clickCenter := func(r ui.Rect) {
		t.Helper()
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		tt.Move(cx, cy)
		tt.Press(cx, cy)
		tt.Release(cx, cy)
		tt.Frame()
	}

	// Verify clicking the avatar center inside needsStack opens the project without the inner avatar stealing pointer hits.
	alphaNeedsAvatar, ok := tt.Find("alpha")
	if !ok {
		t.Fatal("needsStack alpha avatar not found")
	}
	clickCenter(alphaNeedsAvatar)
	if a.project != "alpha" {
		t.Fatalf("clicking needsStack avatar center got project=%q, want alpha", a.project)
	}
	time.Sleep(290 * time.Millisecond)
	tt.Frame()
	if err := tt.Click("Back to Today"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(290 * time.Millisecond)
	tt.Frame()
	if a.project != "" {
		t.Fatalf("expected back on Today, got project=%q", a.project)
	}

	// Dismiss pi notice and ensure it collapses cleanly.
	if err := tt.Click("Dismiss"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(260 * time.Millisecond)
	tt.Frame()
	if tt.HasText("needs /reload") || tt.HasText("before they report") {
		t.Fatal("piSetupNotice should finish collapsing after 260ms")
	}

	// Regular mode Needs -> Rest triggers hero flight into smallTile (40 -> 30).
	a.groups[0].Sessions[0].State = monitor.Running
	a.groups[0].Sessions[0].WaitReason = 0
	tt.Frame()
	for range 4 {
		time.Sleep(75 * time.Millisecond)
		tt.Frame()
	}
	alphaTileAvatar, ok := tt.Find("alpha")
	if !ok || alphaTileAvatar.W != 30 {
		t.Fatalf("alpha regular grid avatar = (%+v, %v), want 30x30 tile avatar", alphaTileAvatar, ok)
	}
	if a.hero.isFlying("alpha") {
		t.Fatal("expected regular grid hero flight to settle cleanly")
	}

	// Verify Recent row hover crossfade ("3m" <-> "→") on Jumpable vs non-Jumpable rows, and "New" pill collapse.
	betaRowRect, ok := tt.Find("Beta task, Unread")
	if !ok {
		t.Fatal("Beta task row not found")
	}
	tt.Move(betaRowRect.X+betaRowRect.W/2, betaRowRect.Y+betaRowRect.H/2)
	tt.Frame()
	time.Sleep(55 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("→") || !tt.HasText("3m") {
		t.Fatalf("expected jumpable hover mid-crossfade to show both arrow and time, texts=%v", tt.Texts())
	}
	time.Sleep(115 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("→") || tt.HasText("3m") {
		t.Fatalf("expected settled hover to show arrow without 3m, texts=%v", tt.Texts())
	}
	piRowRect, ok := tt.Find("Pi unjumpable, Completed")
	if !ok {
		t.Fatal("Pi unjumpable row not found")
	}
	tt.Move(piRowRect.X+piRowRect.W/2, piRowRect.Y+piRowRect.H/2)
	tt.Frame()
	time.Sleep(160 * time.Millisecond)
	tt.Frame()
	if tt.HasText("→") {
		t.Fatalf("non-jumpable row hover must never show arrow, texts=%v", tt.Texts())
	}

	// Transition Unread -> Completed collapses the "New" pill smoothly then removes it.
	newBefore, ok := tt.Find("New")
	if !ok {
		t.Fatal("expected New pill before marking Completed")
	}
	timeBefore, ok := tt.Find("3m")
	if !ok {
		t.Fatal("expected 3m timestamp before marking Completed")
	}
	darkCount := func(r ui.Rect) int {
		img := tt.Image()
		b := img.Bounds()
		x0, y0 := max(b.Min.X, int(r.X)), max(b.Min.Y, int(r.Y))
		x1, y1 := min(b.Max.X, int(r.X+r.W)), min(b.Max.Y, int(r.Y+r.H))
		n := 0
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				cr, cg, cb, _ := img.At(x, y).RGBA()
				if (cr>>8) < 120 && (cg>>8) < 120 && (cb>>8) < 120 {
					n++
				}
			}
		}
		return n
	}
	pillBox := ui.Rect{X: newBefore.X - 6, Y: newBefore.Y - 4, W: newBefore.W + 12, H: newBefore.H + 8}
	pillDarkBefore := darkCount(pillBox)
	a.groups[1].Sessions[0].State = monitor.Completed
	tt.Frame()
	time.Sleep(55 * time.Millisecond)
	tt.Frame()
	timeMid, okMidTime := tt.Find("3m")
	if !okMidTime {
		t.Fatal("expected 3m timestamp visible mid-collapse")
	}
	time.Sleep(165 * time.Millisecond)
	tt.Frame()
	if tt.HasText("New") {
		t.Fatalf("expected New pill removed after collapse completes, texts=%v", tt.Texts())
	}
	timeEnd, okEndTime := tt.Find("3m")
	if !okEndTime {
		t.Fatal("expected 3m timestamp visible after collapse completes")
	}
	if pillDarkBefore == 0 {
		t.Fatal("expected dark pixels in New pill area before collapse")
	}
	if !(timeBefore.X < timeMid.X && timeMid.X < timeEnd.X) {
		t.Fatalf("expected New pill collapse to slide 3m smoothly rightward (before=%v mid=%v end=%v)", timeBefore.X, timeMid.X, timeEnd.X)
	}

	// Verify castTile clickability at its avatar center on All tab, then return.
	if err := tt.Click("All"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	time.Sleep(290 * time.Millisecond)
	tt.Frame()
	gammaCastAvatar, ok := tt.Find("gamma")
	if !ok || gammaCastAvatar.W != 36 {
		t.Fatalf("gamma castTile avatar = (%+v, %v), want 36x36 on All tab", gammaCastAvatar, ok)
	}
	clickCenter(gammaCastAvatar)
	if a.project != "gamma" {
		t.Fatalf("clicking castTile avatar center got project=%q, want gamma", a.project)
	}
	time.Sleep(290 * time.Millisecond)
	tt.Frame()
	if err := tt.Click("Back to All projects"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(290 * time.Millisecond)
	tt.Frame()
	if err := tt.Click("Today"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	time.Sleep(290 * time.Millisecond)
	tt.Frame()

	// Clicking the smallTile avatar center opens projectPage (horizontal push-slide), then open & cancel character editor (modal fade+rise).
	alphaTileAvatar, ok = tt.Find("alpha")
	if !ok {
		t.Fatal("smallTile alpha avatar not found")
	}
	clickCenter(alphaTileAvatar)
	time.Sleep(110 * time.Millisecond)
	tt.Frame()
	if dumpDir := os.Getenv("SPARK_UX_DUMP_DIR"); dumpDir != "" {
		_ = os.MkdirAll(dumpDir, 0o755)
		if f, err := os.Create(filepath.Join(dumpDir, "project_slide_mid.png")); err == nil {
			_ = png.Encode(f, tt.Image())
			_ = f.Close()
		}
	}
	{
		img := tt.Image()
		sx := float32(img.Bounds().Dx()) / 520
		sy := float32(img.Bounds().Dy()) / 640
		assertWhite := func(x, y float32, what string) {
			t.Helper()
			cr, cg, cb, _ := img.At(int(x*sx), int(y*sy)).RGBA()
			r8, g8, b8 := cr>>8, cg>>8, cb>>8
			if r8 < 252 || g8 < 252 || b8 < 252 {
				t.Fatalf("%s at (%v,%v) = rgb(%d,%d,%d), want seamless pure white matching titleBar (no gray scrim split)", what, x, y, r8, g8, b8)
			}
		}
		assertWhite(450, 20, "titleBar right")
		assertWhite(450, 60, "stage-project opaque header area")
		assertWhite(4, 49, "peeking stage-list margin under stage-scrim")
	}
	time.Sleep(190 * time.Millisecond)
	tt.Frame()
	if a.project != "alpha" || !tt.HasText("Back to Today") {
		t.Fatalf("expected project page open after smallTile avatar click, got project=%q texts=%v", a.project, tt.Texts())
	}

	rEdit, okEdit := tt.Find("Edit project character")
	if !okEdit {
		t.Fatal("Edit project character button not found")
	}
	clickCenter(rEdit)
	if a.editor == nil {
		t.Fatalf("expected character editor open after center click, texts=%v", tt.Texts())
	}
	time.Sleep(25 * time.Millisecond)
	tt.Frame()
	editorEarlyRect, ok := tt.Find("Custom character")
	if !ok {
		t.Fatal("expected Custom character title early in open animation")
	}
	time.Sleep(180 * time.Millisecond)
	tt.Frame()
	editorSettledRect, ok := tt.Find("Custom character")
	if !ok {
		t.Fatal("expected Custom character title after open settles")
	}
	if editorEarlyRect.Y <= editorSettledRect.Y {
		t.Fatalf("expected editor panel rise animation (early.Y=%v > settled.Y=%v)", editorEarlyRect.Y, editorSettledRect.Y)
	}

	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	// Model clears immediately on Cancel while closing preview animates down/out smoothly.
	if a.editor != nil {
		t.Fatal("a.editor must become nil immediately on Cancel")
	}
	time.Sleep(60 * time.Millisecond)
	tt.Frame()
	editorCloseMid, okCloseMid := tt.Find("Custom character")
	if !okCloseMid || editorCloseMid.Y <= editorSettledRect.Y {
		t.Fatalf("expected closing editor panel still fading/sliding down (ok=%v closeMid.Y=%v settled.Y=%v)", okCloseMid, editorCloseMid.Y, editorSettledRect.Y)
	}
	time.Sleep(160 * time.Millisecond)
	tt.Frame()
	if a.lastEditor != nil || tt.HasText("Custom character") {
		t.Fatal("expected lastEditor passive snapshot and title to clear once close animation finishes")
	}

	// Verify savedToast slides up on fade-in and slides down on fade-out.
	a.savedAt = time.Now()
	tt.Frame() // start toast-fade from 0 -> 1
	time.Sleep(25 * time.Millisecond)
	tt.Frame()
	toastEarly, ok := tt.Find("Character saved")
	if !ok {
		t.Fatal("expected Character saved toast early in fade-in")
	}
	time.Sleep(180 * time.Millisecond)
	tt.Frame()
	toastSettled, ok := tt.Find("Character saved")
	if !ok {
		t.Fatal("expected Character saved toast settled visible")
	}
	if toastEarly.Y <= toastSettled.Y {
		t.Fatalf("expected toast enter slide up (early.Y=%v > settled.Y=%v)", toastEarly.Y, toastSettled.Y)
	}
	a.savedAt = time.Now().Add(-toastSpan - time.Millisecond)
	tt.Frame()
	time.Sleep(60 * time.Millisecond)
	tt.Frame()
	toastOut, okOut := tt.Find("Character saved")
	if !okOut || toastOut.Y <= toastSettled.Y {
		t.Fatalf("expected closing toast still visible mid-fade and sliding down (ok=%v out.Y=%v settled.Y=%v)", okOut, toastOut.Y, toastSettled.Y)
	}
	time.Sleep(160 * time.Millisecond)
	tt.Frame()
	if tt.HasText("Character saved") {
		t.Fatalf("expected Character saved toast gone after fade-out, texts=%v", tt.Texts())
	}

	// Hover + press feedback check on Back button & pop animation.
	backRect, ok := tt.Find("Back to Today")
	if !ok {
		t.Fatal("Back to Today button not found")
	}
	tt.Move(backRect.X+backRect.W/2, backRect.Y+backRect.H/2)
	time.Sleep(160 * time.Millisecond)
	tt.Frame()
	tt.Press(backRect.X+backRect.W/2, backRect.Y+backRect.H/2)
	time.Sleep(100 * time.Millisecond)
	tt.Frame()
	tt.Release(backRect.X+backRect.W/2, backRect.Y+backRect.H/2)
	time.Sleep(290 * time.Millisecond)
	tt.Frame()
	if a.project != "" {
		t.Fatalf("expected Back click to pop projectPage, got project=%q", a.project)
	}
	if a.lastProject != "" {
		t.Fatalf("expected lastProject to clear once slide finishes, got %q", a.lastProject)
	}

	// Snooze Later on a waiting project moves Waiting card out into Sessions.
	a.groups[0].Sessions[0].State = monitor.Waiting
	a.groups[0].Sessions[0].WaitReason = monitor.WaitPermission
	a.project = "alpha"
	tt.Frame() // start project-slide
	time.Sleep(290 * time.Millisecond)
	tt.Frame() // settle slide-in so Later is onscreen
	if err := tt.Click("Later"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(260 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("Sessions") {
		t.Fatalf("expected snoozed waiting item under Sessions, texts=%v", tt.Texts())
	}

	// Empty state crossfade: clear all groups, then verify Today shows Everyone's resting.
	a.project = ""
	a.groups = nil
	tt.Frame()
	time.Sleep(220 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("Everyone’s resting.") {
		t.Fatalf("expected Today empty state after clearing groups, got %v", tt.Texts())
	}
}

func TestTransitionReduceMotionAndResizeSkipMotion(t *testing.T) {
	base := time.Now()
	a := testTransitionApp(testFig1Groups(base))
	tt := newViewTester(a, 360, 640) // ReduceMotion: true

	a.groups = testFig2Groups(base)
	tt.Frame()
	if a.hero.isFlying("fix_avatar_eyes_demo") {
		t.Fatal("ReduceMotion must never leave an active hero flight")
	}

	// Clicking project with ReduceMotion opens and closes without lastProject lag.
	if err := tt.Click("official-knowledge, 1 to read"); err != nil {
		t.Fatal(err)
	}
	if a.project != "official-knowledge" {
		t.Fatalf("expected instant project open, got %q", a.project)
	}
	if err := tt.Click("Back to Today"); err != nil {
		t.Fatal(err)
	}
	if a.project != "" || a.lastProject != "" {
		t.Fatalf("expected instant pop under ReduceMotion, got (%q, %q)", a.project, a.lastProject)
	}

	// Resizing narrow -> regular with animations enabled must cancel cross-layout hero flight.
	tt.SetPreferences(ui.Preferences{})
	a.groups = testFig1Groups(base)
	tt.SetSize(320, 640)
	a.groups = testFig2Groups(base)
	tt.SetSize(520, 640)
	a.groups = testFig2Groups(base)
	tt.Frame()
	if a.hero.isFlying("fix_avatar_eyes_demo") {
		t.Fatal("layout/window resize must not trigger cross-bucket hero flight")
	}
}
