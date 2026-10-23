package main

import "math"

// corner is one corner's elliptical radius, as a fraction of the body's width and height
type corner struct{ rx, ry float32 }

type point struct{ x, y float32 }

// body is one body shape: either a rounded rectangle, or a ready-made outline
type body struct {
	height float32 // body height inside a 64-wide frame, centred
	rotate float32 // degrees, negative is counter-clockwise
	radii  [4]corner
	// outline, when set, is drawn as-is; coordinates are offsets from the centre of the
	// 64px frame
	outline []point
	// faceY shifts the whole face down, giving the eyes room in shapes that narrow at the top
	faceY float32
}

func even(r float32) corner { return corner{r, r} }

// In order: circle, droplet, square, rice ball, cloud, capsule, star, heart
var bodies = [...]body{
	{height: 64, radii: [4]corner{even(.5), even(.5), even(.5), even(.5)}},
	{height: 64, rotate: -15, radii: [4]corner{even(.5), even(.5), even(.5), even(14.0 / 64)}},
	{height: 54, radii: [4]corner{{16.0 / 64, 16.0 / 54}, {16.0 / 64, 16.0 / 54}, {16.0 / 64, 16.0 / 54}, {16.0 / 64, 16.0 / 54}}},
	{outline: riceBall(), faceY: 7},
	{outline: cloud()},
	{height: 38, radii: [4]corner{{19.0 / 64, .5}, {19.0 / 64, .5}, {19.0 / 64, .5}, {19.0 / 64, .5}}},
	{outline: star(), faceY: 2},
	{outline: heart()},
}

// bodyIDs name the shapes in the saved choices; they outlive their positions in bodies, so a
// shape added in the middle doesn't move anyone's character. In bodies' order
var bodyIDs = [...]string{"circle", "droplet", "square", "rice-ball", "cloud", "capsule", "star", "heart"}

// bodyNames are the shapes as they are read out to assistive technology
var bodyNames = [...]string{"Circle", "Droplet", "Square", "Rice ball", "Cloud", "Capsule", "Star", "Heart"}

func bodyID(i int) string {
	if i < 0 || i >= len(bodyIDs) {
		return ""
	}
	return bodyIDs[i]
}

// bodyIndex is the shape a saved id names, or -1
func bodyIndex(id string) int {
	for i, s := range bodyIDs {
		if s == id {
			return i
		}
	}
	return -1
}

// riceBall puffs a small triangle outward: the edges stay straight, the corners become arcs,
// which comes out pointier than rounding the corners directly
func riceBall() []point {
	return fitted(inflated(regular(3, .8, .8), .32), 66)
}

// inflated pushes each edge of a polygon outward by r and replaces each corner with an arc of
// radius r around the original vertex. Vertices must run clockwise on screen
func inflated(v []point, r float64) []point {
	normal := func(a, b point) float64 { return math.Atan2(-float64(b.x-a.x), float64(b.y-a.y)) }
	var out []point
	n := len(v)
	for i, c := range v {
		a0, a1 := normal(v[(i-1+n)%n], c), normal(c, v[(i+1)%n])
		sweep := math.Remainder(a1-a0, 2*math.Pi)
		const arcSteps = 16
		for k := 0; k <= arcSteps; k++ {
			a := a0 + sweep*float64(k)/arcSteps
			out = append(out, point{c.x + float32(r*math.Cos(a)), c.y + float32(r*math.Sin(a))})
		}
	}
	return out
}

func star() []point {
	return fitted(filleted(regular(5, 1, .52), func(i int) float64 {
		if i%2 == 1 {
			return .1
		}
		return .18
	}), 68)
}

// regular is an upright regular polygon; when inner is less than outer an inner point is
// inserted between every two vertices, which draws a star
func regular(sides int, outer, inner float64) []point {
	var v []point
	steps := sides
	if inner < outer {
		steps *= 2
	}
	for i := range steps {
		a := -math.Pi/2 + float64(i)*2*math.Pi/float64(steps)
		r := outer
		if i%2 == 1 && inner < outer {
			r = inner
		}
		v = append(v, point{float32(math.Cos(a) * r), float32(math.Sin(a) * r)})
	}
	return v
}

// filleted replaces each corner of a polygon with an arc tangent to both edges, reflex
// corners included
func filleted(v []point, radius func(i int) float64) []point {
	var out []point
	n := len(v)
	for i := range v {
		p, c, q := v[(i-1+n)%n], v[i], v[(i+1)%n]
		d1x, d1y := unit(float64(p.x-c.x), float64(p.y-c.y))
		d2x, d2y := unit(float64(q.x-c.x), float64(q.y-c.y))
		half := math.Acos(d1x*d2x+d1y*d2y) / 2
		r := radius(i)
		bx, by := unit(d1x+d2x, d1y+d2y)
		ox, oy := float64(c.x)+bx*r/math.Sin(half), float64(c.y)+by*r/math.Sin(half)
		t := r / math.Tan(half)
		a0 := math.Atan2(float64(c.y)+d1y*t-oy, float64(c.x)+d1x*t-ox)
		a1 := math.Atan2(float64(c.y)+d2y*t-oy, float64(c.x)+d2x*t-ox)
		sweep := math.Remainder(a1-a0, 2*math.Pi)
		const arcSteps = 16
		for k := 0; k <= arcSteps; k++ {
			a := a0 + sweep*float64(k)/arcSteps
			out = append(out, point{float32(ox + r*math.Cos(a)), float32(oy + r*math.Sin(a))})
		}
	}
	return out
}

func unit(x, y float64) (float64, float64) {
	l := math.Hypot(x, y)
	return x / l, y / l
}

// cloud is the union of a few circles: three spread along the bottom, two bulging on top. Cast
// a ray out from the centre and take the farthest intersection as the outline
func cloud() []point {
	circles := [][3]float64{{-17, 8, 15}, {17, 8, 15}, {0, 10, 18}, {-8, -11, 15}, {10, -10, 14}}
	const rays = 240
	out := make([]point, rays)
	for i := range out {
		a := float64(i) / rays * 2 * math.Pi
		dx, dy := math.Cos(a), math.Sin(a)
		far := 0.0
		for _, c := range circles {
			b := dx*c[0] + dy*c[1]
			if d := b*b - (c[0]*c[0] + c[1]*c[1] - c[2]*c[2]); d >= 0 {
				far = max(far, b+math.Sqrt(d))
			}
		}
		out[i] = point{float32(dx * far), float32(dy * far)}
	}
	return fitted(out, 66)
}

// heart is the classic heart curve, with the bottom tip replaced by a curve tangent to both
// sides
func heart() []point {
	const samples = 240
	pts := make([]point, samples)
	for i := range pts {
		t := float64(i) / samples * 2 * math.Pi
		x := 16 * math.Pow(math.Sin(t), 3)
		y := -(13*math.Cos(t) - 5*math.Cos(2*t) - 2*math.Cos(3*t) - math.Cos(4*t))
		pts[i] = point{float32(x), float32(y)}
	}
	pts = fitted(pts, 68)

	const cut = 6
	tip := samples / 2
	from, to := tip, tip
	for pts[from].y > pts[tip].y-cut {
		from--
	}
	for pts[to].y > pts[tip].y-cut {
		to++
	}
	a, b := pts[from], pts[to]
	ax, ay := unit(float64(a.x-pts[from-1].x), float64(a.y-pts[from-1].y))
	bx, by := unit(float64(b.x-pts[to+1].x), float64(b.y-pts[to+1].y))
	reach := math.Hypot(float64(a.x-pts[tip].x), float64(a.y-pts[tip].y)) * .6

	out := append([]point{}, pts[:from+1]...)
	const curveSteps = 12
	for k := 1; k < curveSteps; k++ {
		t := float64(k) / curveSteps
		out = append(out, bezier(t,
			a, point{a.x + float32(ax*reach), a.y + float32(ay*reach)},
			point{b.x + float32(bx*reach), b.y + float32(by*reach)}, b))
	}
	return fitted(append(out, pts[to:]...), 68)
}

func bezier(t float64, p0, p1, p2, p3 point) point {
	u := 1 - t
	w0, w1, w2, w3 := float32(u*u*u), float32(3*u*u*t), float32(3*u*t*t), float32(t*t*t)
	return point{w0*p0.x + w1*p1.x + w2*p2.x + w3*p3.x, w0*p0.y + w1*p1.y + w2*p2.y + w3*p3.y}
}

// fitted scales an outline so the long side of its bounding box equals span, then centres the
// bounding box on the origin
func fitted(pts []point, span float32) []point {
	minX, minY := float32(math.Inf(1)), float32(math.Inf(1))
	maxX, maxY := float32(math.Inf(-1)), float32(math.Inf(-1))
	for _, p := range pts {
		minX, maxX, minY, maxY = min(minX, p.x), max(maxX, p.x), min(minY, p.y), max(maxY, p.y)
	}
	k := span / max(maxX-minX, maxY-minY)
	cx, cy := (minX+maxX)/2, (minY+maxY)/2
	out := make([]point, len(pts))
	for i, p := range pts {
		out[i] = point{(p.x - cx) * k, (p.y - cy) * k}
	}
	return out
}

// contourSamples is how many points a shape is sampled at. Two contours with the same count,
// winding and start point can be blended point by point, which is how one body becomes another
const contourSamples = 160

// contours holds every body as a closed outline in the 64-unit frame: contourSamples points,
// all running the same way round and all starting at the top, so index i of one is the same
// part of the body as index i of another
var contours = sampledContours()

func sampledContours() [][]point {
	out := make([][]point, len(bodies))
	for i, b := range bodies {
		out[i] = aligned(resampled(bodyOutline(b), contourSamples))
	}
	return out
}

// bodyOutline is b's edge as a polyline, its rotation included, without repeating the first
// point at the end
func bodyOutline(b body) []point {
	if len(b.outline) > 0 {
		return rotated(b.outline, b.rotate)
	}
	// A rounded rectangle walked clockwise from the top edge's right end. Each corner is a
	// quarter of an ellipse; the straight edges between them come out of the resampling
	const w, steps = float32(64), 16
	l, t, r, bottom := -w/2, -b.height/2, w/2, b.height/2
	el := func(c corner) (float32, float32) { return c.rx * w, c.ry * b.height }
	tlw, tlh := el(b.radii[0])
	trw, trh := el(b.radii[1])
	brw, brh := el(b.radii[2])
	blw, blh := el(b.radii[3])

	var pts []point
	arc := func(cx, cy, rx, ry float32, from, to float64) {
		for k := 0; k <= steps; k++ {
			a := from + (to-from)*float64(k)/steps
			pts = append(pts, point{cx + rx*float32(math.Cos(a)), cy + ry*float32(math.Sin(a))})
		}
	}
	arc(r-trw, t+trh, trw, trh, -math.Pi/2, 0)
	arc(r-brw, bottom-brh, brw, brh, 0, math.Pi/2)
	arc(l+blw, bottom-blh, blw, blh, math.Pi/2, math.Pi)
	arc(l+tlw, t+tlh, tlw, tlh, math.Pi, 3*math.Pi/2)
	return rotated(pts, b.rotate)
}

// rotated turns a polyline about the origin
func rotated(pts []point, degrees float32) []point {
	if degrees == 0 {
		return pts
	}
	cos, sin := math.Cos(float64(degrees)*math.Pi/180), math.Sin(float64(degrees)*math.Pi/180)
	out := make([]point, len(pts))
	for i, p := range pts {
		x, y := float64(p.x), float64(p.y)
		out[i] = point{float32(x*cos - y*sin), float32(x*sin + y*cos)}
	}
	return out
}

// resampled walks a closed polyline and picks n points the same distance apart, so shapes of
// different densities blend evenly
func resampled(pts []point, n int) []point {
	if len(pts) < 3 {
		return nil
	}
	lengths := make([]float64, len(pts))
	var total float64
	for i := range pts {
		a, b := pts[i], pts[(i+1)%len(pts)]
		lengths[i] = math.Hypot(float64(b.x-a.x), float64(b.y-a.y))
		total += lengths[i]
	}
	if total <= 0 {
		return nil
	}
	step := total / float64(n)
	out := make([]point, 0, n)
	at, walked := 0, 0.0
	for i := range n {
		want := float64(i) * step
		for at < len(pts)-1 && walked+lengths[at] < want {
			walked += lengths[at]
			at++
		}
		a, b := pts[at], pts[(at+1)%len(pts)]
		t := 0.0
		if lengths[at] > 0 {
			t = (want - walked) / lengths[at]
		}
		out = append(out, point{
			x: a.x + float32(t*float64(b.x-a.x)),
			y: a.y + float32(t*float64(b.y-a.y)),
		})
	}
	return out
}

// aligned turns a contour to run one way round and start it at the top, so every body's
// points pair up with every other's instead of twisting
func aligned(pts []point) []point {
	if len(pts) < 3 {
		return pts
	}
	// Signed area: negative and positive are the two directions, and only the sign matters
	var area float64
	for i := range pts {
		a, b := pts[i], pts[(i+1)%len(pts)]
		area += float64(a.x)*float64(b.y) - float64(b.x)*float64(a.y)
	}
	if area < 0 {
		for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
			pts[i], pts[j] = pts[j], pts[i]
		}
	}
	top := 0
	for i, p := range pts {
		if p.y < pts[top].y-0.01 || (abs32(p.y-pts[top].y) <= 0.01 && abs32(p.x) < abs32(pts[top].x)) {
			top = i
		}
	}
	out := make([]point, 0, len(pts))
	out = append(out, pts[top:]...)
	return append(out, pts[:top]...)
}

// morphed blends two contours; t of 0 is from, 1 is to
func morphed(from, to []point, t float32) []point {
	if len(from) != len(to) {
		return to
	}
	if t <= 0 {
		return from
	}
	if t >= 1 {
		return to
	}
	out := make([]point, len(from))
	for i := range from {
		out[i] = point{
			x: from[i].x + (to[i].x-from[i].x)*t,
			y: from[i].y + (to[i].y-from[i].y)*t,
		}
	}
	return out
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
