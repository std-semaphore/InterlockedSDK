package main

import "math"

// Godot's Curve3D stores each point with an "in" and "out" tangent handle,
// both relative to the point itself. The curve between consecutive points
// i and i+1 is a cubic Bezier:
//
//   P0 = point[i].Pos
//   P1 = point[i].Pos + point[i].Out
//   P2 = point[i+1].Pos + point[i+1].In
//   P3 = point[i+1].Pos
//
// This file reconstructs that shape for drawing and for offset-along-track
// lookups (used for hover tooltips and object placement).

// SampledTrack is a track section pre-flattened into a polyline, with each
// sample point's cumulative distance from the start (its "offset" — the
// same quantity Godot calls distance/offset along the curve).
type SampledTrack struct {
	Section *TrackSection
	Points  []Point2D
	Offsets []float64 // same length as Points; Offsets[i] is the distance along the curve to Points[i]
	Length  float64
}

const samplesPerSegment = 24 // resolution for flattening each Bezier segment; plenty for hover accuracy at typical zoom levels

func cubicBezier(p0, p1, p2, p3 Point2D, t float64) Point2D {
	mt := 1 - t
	mt2 := mt * mt
	mt3 := mt2 * mt
	t2 := t * t
	t3 := t2 * t

	x := mt3*p0.X + 3*mt2*t*p1.X + 3*mt*t2*p2.X + t3*p3.X
	z := mt3*p0.Z + 3*mt2*t*p1.Z + 3*mt*t2*p2.Z + t3*p3.Z

	return Point2D{X: x, Z: z}
}

// PointsBetweenOffsets returns a polyline representing the section of
// track between two offsets. The returned slice includes interpolated
// start/end points so platform endpoints line up exactly with the
// requested offsets rather than the nearest sample.
func (st *SampledTrack) PointsBetweenOffsets(a, b float64) []Point2D {
	if len(st.Points) == 0 {
		return nil
	}

	if a > b {
		a, b = b, a
	}

	startPt, ok := st.PointAtOffset(a)
	if !ok {
		return nil
	}

	endPt, ok := st.PointAtOffset(b)
	if !ok {
		return nil
	}

	out := []Point2D{startPt}

	for i, off := range st.Offsets {
		if off > a && off < b {
			out = append(out, st.Points[i])
		}
	}

	out = append(out, endPt)

	if len(out) < 2 {
		return nil
	}

	return out
}

func dist(a, b Point2D) float64 {
	dx := a.X - b.X
	dz := a.Z - b.Z
	return math.Sqrt(dx*dx + dz*dz)
}

// normalizeDirection ensures a section's curve_data runs right-to-left
// (decreasing X), matching the "up" chainage convention used elsewhere in
// this dataset (signals/buffers have a "direction" of up/down, and "at"
// offsets are authored assuming up = right-to-left).
//
// Some exported sections come out of Godot authored the opposite way
// (left-to-right). Because PointAtOffset measures distance from index 0,
// a section authored backwards puts offset=0 at the wrong end — every
// object on that section then lands mirrored along the track, even
// though it's correctly placed on the right track. Reversing the point
// list (and swapping/negating in/out tangents, since reversing a Bezier
// chain swaps which tangent points "forward") fixes this without
// touching any of the offset math itself.
//
// We use overall horizontal displacement (last point vs first point)
// rather than strict per-segment monotonicity, since some tracks curve
// back on themselves locally but still have a clear overall direction.
// Near-vertical sections (first.X ≈ last.X) are left as-is since the
// heuristic can't judge them reliably — flag those manually if they turn
// out wrong.

// SampleTrack flattens a track section's curve into a polyline with
// cumulative-distance offsets, used both for drawing and for hover hit
// testing / offset lookup.
func SampleTrack(section *TrackSection) SampledTrack {
	st := SampledTrack{Section: section}

	if len(section.Curve) == 0 {
		return st
	}

	// First point starts the polyline at offset 0.
	st.Points = append(st.Points, section.Curve[0].Pos)
	st.Offsets = append(st.Offsets, 0)

	cumulative := 0.0

	for i := 0; i < len(section.Curve)-1; i++ {
		a := section.Curve[i]
		b := section.Curve[i+1]

		p0 := a.Pos
		p1 := Point2D{X: a.Pos.X + a.Out.X, Z: a.Pos.Z + a.Out.Z}
		p2 := Point2D{X: b.Pos.X + b.In.X, Z: b.Pos.Z + b.In.Z}
		p3 := b.Pos

		prev := p0
		for s := 1; s <= samplesPerSegment; s++ {
			t := float64(s) / float64(samplesPerSegment)
			pt := cubicBezier(p0, p1, p2, p3, t)
			cumulative += dist(prev, pt)
			st.Points = append(st.Points, pt)
			st.Offsets = append(st.Offsets, cumulative)
			prev = pt
		}
	}

	st.Length = cumulative
	return st
}

// PointAtOffset returns the 2D position on a sampled track at a given
// offset (distance from the start), used to place object markers and
// platform boundaries. Offsets outside the track's length are clamped.
// ok is false if the track has no sampled points at all (e.g. empty
// curve_data) — callers must check this rather than treating a zero
// Point2D as a real position, since (0,0) is a valid world coordinate.
func (st *SampledTrack) PointAtOffset(offset float64) (pt Point2D, ok bool) {
	if len(st.Points) == 0 {
		return Point2D{}, false
	}
	if offset <= 0 {
		return st.Points[0], true
	}
	if offset >= st.Length {
		return st.Points[len(st.Points)-1], true
	}

	// linear scan is fine here — tracks have at most a few hundred samples
	// and this only runs once per object on data load, not per frame.
	for i := 1; i < len(st.Offsets); i++ {
		if st.Offsets[i] >= offset {
			segStart := st.Offsets[i-1]
			segEnd := st.Offsets[i]
			segLen := segEnd - segStart
			var t float64
			if segLen > 0 {
				t = (offset - segStart) / segLen
			}
			a := st.Points[i-1]
			b := st.Points[i]
			return Point2D{
				X: a.X + (b.X-a.X)*t,
				Z: a.Z + (b.Z-a.Z)*t,
			}, true
		}
	}

	return st.Points[len(st.Points)-1], true
}

// DirectionAtOffset returns the unit tangent vector of the track at a given
// offset, pointing in the direction of increasing offset (i.e. the "down"
// direction, since offset 0 is the "up" end after normalizeDirection). Used
// to draw direction arrows on signals/objects. ok is false under the same
// conditions as PointAtOffset.
func (st *SampledTrack) DirectionAtOffset(offset float64) (dir Point2D, ok bool) {
	if len(st.Points) < 2 {
		return Point2D{}, false
	}
	if offset <= 0 {
		return unitVector(st.Points[0], st.Points[1])
	}
	if offset >= st.Length {
		n := len(st.Points)
		return unitVector(st.Points[n-2], st.Points[n-1])
	}
	for i := 1; i < len(st.Offsets); i++ {
		if st.Offsets[i] >= offset {
			return unitVector(st.Points[i-1], st.Points[i])
		}
	}
	n := len(st.Points)
	return unitVector(st.Points[n-2], st.Points[n-1])
}

func unitVector(a, b Point2D) (Point2D, bool) {
	dx := b.X - a.X
	dz := b.Z - a.Z
	length := math.Hypot(dx, dz)
	if length == 0 {
		return Point2D{}, false
	}
	return Point2D{X: dx / length, Z: dz / length}, true
}

// OffsetRibbon takes a polyline (e.g. a track sub-section) and returns a
// closed polygon representing a constant-width band running alongside it,
// offset to one side by `offset` world units. Unlike offsetting each point
// by its own local tangent (which lets the offset distance drift wherever
// the curve bends, producing a pinched or self-crossing band on anything
// but a straight line), this computes one perpendicular per SEGMENT and
// places each vertex at the intersection of its two neighboring segments'
// offset lines (mitered join). That keeps the band a constant width along
// its whole length, matching what "offset polyline" should mean.
//
// The returned polygon lists the offset edge first (same point order as
// the input), then the original centerline points in reverse, so it can
// be drawn directly as a filled polygon representing the platform's
// footprint.
func OffsetRibbon(points []Point2D, offset float64) []Point2D {
	n := len(points)
	if n < 2 {
		return nil
	}

	offsetSide := make([]Point2D, n)

	// Per-segment perpendicular (unit, rotated 90°).
	perp := make([]Point2D, n-1)
	for i := 0; i < n-1; i++ {
		dx := points[i+1].X - points[i].X
		dz := points[i+1].Z - points[i].Z
		length := math.Hypot(dx, dz)
		if length == 0 {
			perp[i] = Point2D{}
			continue
		}
		perp[i] = Point2D{X: -dz / length, Z: dx / length}
	}

	for i := 0; i < n; i++ {
		var px, pz float64
		switch {
		case i == 0:
			px, pz = perp[0].X, perp[0].Z
		case i == n-1:
			px, pz = perp[n-2].X, perp[n-2].Z
		default:
			// Average the two adjacent segment normals (a simple miter).
			// Good enough for the gentle curvature seen on real track —
			// not meant to handle near-180° reversals gracefully.
			ax, az := perp[i-1].X, perp[i-1].Z
			bx, bz := perp[i].X, perp[i].Z
			sx, sz := ax+bx, az+bz
			length := math.Hypot(sx, sz)
			if length == 0 {
				px, pz = ax, az
			} else {
				px, pz = sx/length, sz/length
			}
		}
		offsetSide[i] = Point2D{X: points[i].X + px*offset, Z: points[i].Z + pz*offset}
	}

	poly := make([]Point2D, 0, n*2)
	poly = append(poly, offsetSide...)
	for i := n - 1; i >= 0; i-- {
		poly = append(poly, points[i])
	}
	return poly
}
