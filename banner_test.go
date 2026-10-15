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

// SPARK_BANNER=1 go test -run TestBanner . regenerates docs/banner.png: render the app's
// interface without a window, then lay it out as a banner with headless Chrome and
// docs/banner/index.html
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

// SPARK_ICON=1 go test -run TestIcon . renders docs/icon/index.html into resources/icon.png
// with headless Chrome
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

// renderDemoApp renders the Today page with made-up sessions, hovering a row to reveal the
// jump arrow
func renderDemoApp() *image.RGBA {
	a := demoApp()
	const scale = 2
	tt := ui.NewTester(a.view, 360, 640)
	tt.SetScale(scale)
	// Characters don't blink and bubbles don't bob, so each expression is captured at rest
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	r, _ := tt.Find("Upgrade the router to v7")
	tt.Move(r.X+40, r.Y+r.H/2)
	tt.Frame()

	// The traffic lights are drawn by the system and absent from a windowless render, so put
	// them back in place
	img := tt.Image()
	for i, c := range []color.RGBA{{0xFF, 0x5F, 0x57, 0xFF}, {0xFE, 0xBC, 0x2E, 0xFF}, {0x28, 0xC8, 0x40, 0xFF}} {
		fillCircle(img, (20+float64(i)*20)*scale, 24*scale, 6*scale, c)
	}
	return img
}

// demoApp is a set of made-up sessions covering all four states, from both agents
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
		{ProjectName: "spark-cli", Sessions: []monitor.SessionItem{
			{ID: "01a12f69-fdb1-7173-b784-47189d9442d7", Source: monitor.SourceCodex, Title: "Port the release script to Go", State: monitor.Running, ActivityAt: now, IsToday: true},
			{ID: "01a12f6a-004f-7b00-a3db-568255912b84", Source: monitor.SourceCodex, Title: "Ship the 0.4 roadmap", State: monitor.Unread, ActivityAt: ago(21 * time.Minute), IsToday: true},
		}},
		// A pi project: one session waiting on a question in Herdr, so the card can go there,
		// and one started in a terminal spark can't open
		{ProjectName: "rag", Sessions: []monitor.SessionItem{
			{ID: "01a13ed4-da48-74ac-b607-dc2344b528b0", Source: monitor.SourcePi, Title: "Migrate the router to the new gateway", State: monitor.Waiting, WaitReason: monitor.WaitQuestion, Pane: "w4:pC", ActivityAt: ago(2 * time.Minute), IsToday: true},
			{ID: "01a13f06-5941-7138-a5d7-056c36fd2894", Source: monitor.SourcePi, Title: "Rebuild the ingest pipeline", State: monitor.Running, Pane: "w4:pF", ActivityAt: now, IsToday: true},
			{ID: "01a139f7-774a-705c-a47e-49deb8437f52", Source: monitor.SourcePi, Title: "Backfill events for September", State: monitor.Unread, ActivityAt: ago(38 * time.Minute), IsToday: true},
			{ID: "01a13919-4037-72b8-8b7e-c47d3459020f", Source: monitor.SourcePi, Title: "Trace the slow ranking query", State: monitor.Completed, NoSignal: true, ActivityAt: ago(11 * time.Hour), IsToday: true},
		}},
		{ProjectName: "togo", Sessions: []monitor.SessionItem{
			{ID: "01a13022-edf8-7755-9563-b28f921ada85", Source: monitor.SourcePi, Title: "Pick an embedding model", State: monitor.Waiting, WaitReason: monitor.WaitQuestion, ActivityAt: ago(4 * time.Minute), IsToday: true},
		}},
	}}
}

// fillCircle draws a filled circle with an anti-aliased edge
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
