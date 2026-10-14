package main

import (
	"image"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zackzhou-work/spark-go/internal/monitor"
)

// emptyToday is the app with nothing started today, so Today rests. Both hooks are
// installed so the card is not sharing the page with the install banner
func emptyToday() *app {
	return &app{
		groups: []monitor.ProjectGroup{{ProjectName: "old", Sessions: []monitor.SessionItem{
			{ID: "local_1", Title: "Last week's work", State: monitor.Completed,
				ActivityAt: time.Now().Add(-72 * time.Hour)},
		}}},
		hookInstalled:      true,
		codexHookInstalled: true,
	}
}

// restingInk spans the empty state's characters. Only saturated pixels are counted: the
// card, the type and the grey character are all neutral, and the leftmost and rightmost
// characters are the yellow one and the brown one, so the span is the whole cast
func restingSpan(img *image.RGBA, scale int) (lo, hi int, ok bool) {
	lo, hi = 1<<30, -1
	b := img.Bounds()
	// below the title bar: the traffic lights are saturated too
	for y := 40 * scale; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if max(r, g, bl)-min(r, g, bl) > 0x4000 {
				lo, hi = min(lo, x), max(hi, x)
			}
		}
	}
	return lo, hi, hi >= lo
}

// The card holds its content 24pt in from its own edge, which is what the row has to
// respect. Each character is a fixed Size() box that cannot shrink, so the five of them
// have to overlap to fit: without the overlap they measure 207.4pt inside a card whose
// content box is only 186pt at the narrowest window
func TestRestingCastFitsTheCard(t *testing.T) {
	// the card sits one border plus one 12pt margin inside the window
	const cardInset = 13
	// and its own padding is 24
	const cardPad = 24
	// the five sizes, which is what the row measures with no overlap at all
	const boxRow = 200
	for _, w := range []int{minWidth, 280, 300, 360, 480} {
		tt := newViewTester(emptyToday(), w, 640)
		lo, hi, ok := restingSpan(tt.Image(), 1)
		if !ok {
			t.Fatalf("w=%d: no character pixels found at all", w)
		}
		// the design bar: every character inside the card's padding, not merely on the card
		left, right := cardInset+cardPad, w-1-cardInset-cardPad
		if lo < left || hi > right {
			t.Errorf("w=%d: the cast spans x %d..%d, outside the card's content box %d..%d",
				w, lo, hi, left, right)
		}
		// ...and they have to overlap. A row as wide as its own boxes means the overlap
		// stopped working, which is what let them run past the card in the first place
		if got, limit := float32(hi-lo+1), boxRow*restingScale(float32(w-2)); got >= limit {
			t.Errorf("w=%d: the cast spans %.0fpx, not narrower than its %.0fpx of boxes, so it is not overlapping",
				w, got, limit)
		}
	}
}

// The cast overlaps by restingOverlap and no more: 6pt is the most that still leaves every
// expression readable, so a row wider than the design measures is too tight. This also
// pins restingInk, which restingScale divides by: change restingCast and it has to move
func TestRestingCastMeasuresTheSameAsTheDesign(t *testing.T) {
	tt := newViewTester(emptyToday(), minWidth, 640)
	lo, hi, ok := restingSpan(tt.Image(), 1)
	if !ok {
		t.Fatal("no character pixels found")
	}
	if got := float32(hi - lo + 1); math.Abs(float64(got-restingInk)) > 2 {
		t.Errorf("the cast measures %.0fpt at the narrowest window; restingInk says %.1fpt, so it is stale",
			got, restingInk)
	}
}

// The card has to keep its ground even when the install banner is sharing the page. The
// banner's prose column holds MinWidth(0) and so gives way first: at 260-320pt it used to be
// squeezed to a character a line, grow to 460pt tall, and collapse the card, whose children
// then painted straight onto the bare window with nothing behind them
func TestRestingCardSurvivesTheHookBanner(t *testing.T) {
	// tileHot in the light palette, which is the card's ground
	const cardGround = 0xF4
	for _, w := range []int{minWidth, 280, 300, 308, 320, 360, 480} {
		a := emptyToday()
		// offer both hooks, which is the widest the banner ever gets and a fresh install
		a.hookInstalled, a.codexHookInstalled = false, false
		img := newViewTester(a, w, 640).Image()
		n := 0
		b := img.Bounds()
		for y := 0; y < b.Dy(); y++ {
			for x := 0; x < b.Dx(); x++ {
				r, g, bl, _ := img.At(x, y).RGBA()
				if uint8(r>>8) == cardGround && uint8(g>>8) == cardGround && uint8(bl>>8) == cardGround {
					n++
				}
			}
		}
		// the card is most of the window at every width here; a collapsed one paints a few
		// hundred pixels of rounded corner and nothing else
		if n < w*150 {
			t.Errorf("w=%d: only %dpx of card ground painted, so the banner has collapsed the card", w, n)
		}
	}
}

// SPARK_RESTING=1 go test -run TestRestingRender . writes the empty state at the narrowest
// and the default window, light and dark, into /tmp, to look at
func TestRestingRender(t *testing.T) {
	if os.Getenv("SPARK_RESTING") == "" {
		t.Skip("set SPARK_RESTING=1 to render the empty state to /tmp")
	}
	for _, w := range []int{minWidth, 360} {
		for _, dark := range []bool{false, true} {
			tt := ui.NewTester(emptyToday().view, w, 640)
			tt.SetScale(2)
			tt.SetDark(dark)
			tt.SetPreferences(ui.Preferences{ReduceMotion: true})
			tt.Frame()
			name := "resting-" + strconv.Itoa(w)
			if dark {
				name += "-dark"
			}
			path := filepath.Join(os.TempDir(), name+".png")
			writePNG(t, path, tt.Image())
			t.Logf("wrote %s", path)
		}
	}
}
