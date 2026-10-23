package main

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zackzhou-work/spark-go/internal/monitor"
)

const (
	// A new shape rolls into the next over morphSpan and a new colour crosses the body and
	// the candidates over colourSpan; the chosen shape's outline fades in over ringSpan and
	// the pupils reach the pointer over gazeSpan
	morphSpan  = 420 * time.Millisecond
	colourSpan = 300 * time.Millisecond
	ringSpan   = 180 * time.Millisecond
	gazeSpan   = 195 * time.Millisecond
	toastSpan  = 1800 * time.Millisecond

	// draftUnits is the frame the editor draws in: the bodies span 64 of its 88, which
	// leaves the room the preview's gaze and the candidates' outline need. It is not the
	// 64-unit frame the characters on the pages are drawn in
	draftUnits = 88

	// The outline around the chosen shape: its line sits ringGap away from the body's edge
	// and is ringLine wide. The dots keep their own, smaller ring
	ringGap, ringLine = 4.3, 1.1
	dotGap, dotLine   = 2.5, 1.5

	// The panel's own chrome, above and below the body it scrolls: the title row, the divider
	// and the footer with its buttons
	editorHeadPad, editorClose = 20, 26
	editorFootPad, editorPill  = 15, 34
	editorChrome               = editorHeadPad + editorClose + 1 + 2*editorFootPad + editorPill

	// editorMargin is the room the panel keeps from the window's edges and from its title bar
	editorMargin = 12

	// gazeReach is how far the pupils go, in draft units, once the pointer is as far from
	// the preview's centre as the preview is wide
	gazeReachX, gazeReachY = 2.5, 3.2
)

// editorMetrics is how big the panel's parts are. A window too short for the full one gets the
// compact one, which keeps the preview and a whole row of shapes in sight instead of cutting
// both: the rest scrolls either way
type editorMetrics struct {
	preview, stagePad      float32
	padX, blockGap, bottom float32
	shapeH, shapeGap       float32
	colourH, colourGap     float32
}

var (
	editorFull = editorMetrics{
		preview: 110, stagePad: 18, padX: 25, blockGap: 26, bottom: 26,
		shapeH: 53, shapeGap: 10, colourH: 40, colourGap: 13,
	}
	editorCompact = editorMetrics{
		preview: 84, stagePad: 12, padX: 20, blockGap: 20, bottom: 20,
		shapeH: 50, shapeGap: 8, colourH: 36, colourGap: 10,
	}
)

// height is the panel at this size: what it takes for the title, the whole body and the
// buttons, without scrolling
func (m editorMetrics) height() float32 {
	stage := 2*m.stagePad + m.preview
	shapes := 2*m.shapeH + m.shapeGap
	dots := 2*m.colourH + m.colourGap
	return stage + shapes + m.blockGap + dots + m.bottom + editorChrome
}

// fitEditor picks the profile for the room a window has between its title bar and its edges
func fitEditor(room float32) editorMetrics {
	if room < editorFull.height() {
		return editorCompact
	}
	return editorFull
}

// characterDraft is what the editor is showing: the project it was opened on, the character
// picked so far, and where each transition started. Saves never read it after the write
// begins; the project and the character are captured first
type characterDraft struct {
	project string
	choice  character
	// shapeFrom, faceFrom and shapeStarted are what showed when the shape last changed, so a
	// second choice carries on from there rather than jumping back to the last target
	shapeFrom    []point
	faceFrom     float32
	shapeStarted time.Time
	// colourFrom, lineFrom and colourStarted do the same for the body colour and the lines
	// drawn on it
	colourFrom    ui.Color
	lineFrom      float32
	colourStarted time.Time
	// gazeX, gazeY is where the pupils are headed, in draft units
	gazeX, gazeY float32
	// still is the desktop's reduce-motion preference, read from the context as it builds
	still bool
	// saving holds off a second save and the ways out while the file is being written; err
	// is what the last write reported
	saving bool
	err    string
}

// eased is the prototype's ease-out, 1-(1-t)^4: fast away, slowing to a stop
func eased(t float32) float32 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	u := 1 - t
	return 1 - u*u*u*u
}

// ran is how far a transition that started at start has gone by now; one that never started
// has finished
func ran(start time.Time, span time.Duration, now time.Time) float32 {
	if start.IsZero() || span <= 0 {
		return 1
	}
	return eased(float32(now.Sub(start)) / float32(span))
}

func (d *characterDraft) span(span time.Duration) time.Duration {
	if d.still {
		return 0
	}
	return span
}

// morph is the shape the preview shows now and where its face sits
func (d *characterDraft) morph(now time.Time) ([]point, float32) {
	t := ran(d.shapeStarted, d.span(morphSpan), now)
	face := bodies[d.choice.shape].faceY
	return morphed(d.shapeFrom, contours[d.choice.shape], t), d.faceFrom + (face-d.faceFrom)*t
}

// colour is the body colour the preview shows now
func (d *characterDraft) colour(now time.Time) ui.Color {
	return blend(d.colourFrom, colours[d.choice.hue].body, ran(d.colourStarted, d.span(colourSpan), now))
}

// line is how white the lines drawn on the body are now, which is how a colour change carries
// the face with it
func (d *characterDraft) line(now time.Time) float32 {
	want := faceInk(colours[d.choice.hue].body)
	return d.lineFrom + (want-d.lineFrom)*ran(d.colourStarted, d.span(colourSpan), now)
}

// moving reports whether anything is still on its way, which is when the preview asks for
// more frames
func (d *characterDraft) moving(now time.Time) bool {
	return ran(d.shapeStarted, d.span(morphSpan), now) < 1 || ran(d.colourStarted, d.span(colourSpan), now) < 1
}

func blend(from, to ui.Color, t float32) ui.Color {
	mix := func(a, b uint8) uint8 { return uint8(math.Round(float64(float32(a) + (float32(b)-float32(a))*t))) }
	return ui.RGB(mix(from.R, to.R), mix(from.G, to.G), mix(from.B, to.B))
}

func lit(on bool) float32 {
	if on {
		return 1
	}
	return 0
}

// character is what a project wears: the cast's pick, or the one its name hashes to before the
// cast is built
func (a *app) character(project string) character {
	if ch, ok := a.cast[project]; ok {
		return ch
	}
	return characterOf(projectSeed(project))
}

// openCharacterEditor starts a draft from the project's character as it stands, which may be
// one the automatic assignment chose
func (a *app) openCharacterEditor(project string) {
	ch := a.character(project)
	a.editor = &characterDraft{
		project:    project,
		choice:     ch,
		shapeFrom:  contours[ch.shape],
		faceFrom:   bodies[ch.shape].faceY,
		colourFrom: ch.color(),
		lineFrom:   faceInk(ch.color()),
	}
}

// closeCharacterEditor throws the draft away; nothing it held was ever saved
func (a *app) closeCharacterEditor() {
	a.editor = nil
}

func (a *app) chooseShape(shape int) {
	d := a.editor
	if d == nil || d.saving || d.choice.shape == shape {
		return
	}
	now := time.Now()
	d.shapeFrom, d.faceFrom = d.morph(now)
	d.choice.shape, d.shapeStarted = shape, now
}

func (a *app) chooseColour(hue int) {
	d := a.editor
	if d == nil || d.saving || d.choice.hue == hue {
		return
	}
	now := time.Now()
	d.colourFrom, d.lineFrom = d.colour(now), d.line(now)
	d.choice.hue, d.colourStarted = hue, now
}

// saveCharacterEditor writes the draft to the choices file. The candidate file is built here,
// on the UI thread, and written on a goroutine; the result comes back through the window's
// update, so the background never touches what the window is showing
func (a *app) saveCharacterEditor() {
	d := a.editor
	if d == nil || d.saving {
		return
	}
	if a.charactersErr != nil {
		// A file spark couldn't read is never written over: whatever is in it is all the user
		// has, and guessing at the rest of it would lose the rest
		return
	}
	project, path, next := d.project, a.charactersAt, a.characters.with(d.project, d.choice)
	d.saving, d.err = true, ""
	// The result lands on the UI thread: the window's update, or in place in a test, which has
	// no window to come back through
	done := func(err error) {
		if err != nil {
			if e := a.editor; e != nil && e.project == project {
				e.saving, e.err = false, "Couldn't save: "+err.Error()
			}
			return
		}
		a.characters = next
		a.savedAt = time.Now()
		if e := a.editor; e != nil && e.project == project {
			a.closeCharacterEditor()
		}
	}
	if a.win == nil {
		done(saveCharacterSettings(path, next))
		return
	}
	go func() {
		err := saveCharacterSettings(path, next)
		a.win.Update(func() { done(err) })
	}()
}

// characterButton is the project page's big avatar; the whole of it opens the character
// editor. The pointer resting on it lights its ground and puts the hint under it, clear of the
// count badge at its lower right
func (a *app) characterButton(c *ui.Context, p palette, g monitor.ProjectGroup, m mood, pr prop) {
	const size, avatarSize = float32(96), float32(72)
	btn := ui.ButtonBase(c).Size(size, size).Radius(22).Cursor(ui.CursorPointer).
		Label("Edit project character").Description("Edit character").Bind(&a.characterEntry)
	hint := btn.Animate("hint", lit(btn.Hovered() || btn.Focused()), rowHoverFade)
	if hint > 0 {
		btn.Background(p.fill.Alpha(hint))
	}
	btn.OnClick(func() { a.openCharacterEditor(g.ProjectName) })
	btn.Children(func() {
		ui.Box(c).Size(avatarSize, avatarSize).Center().Children(func() {
			a.avatar(c, g.ProjectName, m, dressed(pr, p, p.tileHot), avatarSize)
		})
		// The hint goes under the avatar, on a pill of its own so it reads over the name
		ui.Row(c).Absolute().Left(-size).Right(-size).Top(size + 2).Justify(ui.Center).PassThrough().
			Opacity(hint).Children(func() {
			ui.Row(c).Height(26).Padding(0, 11).Radius(8).Center().PassThrough().
				Background(p.bg).Shadow(0, 2, 8, 0, ui.Hex("#000000").Alpha(0.18)).Children(func() {
				ui.Text(c, "Edit character").FontSize(13).TextColor(p.ink)
			})
		})
	})
}

// characterEditor is the modal the project page's avatar opens: the draft as it would look,
// the shapes and colours to try, and Cancel and Save
func (a *app) characterEditor(c *ui.Context, p palette) {
	d := a.editor
	if d == nil {
		return
	}
	d.still = c.Preferences().ReduceMotion
	w, h := c.Size()
	width := min(float32(360), w-2*editorMargin)
	// The window's own buttons sit over its content at the top left, so the dialog keeps clear
	// of the title bar: a panel across the traffic lights reads as one control with them
	bar := max(c.TitleBar().Height, float32(titleBarHeight)) + editorMargin
	room := h - bar - editorMargin
	m := fitEditor(room)

	open := true
	ui.DialogBase(c, &open, func(back, panel ui.Element) {
		// The window behind is dimmed rather than removed: the character being replaced stays
		// in sight next to its draft, and the title bar keeps to itself
		back.Background(p.bg.Alpha(0.55)).Padding(bar, editorMargin, editorMargin, editorMargin)
		// A short window opens the panel at the room it has and scrolls the choices, so the
		// title, the buttons and the error stay in reach
		panel.Bind(&a.editorPanel).Column().Width(width).Height(max(min(m.height(), room), 160)).
			Radius(22).Border(1, p.line).Background(p.bg).Shadow(0, 20, 70, 0, ui.Hex("#000000").Alpha(0.16)).Clip()

		ui.Row(c).Padding(editorHeadPad, 24, 0, 24).Gap(8).Children(func() {
			ui.Text(c, "Custom character").Grow(1).FontSize(16).FontWeight(620).LetterSpacing(-0.4).TextColor(p.strong)
			close := ui.ButtonBase(c).Size(editorClose, editorClose).Radius(editorClose / 2).Label("Close").Disabled(d.saving)
			if close.Hovered() {
				close.Background(p.fill)
			}
			close.OnClick(func() { a.closeCharacterEditor() })
			close.Children(func() { ui.Icon(c, closeIcon).Size(13, 13).TextColor(p.sec) })
		})

		// A short window scrolls the character and the choices; the title, the ways out and
		// the error stay where they can be reached
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Children(func() {
				ui.Box(c).Padding(m.stagePad, m.padX, m.stagePad, m.padX).Center().Children(func() {
					a.draftPreview(c, d, m.preview)
				})
				ui.Column(c).Padding(0, m.padX, m.bottom, m.padX).Gap(m.blockGap).Children(func() {
					shapes := ui.Grid(c).Key("shapes").Columns(4).GapX(5).GapY(m.shapeGap).Role(ui.RoleRadioGroup)
					for i := range bodies {
						shapes.Children(func() { a.shapeCell(c, p, d, i, m) })
					}
					dots := ui.Grid(c).Key("colours").Columns(12).GapY(m.colourGap).Role(ui.RoleRadioGroup)
					for i := range colours {
						dots.Children(func() { a.colourCell(c, p, d, i, m) })
					}
				})
			})
		})

		ui.Box(c).Height(1).Background(p.line)
		ui.Row(c).Padding(editorFootPad, 24, editorFootPad, 24).Gap(9).Center().Children(func() {
			if msg := a.editorMessage(d); msg != "" {
				ui.Text(c, msg).Grow(1).FontSize(11).LineHeight(1.4).TextColor(p.ink)
			}
			cancel := editorButton(c, p, "Cancel", false, editorPill, 13).Disabled(d.saving)
			cancel.OnClick(func() { a.closeCharacterEditor() })
			save := editorButton(c, p, "Save", true, editorPill, 13).MinWidth(72).Disabled(d.saving || a.charactersErr != nil)
			save.OnClick(func() { a.saveCharacterEditor() })
		})
		a.updateGaze(c, panel, d)
	})

	// Escape and a press on the backdrop ask to close; while the file is being written the
	// dialog waits, so spark never says it cancelled and then saves anyway
	if !open && !d.saving {
		a.closeCharacterEditor()
	}
}

// editorMessage is the line beside the buttons: what a failed save said, or why saving is off
func (a *app) editorMessage(d *characterDraft) string {
	switch {
	case d.err != "":
		return d.err
	case a.charactersErr != nil:
		return "Couldn't read " + a.charactersAt + ". Saving stays off until it is fixed."
	}
	return ""
}

// draftPreview draws the character as it is being chosen, its eyes following the pointer
func (a *app) draftPreview(c *ui.Context, d *characterDraft, size float32) {
	preview := ui.Box(c).Size(size, size).Bind(&a.editorPreview).
		Label("Live preview: " + colours[d.choice.hue].name + " " + bodyNames[d.choice.shape])
	// The pupils trail the pointer rather than snapping to it; with reduce motion they are
	// where the pointer is or nowhere
	gx := preview.AnimateWith("gaze-x", d.gazeX, gazeSpan, ui.EaseOut)
	gy := preview.AnimateWith("gaze-y", d.gazeY, gazeSpan, ui.EaseOut)
	still := d.still
	preview.Draw(func(pt *ui.Painter, r ui.Rect) {
		now := pt.Now()
		paintDraft(pt, r, d, gx, gy, now)
		if !still && d.moving(now) {
			// The shape and the colour carry on between builds, so ask for the next frame
			pt.After(idleFrame)
		}
	})
}

// updateGaze turns the pointer into where the pupils should look: away from the middle of the
// preview, further the further away it is, and nowhere at all while it is above the character
// or outside the editor. The preview holds the last frame's box, which is all a target a frame
// behind needs
func (a *app) updateGaze(c *ui.Context, panelElement ui.Element, d *characterDraft) {
	x, y, over := panelElement.PointerPosition()
	panel, preview := a.editorPanel.Bounds(c), a.editorPreview.Bounds(c)
	if !over || panel.W <= 0 || preview.W <= 0 {
		d.gazeX, d.gazeY = 0, 0
		return
	}
	dx := panel.X + x - (preview.X + preview.W/2)
	dy := panel.Y + y - (preview.Y + preview.H/2)
	distance := float32(math.Hypot(float64(dx), float64(dy)))
	if dy < 0 || distance == 0 {
		d.gazeX, d.gazeY = 0, 0
		return
	}
	strength := min(1, distance/preview.W)
	gazeX, gazeY := dx/distance*gazeReachX*strength, dy/distance*gazeReachY*strength
	if gazeX != d.gazeX || gazeY != d.gazeY {
		// The preview was built with the last target, so the pupils need one more frame to
		// reach the one the pointer has left them
		d.gazeX, d.gazeY = gazeX, gazeY
		c.AnimationFrame()
	}
}

// shapeCell is one of the eight shapes, drawn plain: the candidates have no face, so what they
// show is the outline the character would wear
func (a *app) shapeCell(c *ui.Context, p palette, d *characterDraft, shape int, m editorMetrics) {
	// One choice of a group: the checked state is for assistive technology, the outline that
	// follows the shape is what shows the choice, and the focus brings a ground of its own so
	// the two never read as one
	cell := ui.Row(c.Key("shape:" + bodyID(shape))).Height(m.shapeH).Center().Radius(12).Role(ui.RoleRadio).
		Focusable().Cursor(ui.CursorPointer).Label(bodyNames[shape]).Checked(d.choice.shape == shape).FocusRing(false)
	if cell.Hovered() || cell.Focused() {
		cell.Background(p.fill)
	}
	// The hover lifts the shape a little and tips it; with reduce motion it stays put and the
	// ground alone says the pointer is on it
	lift := cell.AnimateWith("lift", lit(cell.Hovered() && !d.still), 200*time.Millisecond, ui.EaseOut)
	ring := cell.Animate("ring", lit(d.choice.shape == shape), ringSpan)
	cell.Draw(func(pt *ui.Painter, r ui.Rect) {
		paintShapeCandidate(pt, r, shape, d.colour(pt.Now()), ring, lift, p.bg, p.sec)
	})
	cell.OnClick(func() { a.chooseShape(shape) })
}

// colourCell is one of the eleven colours: the dot keeps its size, and only the ring and the
// tick say whether it is under the pointer or chosen
func (a *app) colourCell(c *ui.Context, p palette, d *characterDraft, hue int, m editorMetrics) {
	cell := ui.Box(c.Key("colour:" + colourID(hue))).ColumnSpan(2).Height(m.colourH).Center().Role(ui.RoleRadio).
		Focusable().Cursor(ui.CursorPointer).Label(colours[hue].name).Checked(d.choice.hue == hue).FocusRing(false)
	if hue == 6 {
		// The second row of five is centred under the six
		cell.ColumnStart(2)
	}
	chosen := d.choice.hue == hue
	// The pointer and the focus bring out the same ring: a dot never grows or moves, and the
	// tick is what says it is the chosen one
	ring := cell.Animate("ring", lit(chosen || cell.Hovered() || cell.Focused()), ringSpan)
	cell.Draw(func(pt *ui.Painter, r ui.Rect) {
		paintColourDot(pt, r, colours[hue], ring, chosen, p.sec)
	})
	cell.OnClick(func() { a.chooseColour(hue) })
}

// editorButton is a pill of the editor's own: unlike the page's buttons it takes the focus, so
// the dialog can be used without a pointer
func editorButton(c *ui.Context, p palette, label string, primary bool, height, size float32) ui.Element {
	btn := ui.ButtonBase(c).Height(height).Padding(0, height/2-2).Radius(height / 2).Label(label)
	bg, fg := p.fill, p.ink
	if primary {
		bg, fg = p.primary, p.onPrimary
	}
	if btn.Hovered() {
		bg = p.pillHover
		if primary {
			bg = p.primaryHover
		}
	}
	btn.Background(bg)
	btn.Children(func() {
		ui.Text(c, label).FontSize(size).FontWeight(510).TextColor(fg)
	})
	return btn
}

// contourPath turns a sampled outline into a closed path drawn at cx, cy, u px per unit
func contourPath(pts []point, cx, cy, u float32) ui.Path {
	var path ui.Path
	for i, q := range pts {
		x, y := cx+q.x*u, cy+q.y*u
		if i == 0 {
			path.MoveTo(x, y)
			continue
		}
		path.LineTo(x, y)
	}
	path.Close()
	return path
}

// paintShapeCandidate draws one shape wearing body, and the outline that follows its own edge
// when ring asks for it. The outline is the body's own contour stroked wide in grey, cut back
// by one in the panel's colour: what is left outside the body is a line ringGap away from it
func paintShapeCandidate(pt *ui.Painter, r ui.Rect, shape int, body ui.Color, ring, lift float32, ground, ringInk ui.Color) {
	size := min(47, r.W-4, r.H-6)
	u := size / draftUnits
	cx, cy := r.X+r.W/2, r.Y+r.H/2-2*lift
	pts := contours[shape]
	if lift > 0 {
		pts = rotated(pts, -5*lift)
	}
	path := contourPath(pts, cx, cy, u)
	if ring > 0 {
		k := size / 47
		pt.StrokePath(&path, 2*(ringGap+ringLine)*k, ringInk.Alpha(0.5*ring))
		pt.StrokePath(&path, 2*ringGap*k, ground)
	}
	pt.FillPath(&path, body)
}

// paintColourDot draws one colour: a disc, a ring while it is under the pointer or chosen, and
// a tick once it is chosen, which is what tells the two apart
func paintColourDot(pt *ui.Painter, r ui.Rect, c colour, ring float32, chosen bool, ringInk ui.Color) {
	dot := min(28, min(r.W, r.H)-2*(dotGap+dotLine))
	k := dot / 28
	radius := dot / 2
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	if ring > 0 {
		var path ui.Path
		path.Circle(cx, cy, radius+dotGap+dotLine/2)
		pt.StrokePath(&path, dotLine, ringInk.Alpha(ring))
	}
	pt.Fill(ui.Rect{X: cx - radius, Y: cy - radius, W: dot, H: dot}, c.body, radius)
	if !chosen {
		return
	}
	var tick ui.Path
	tick.MoveTo(cx-3.6*k, cy+0.2*k)
	tick.LineTo(cx-1.1*k, cy+2.9*k)
	tick.LineTo(cx+3.9*k, cy-3.2*k)
	pt.StrokePath(&tick, 1.7*k, c.tick())
}

// paintDraft draws the draft character: the body on its way to the chosen shape, its colour on
// its way to the chosen colour, and the face that keeps its eyes on the pointer
func paintDraft(pt *ui.Painter, r ui.Rect, d *characterDraft, gazeX, gazeY float32, now time.Time) {
	pts, faceY := d.morph(now)
	u := r.W / draftUnits
	cx, cy := r.X+r.W/2, r.Y+r.H/2

	// A shape lands with a short elastic squeeze and one blink, so the change is felt rather
	// than only seen
	squeezeX, squeezeY, blink := float32(1), float32(1), float32(1)
	if !d.still {
		t := math.Sin(math.Pi * float64(ran(d.shapeStarted, morphSpan, now)))
		squeezeX, squeezeY = 1+0.055*float32(t), 1-0.055*0.65*float32(t)
		blink = 1 - 0.75*float32(math.Pow(t, 8))
	}
	squeezed := make([]point, len(pts))
	for i, q := range pts {
		squeezed[i] = point{q.x * squeezeX, q.y * squeezeY}
	}
	body := contourPath(squeezed, cx, cy, u)
	pt.FillPath(&body, d.colour(now))

	face := draftFace{cx: cx, cy: cy, u: u, sx: squeezeX, sy: squeezeY, blink: blink, offset: faceY}
	paintDraftFace(pt, face, gazeX, gazeY, faceLine(d.line(now)))
}

// draftFace maps the preview's face from draft units to the screen: the blink and the body's
// squeeze both scale it, and its offset gives shapes that narrow at the top room for the eyes
type draftFace struct {
	cx, cy           float32
	u, sx, sy, blink float32
	offset           float32
}

func (f draftFace) at(x, y float32) (float32, float32) {
	y = y*f.blink + f.offset
	return f.cx + x*f.sx*f.u, f.cy + y*f.sy*f.u
}

func (f draftFace) ellipse(path *ui.Path, x, y, rx, ry float32) {
	ex, ey := f.at(x, y)
	addEllipse(path, ex, ey, rx*f.u*f.sx, ry*f.u*f.sy*f.blink)
}

// paintDraftFace is the editor's own expression: open eyes with a slight smile, so there is
// something to watch follow the pointer. line is the contrasting ink for the mouth; the eye
// whites stay white and the pupils stay dark, whichever body they are on
func paintDraftFace(pt *ui.Painter, face draftFace, gazeX, gazeY float32, line ui.Color) {
	// The pupils stay inside the whites: gazeReach of the whites' 7 across and 9 down
	gazeX, gazeY = max(-gazeReachX, min(gazeReachX, gazeX)), max(-gazeReachY, min(gazeReachY, gazeY))

	var whites, pupils ui.Path
	for _, side := range []float32{-1, 1} {
		face.ellipse(&whites, side*10, -3, 7, 9)
		face.ellipse(&pupils, side*10+gazeX, -3+gazeY, 2.8, 4.2)
	}
	pt.FillPath(&whites, eyeWhite)
	pt.FillPath(&pupils, eyeInk)

	// A quadratic smile, kept as a cubic so the face's scaling carries it
	x0, y0 := face.at(-4, 12)
	x1, y1 := face.at(-4/3.0, 14)
	x2, y2 := face.at(4/3.0, 14)
	x3, y3 := face.at(4, 12)
	var mouth ui.Path
	mouth.MoveTo(x0, y0)
	mouth.CubeTo(x1, y1, x2, y2, x3, y3)
	pt.StrokePath(&mouth, max(1, 1.6*face.u*face.sy*face.blink), line)
}

// addEllipse adds an axis-aligned ellipse to a path, with kappa's share of its radii as the
// control points
func addEllipse(path *ui.Path, cx, cy, rx, ry float32) {
	k := float32(kappa)
	path.MoveTo(cx+rx, cy)
	path.CubeTo(cx+rx, cy+ry*k, cx+rx*k, cy+ry, cx, cy+ry)
	path.CubeTo(cx-rx*k, cy+ry, cx-rx, cy+ry*k, cx-rx, cy)
	path.CubeTo(cx-rx, cy-ry*k, cx-rx*k, cy-ry, cx, cy-ry)
	path.CubeTo(cx+rx*k, cy-ry, cx+rx, cy-ry*k, cx+rx, cy)
	path.Close()
}
