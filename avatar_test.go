package main

import (
	"fmt"
	"math"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zackzhou-work/spark-go/internal/monitor"
)

func TestProjectLookPicksMostUrgent(t *testing.T) {
	question := monitor.SessionItem{State: monitor.Waiting, WaitReason: monitor.WaitQuestion}
	permission := monitor.SessionItem{State: monitor.Waiting, WaitReason: monitor.WaitPermission}
	running := monitor.SessionItem{State: monitor.Running}
	unread := monitor.SessionItem{State: monitor.Unread}
	done := monitor.SessionItem{State: monitor.Completed}
	cases := []struct {
		sessions []monitor.SessionItem
		mood     mood
		prop     prop
	}{
		{[]monitor.SessionItem{done, running, question, permission}, moodNeedsYou, prop{glyph: "?"}},
		{[]monitor.SessionItem{permission}, moodNeedsYou, prop{glyph: "!"}},
		{[]monitor.SessionItem{unread, running, unread}, moodWorking, prop{count: 2}},
		{[]monitor.SessionItem{done, unread}, moodToRead, prop{count: 1}},
		{[]monitor.SessionItem{done}, moodCaughtUp, prop{}},
		{nil, moodCaughtUp, prop{}},
	}
	for _, tc := range cases {
		m, p := projectLook(monitor.ProjectGroup{Sessions: tc.sessions})
		if m != tc.mood || p != tc.prop {
			t.Errorf("projectLook(%v) = %v %+v, want %v %+v", tc.sessions, m, p, tc.mood, tc.prop)
		}
	}
}

func TestProjectKeepsItsCharacter(t *testing.T) {
	if projectSeed("spark-go") != projectSeed("spark-go") {
		t.Fatal("the same project should always get the same character")
	}
}

func TestColorAndShapeArePickedApart(t *testing.T) {
	seen := map[character]bool{}
	for i := range 4000 {
		seen[characterOf(projectSeed(fmt.Sprintf("project-%d", i)))] = true
	}
	if len(seen) != len(colours)*len(bodies) {
		t.Fatalf("project names should reach all %d pairings, reached %d", len(colours)*len(bodies), len(seen))
	}
}

func groupsNamed(names ...string) []monitor.ProjectGroup {
	groups := make([]monitor.ProjectGroup, len(names))
	for i, n := range names {
		groups[i] = monitor.ProjectGroup{ProjectName: n}
	}
	return groups
}

// clashing finds n project names that all hash to the same character
func clashing(n int) []string {
	byCharacter := map[character][]string{}
	for i := 0; ; i++ {
		name := fmt.Sprintf("project-%d", i)
		ch := characterOf(projectSeed(name))
		byCharacter[ch] = append(byCharacter[ch], name)
		if len(byCharacter[ch]) == n {
			return byCharacter[ch]
		}
	}
}

func TestCastKeepsUnclashedCharacters(t *testing.T) {
	names := []string{"spark-go", "joyspace-ai", "OpenPaper"}
	cast := castCharacters(groupsNamed(names...), characterSettings{})
	for _, n := range names {
		if want := characterOf(projectSeed(n)); cast[n] != want {
			t.Errorf("%s should keep its own character %v, got %v", n, want, cast[n])
		}
	}
}

func TestCastMovesClashesToUnusedHues(t *testing.T) {
	names := clashing(3)
	cast := castCharacters(groupsNamed(names...), characterSettings{})
	first := characterOf(projectSeed(names[0]))
	if cast[names[0]] != first {
		t.Errorf("the first name in order should keep its character")
	}
	used := map[int]bool{}
	for _, n := range names {
		if cast[n].shape != first.shape {
			t.Errorf("%s should keep its shape when moved, got %v", n, cast[n])
		}
		used[cast[n].hue] = true
	}
	if len(used) != len(names) {
		t.Errorf("clashing projects should spread over different colours, got %v", cast)
	}
}

func TestCastNeverRepeatsUntilFull(t *testing.T) {
	total := len(colours) * len(bodies)
	var names []string
	for i := range total {
		names = append(names, fmt.Sprintf("p%d", i))
	}
	cast := castCharacters(groupsNamed(names...), characterSettings{})
	seen := map[character]bool{}
	for _, ch := range cast {
		seen[ch] = true
	}
	if len(seen) != total {
		t.Fatalf("%d projects should get %d different characters, got %d", total, total, len(seen))
	}
}

func TestCastIgnoresGroupOrder(t *testing.T) {
	names := append(clashing(4), "spark-go", "infra")
	reversed := slices.Clone(names)
	slices.Reverse(reversed)
	a, b := castCharacters(groupsNamed(names...), characterSettings{}), castCharacters(groupsNamed(reversed...), characterSettings{})
	for _, n := range names {
		if a[n] != b[n] {
			t.Errorf("%s got %v and %v depending on group order", n, a[n], b[n])
		}
	}
}

// TestBodiesStayInsideTheFrame keeps the outlines from growing past the box the bubble and
// badge already spill out of
func TestBodiesStayInsideTheFrame(t *testing.T) {
	for i, b := range bodies {
		for _, p := range b.outline {
			if p.x < -36 || p.x > 36 || p.y < -36 || p.y > 36 {
				t.Errorf("body %d reaches (%.1f, %.1f), outside the 64px frame plus a little margin", i, p.x, p.y)
				break
			}
		}
	}
}

// TestGazeLooksTheWayItSays keeps the angles honest: yaw to the right moves the eyes right,
// pitch down moves them down, and a roll leans the body the way the eyes lean
func TestGazeLooksTheWayItSays(t *testing.T) {
	rest := gaze{}.eyes(10.5, 7.5, 9)
	mid := func(e [2]eyeOnFace) (float32, float32) { return (e[0].x + e[1].x) / 2, (e[0].y + e[1].y) / 2 }
	restX, restY := mid(rest)

	rx, _ := mid((gaze{yaw: 20}).eyes(10.5, 7.5, 9))
	if rx <= restX+1 {
		t.Errorf("yaw 20° left the eyes at x %.2f, want them right of %.2f", rx, restX)
	}
	if right := (gaze{yaw: 20}).eyes(10.5, 7.5, 9)[1]; right.rx >= rest[1].rx {
		t.Errorf("the eye turned toward the edge kept its width: %.2f vs %.2f", right.rx, rest[1].rx)
	}

	_, downY := mid((gaze{pitch: 20}).eyes(10.5, 7.5, 9))
	if downY <= restY+1 {
		t.Errorf("pitch 20° put the eyes at design y %.2f, want them below %.2f", downY, restY)
	}

	if lean := (gaze{roll: 10}).lean(); lean >= 0 {
		t.Errorf("a counter-clockwise roll leans the body by %.1f°, want the drawing's clockwise sense", lean)
	}
}

// TestEyesStayOnTheFace keeps the eyes from sliding off the body as the face turns
func TestEyesStayOnTheFace(t *testing.T) {
	for _, yaw := range []float32{-60, -40, -20, 0, 20, 40, 60} {
		for _, pitch := range []float32{-30, -15, 0, 15, 30} {
			for _, e := range (gaze{yaw: yaw, pitch: pitch}).eyes(11, 8, 10) {
				if float32(math.Abs(float64(e.x)))+e.rx > faceRadius {
					t.Fatalf("yaw %v pitch %v: eye reaches %.1f across, past the %d-unit face", yaw, pitch, float32(math.Abs(float64(e.x)))+e.rx, faceRadius)
				}
				if float32(math.Abs(float64(e.y)))+e.ry > faceRadius {
					t.Fatalf("yaw %v pitch %v: eye reaches %.1f down, past the %d-unit face", yaw, pitch, float32(math.Abs(float64(e.y)))+e.ry, faceRadius)
				}
			}
		}
	}
}

// AVATAR_POSES_PNG=out.png go test -run TestAvatarPoses . renders a sheet of gaze poses to an
// image, for checking which way the eyes actually go
func TestAvatarPoses(t *testing.T) {
	path := os.Getenv("AVATAR_POSES_PNG")
	if path == "" {
		t.Skip("set AVATAR_POSES_PNG to render the pose sheet")
	}

	type row struct {
		name string
		mood mood
		size float32
	}
	rows := []row{
		{"Working", moodWorking, 48},
		{"Needs you", moodNeedsYou, 48},
		{"All caught up", moodCaughtUp, 48},
		{"Working 26", moodWorking, 26},
		{"Needs you 26", moodNeedsYou, 26},
	}
	yaws := []float32{-30, -15, 0, 15, 30}

	const pad, colW, rowH = 16, 62, 62
	width := pad*2 + 110 + len(yaws)*colW
	height := pad*2 + 22 + len(rows)*rowH

	ch := character{hue: 7, shape: bodyIndex("square")}
	view := func(c *ui.Context) {
		ui.Box(c).Fill().Background(ui.Hex("#FFFFFF")).Padding(pad).Children(func() {
			ui.Row(c).Height(22).Gap(0).Children(func() {
				ui.Text(c, "").Width(110)
				for _, y := range yaws {
					ui.Text(c, fmt.Sprintf("yaw %+d", int(y))).Width(colW).FontSize(10).TextColor(ui.Hex("#888888"))
				}
			})
			for r, row := range rows {
				ui.Row(c).Height(rowH).Gap(0).Children(func() {
					ui.Text(c, row.name).Width(110).FontSize(10).TextColor(ui.Hex("#888888"))
					for col, yaw := range yaws {
						size := row.size
						g := gaze{yaw: yaw}
						ui.Row(c.Key(fmt.Sprint(r, "-", col))).Width(colW).Center().Children(func() {
							ui.Box(c).Size(size, size).Draw(func(pt *ui.Painter, box ui.Rect) {
								paintAvatar(pt, box, ch, pose{mood: row.mood, squash: 1, blink: 1, gaze: g}, prop{})
							})
						})
					}
				})
			}
		})
	}

	tt := ui.NewTester(view, width, height)
	tt.SetScale(2)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	writePNG(t, path, tt.Image())
}

// AVATAR_PNG=out.png go test -run TestAvatarGallery . renders the preview window to an image
// without opening a window
func TestAvatarGallery(t *testing.T) {
	g := &avatarGallery{}
	tt := ui.NewTester(g.view, 840, 760)
	tt.SetScale(2)
	// Idle motions off, so each expression is captured at rest
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	if !tt.HasText("All caught up") {
		t.Fatal("gallery did not render")
	}
	if err := tt.Click("Needs you"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(faceShift + 50*time.Millisecond)
	tt.Frame()
	if path := os.Getenv("AVATAR_PNG"); path != "" {
		writePNG(t, path, tt.Image())
	}
}

// The badge digit centres on the glyph's strokes, not the line box
func TestBadgeDigitIsCentered(t *testing.T) {
	for _, size := range []float32{64, 48, 34} {
		for _, count := range []int{1, 7, 12} {
			if dx, dy := badgeDigitOffset(size, count); dx < -0.5 || dx > 0.5 || dy < -0.5 || dy > 0.5 {
				t.Errorf("size %v count %d: digit is off center by (%.2f, %.2f) DIP", size, count, dx, dy)
			}
		}
	}
}

// badgeDigitOffset renders an unread character and returns the offset of the digit's stroke
// centre from the black badge's centre
func badgeDigitOffset(size float32, count int) (dx, dy float64) {
	const pad, scale = 20, 2
	view := func(c *ui.Context) {
		ui.Box(c).Fill().Background(ui.Hex("#FFFFFF")).Padding(pad).Children(func() {
			avatarOf(c, character{}, 0, moodToRead, prop{count: count}, size)
		})
	}
	tt := ui.NewTester(view, 160, 160)
	tt.SetScale(scale)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	img := tt.Image()

	// Look only at the lower right, avoiding the black strokes of the mouth and eyes: black
	// is the badge, and white inside it is the digit
	u := size / 64
	x0, y0 := int((pad+40*u)*scale), int((pad+44*u)*scale)
	dark, digit := [4]int{1 << 30, 1 << 30, -1, -1}, [4]int{1 << 30, 1 << 30, -1, -1}
	grow := func(b *[4]int, x, y int) {
		b[0], b[1], b[2], b[3] = min(b[0], x), min(b[1], y), max(b[2], x+1), max(b[3], y+1)
	}
	for y := y0; y < img.Bounds().Dy(); y++ {
		for x := x0; x < img.Bounds().Dx(); x++ {
			if p := img.RGBAAt(x, y); p.R < 60 && p.G < 60 && p.B < 60 {
				grow(&dark, x, y)
			}
		}
	}
	for y := dark[1] + 3; y < dark[3]-3; y++ {
		for x := dark[0] + 3; x < dark[2]-3; x++ {
			if p := img.RGBAAt(x, y); p.R > 160 {
				grow(&digit, x, y)
			}
		}
	}
	dx = float64(digit[0]+digit[2]-dark[0]-dark[2]) / (2 * scale)
	dy = float64(digit[1]+digit[3]-dark[1]-dark[3]) / (2 * scale)
	return dx, dy
}

// AVATAR_DRIFT_PNG=out.png go test -run TestAvatarDriftSheet . renders what each expression
// actually does over its cycle, at the sizes it is drawn at
func TestAvatarDriftSheet(t *testing.T) {
	path := os.Getenv("AVATAR_DRIFT_PNG")
	if path == "" {
		t.Skip("set AVATAR_DRIFT_PNG to render the drift sheet")
	}
	rows := []struct {
		name string
		mood mood
		size float32
	}{
		{"Working 40", moodWorking, 40},
		{"Needs you 40", moodNeedsYou, 40},
		{"To read 40", moodToRead, 40},
		{"Caught up 40", moodCaughtUp, 40},
		{"Working 26", moodWorking, 26},
		{"To read 26", moodToRead, 26},
	}
	const frames, pad, colW, rowH = 13, 16, 42, 52

	ch := character{hue: 7, shape: bodyIndex("square")}
	view := func(c *ui.Context) {
		ui.Box(c).Fill().Background(ui.Hex("#FFFFFF")).Padding(pad).Children(func() {
			ui.Row(c).Height(20).Gap(0).Children(func() {
				ui.Text(c, "").Width(96)
				for f := range frames {
					ui.Text(c, fmt.Sprintf("%.1fs", float64(f)*0.5)).Width(colW).FontSize(9).TextColor(ui.Hex("#888888"))
				}
			})
			for r, row := range rows {
				ui.Row(c).Height(rowH).Gap(0).Children(func() {
					ui.Text(c, row.name).Width(96).FontSize(9).TextColor(ui.Hex("#888888"))
					for f := range frames {
						g, breath, lift := drift(row.mood, 3, float64(f)*0.5)
						p := pose{mood: row.mood, squash: 1, blink: 1, gaze: g, breath: breath, lift: lift}
						ui.Row(c.Key(fmt.Sprint(r, "-", f))).Width(colW).Center().Children(func() {
							ui.Box(c).Size(row.size, row.size).Draw(func(pt *ui.Painter, box ui.Rect) {
								paintAvatar(pt, box, ch, p, prop{})
							})
						})
					}
				})
			}
		})
	}

	width := pad*2 + 96 + frames*colW
	height := pad*2 + 20 + len(rows)*rowH
	tt := ui.NewTester(view, width, height)
	tt.SetScale(2)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	writePNG(t, path, tt.Image())
}
