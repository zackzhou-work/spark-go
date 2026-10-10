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
	type pair struct{ hue, shape int }
	seen := map[pair]bool{}
	for i := range 2000 {
		ch := characterOf(projectSeed(fmt.Sprintf("project-%d", i)))
		seen[pair{slices.Index(hues[:], ch.color), slices.IndexFunc(bodies[:], func(b body) bool { return b == ch.body })}] = true
	}
	if len(seen) != len(hues)*len(bodies) {
		t.Fatalf("project names should reach all %d pairings, reached %d", len(hues)*len(bodies), len(seen))
	}
	for h := range hues {
		for b := range bodies {
			if ch := characterOf(castSeed(h, b)); ch.color != hues[h] || ch.body != bodies[b] {
				t.Errorf("castSeed(%d, %d) should give hue %d with body %d", h, b, h, b)
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

// AVATAR_PNG=out.png go test -run TestAvatarGallery . 把预览窗口无窗口渲染成图片
func TestAvatarGallery(t *testing.T) {
	g := &avatarGallery{}
	tt := ui.NewTester(g.view, 560, 640)
	tt.SetScale(2)
	// 关掉待机小动作，截到的是每种表情的静止姿态
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

// 角标里的数字按字形的实际笔画居中，而不是按行框
func TestBadgeDigitIsCentered(t *testing.T) {
	for _, size := range []float32{64, 48, 34} {
		for _, count := range []int{1, 7, 12} {
			if dx, dy := badgeDigitOffset(size, count); dx < -0.5 || dx > 0.5 || dy < -0.5 || dy > 0.5 {
				t.Errorf("size %v count %d: digit is off center by (%.2f, %.2f) DIP", size, count, dx, dy)
			}
		}
	}
}

// badgeDigitOffset 渲染一个待读角色，返回角标里数字笔画中心相对黑色底中心的偏移
func badgeDigitOffset(size float32, count int) (dx, dy float64) {
	const pad, scale = 20, 2
	view := func(c *ui.Context) {
		ui.Box(c).Fill().Background(ui.Hex("#FFFFFF")).Padding(pad).Children(func() {
			avatarOf(c, 0, moodToRead, prop{count: count}, size)
		})
	}
	tt := ui.NewTester(view, 160, 160)
	tt.SetScale(scale)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	img := tt.Image()

	// 只看右下角，避开嘴巴和眼睛的黑色笔画：黑色是角标底，底里面的白色是数字
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
