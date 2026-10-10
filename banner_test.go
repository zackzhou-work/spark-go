package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zackzhou-work/spark-go/internal/monitor"
)

const chrome = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

// SPARK_BANNER=1 go test -run TestBanner . 重新生成 docs/banner.png：
// 先无窗口渲染 app 界面，再用无头 Chrome 把 docs/banner/index.html 排成横幅
func TestBanner(t *testing.T) {
	if os.Getenv("SPARK_BANNER") == "" {
		t.Skip("set SPARK_BANNER=1 to regenerate docs/banner.png")
	}
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "app.png"), renderDemoApp())
	copyFile(t, "docs/banner/index.html", filepath.Join(dir, "index.html"))
	copyFile(t, "resources/icon.png", filepath.Join(dir, "icon.png"))

	out, err := filepath.Abs("docs/banner.png")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(chrome, "--headless", "--hide-scrollbars", "--force-device-scale-factor=2",
		"--window-size=1280,640", "--screenshot="+out, "file://"+filepath.Join(dir, "index.html"))
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("chrome: %v\n%s", err, msg)
	}
}

// SPARK_ICON=1 go test -run TestIcon . 用无头 Chrome 把 docs/icon/index.html 渲染成 resources/icon.png
func TestIcon(t *testing.T) {
	if os.Getenv("SPARK_ICON") == "" {
		t.Skip("set SPARK_ICON=1 to regenerate resources/icon.png")
	}
	src, err := filepath.Abs("docs/icon/index.html")
	if err != nil {
		t.Fatal(err)
	}
	out, err := filepath.Abs("resources/icon.png")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(chrome, "--headless", "--hide-scrollbars", "--default-background-color=00000000",
		"--window-size=1024,1024", "--screenshot="+out, "file://"+src)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("chrome: %v\n%s", err, msg)
	}
}

// renderDemoApp 用虚构的会话渲染 Today 页，悬停在一行上露出跳转箭头
func renderDemoApp() *image.RGBA {
	a := demoApp()
	const scale = 2
	tt := ui.NewTester(a.view, 360, 640)
	tt.SetScale(scale)
	// 角色不眨眼、气泡不浮动，截到的是每种表情的静止姿态
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	r, _ := tt.Find("Upgrade the router to v7")
	tt.Move(r.X+40, r.Y+r.H/2)
	tt.Frame()

	// 红绿灯是系统画的，无窗口渲染里没有，照原位补上
	img := tt.Image()
	for i, c := range []color.RGBA{{0xFF, 0x5F, 0x57, 0xFF}, {0xFE, 0xBC, 0x2E, 0xFF}, {0x28, 0xC8, 0x40, 0xFF}} {
		fillCircle(img, (20+float64(i)*20)*scale, 24*scale, 6*scale, c)
	}
	return img
}

// demoApp 是一组虚构的会话，覆盖四种状态
func demoApp() *app {
	now := time.Now()
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	return &app{pinned: true, groups: []monitor.ProjectGroup{
		{ProjectName: "acme-web", Sessions: []monitor.SessionItem{
			{ID: "local_1", Title: "Add dark mode to the settings page", State: monitor.Running, ActivityAt: now, IsToday: true},
			{ID: "local_2", Title: "Fix flaky checkout integration test", State: monitor.Waiting, ActivityAt: ago(2 * time.Minute), IsToday: true},
			{ID: "local_3", Title: "Upgrade the router to v7", State: monitor.Unread, ActivityAt: ago(14 * time.Minute), IsToday: true},
			{ID: "local_8", Title: "Migrate to the new auth client", State: monitor.Completed, ActivityAt: ago(50 * time.Hour)},
		}},
		{ProjectName: "data-pipeline", Sessions: []monitor.SessionItem{
			{ID: "local_4", Title: "Backfill events for September", State: monitor.Running, ActivityAt: now, IsToday: true},
			{ID: "local_5", Title: "Profile the nightly aggregation job", State: monitor.Completed, ActivityAt: ago(time.Hour), IsToday: true},
		}},
		{ProjectName: "docs-site", Sessions: []monitor.SessionItem{
			{ID: "local_6", Title: "Rewrite the getting started guide", State: monitor.Unread, ActivityAt: ago(38 * time.Minute), IsToday: true},
			{ID: "local_7", Title: "Add search to the API reference", State: monitor.Completed, ActivityAt: ago(75 * time.Hour)},
		}},
		{ProjectName: "search-api", Sessions: []monitor.SessionItem{
			{ID: "local_9", Title: "Pick an embedding model", State: monitor.Waiting, WaitReason: monitor.WaitQuestion, ActivityAt: ago(8 * time.Minute), IsToday: true},
			{ID: "local_10", Title: "Trace the slow ranking query", State: monitor.Completed, ActivityAt: ago(26 * time.Hour)},
		}},
		{ProjectName: "infra", Sessions: []monitor.SessionItem{
			{ID: "local_11", Title: "Rotate the staging certificates", State: monitor.Completed, ActivityAt: ago(3 * time.Hour), IsToday: true},
		}},
	}}
}

// fillCircle 画一个边缘抗锯齿的实心圆
func fillCircle(img *image.RGBA, cx, cy, radius float64, c color.RGBA) {
	for y := int(cy - radius - 1); y <= int(cy+radius+1); y++ {
		for x := int(cx - radius - 1); x <= int(cx+radius+1); x++ {
			coverage := min(radius-math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)+0.5, 1)
			if coverage <= 0 {
				continue
			}
			bg := img.RGBAAt(x, y)
			mix := func(a, b uint8) uint8 { return uint8(float64(a)*(1-coverage) + float64(b)*coverage) }
			img.SetRGBA(x, y, color.RGBA{mix(bg.R, c.R), mix(bg.G, c.G), mix(bg.B, c.B), max(bg.A, uint8(255*coverage))})
		}
	}
}

func writePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
