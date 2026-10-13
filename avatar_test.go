package main

import (
	"fmt"
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
	if len(seen) != len(hues)*len(bodies) {
		t.Fatalf("project names should reach all %d pairings, reached %d", len(hues)*len(bodies), len(seen))
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
	cast := castCharacters(groupsNamed(names...))
	for _, n := range names {
		if want := characterOf(projectSeed(n)); cast[n] != want {
			t.Errorf("%s should keep its own character %v, got %v", n, want, cast[n])
		}
	}
}

func TestCastMovesClashesToUnusedHues(t *testing.T) {
	names := clashing(3)
	cast := castCharacters(groupsNamed(names...))
	first := characterOf(projectSeed(names[0]))
	if cast[names[0]] != first {
		t.Errorf("the first name in order should keep its character")
	}
	hues := map[int]bool{}
	for _, n := range names {
		if cast[n].shape != first.shape {
			t.Errorf("%s should keep its shape when moved, got %v", n, cast[n])
		}
		hues[cast[n].hue] = true
	}
	if len(hues) != len(names) {
		t.Errorf("clashing projects should spread over different hues, got %v", cast)
	}
}

func TestCastNeverRepeatsUntilFull(t *testing.T) {
	total := len(hues) * len(bodies)
	var names []string
	for i := range total {
		names = append(names, fmt.Sprintf("p%d", i))
	}
	cast := castCharacters(groupsNamed(names...))
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
	a, b := castCharacters(groupsNamed(names...)), castCharacters(groupsNamed(reversed...))
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

func TestGlanceStaysInsideTheEye(t *testing.T) {
	const base = 3.5
	for ms := int64(0); ms < 4000; ms += 10 {
		dx, _ := glance(ms)
		if x := base + dx; x < -3.5 || x > 3.5 {
			t.Fatalf("pupil at %v leaves the eye at %dms", x, ms)
		}
	}
}

// AVATAR_PNG=out.png go test -run TestAvatarGallery . renders the preview window to an image
// without opening a window
func TestAvatarGallery(t *testing.T) {
	g := &avatarGallery{}
	tt := ui.NewTester(g.view, 840, 960)
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
