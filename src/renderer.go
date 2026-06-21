package main

import (
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// mapRenderer draws MapWidget's content. Fyne canvas objects are retained
// (not immediate-mode).
//
// Static content is split into two tiers for performance:
//
//   - "world geometry" (track lines, platform footprints): allocated ONCE,
//     only when widget.dataDirty is set (i.e. on SetData). Pan/zoom never
//     reallocates these — they're just repositioned in place via
//     repositionWorldGeometry (no allocation). There are no always-on
//     text labels any more — every name (track/object/platform) is
//     hover-only, drawn fresh each frame as part of the small overlay
//     layer below. That was the single biggest source of visual clutter
//     in the old version.
//   - "object markers + direction arrows": cluster membership is
//     screen-space and can genuinely change with zoom, so these are
//     reallocated whenever the zoom level or view size changes. On a
//     PURE pan (offset changed, zoom/size unchanged), clustering can't
//     have changed, so markers are just shifted by the pan delta instead
//     of being torn down and rebuilt — this was the last big per-frame
//     allocation source during dragging.
type mapRenderer struct {
	widget *MapWidget

	bg            *canvas.Rectangle
	hint          *canvas.Text
	tooltipBg     *canvas.Rectangle
	tooltipTitle  *canvas.Text
	tooltipDetail *canvas.Text

	// World-anchored static geometry. Allocated once on dataDirty,
	// repositioned (never reallocated) on every geometryDirty.
	worldLines []worldLine
	platforms  []worldPolygon

	// Object markers + direction arrows (circles/lines), reallocated on
	// zoom/resize/data changes, shifted (not reallocated) on a pure pan.
	markers []fyne.CanvasObject

	hoverOverlay []fyne.CanvasObject // highlight ring + measurement line/label, rebuilt every frame (cheap)

	builtForSize fyne.Size // last size we built/repositioned geometry at

	// Tracked so we can tell a pure pan (cheap: just shift markers) apart
	// from a zoom/resize (needs an actual cluster recompute).
	markerStateValid bool
	lastOffsetX      float64
	lastOffsetZ      float64
	lastZoom         float64
	lastMarkerSize   fyne.Size
}

// worldLine is a single drawn track segment plus its world-space
// endpoints, so it can be repositioned on camera change without
// reallocating the underlying canvas.Line.
type worldLine struct {
	obj    *canvas.Line
	p1, p2 Point2D
}

// worldPolygon is a platform footprint drawn as a chain of solid thick
// strokes from the offset edge to the centerline. Each stroke spans the
// full width of the platform band, so together they tile into a solid
// filled rectangle. One slice is enough — no separate edge lines needed.
type worldPolygon struct {
	segs []*canvas.Line // solid strokes spanning offset-edge → centerline
	pd   *platformDrawable
	// footprintIdx selects which footprint within pd.footprints this
	// polygon covers (for multi-section platforms, one worldPolygon per
	// section).
	footprintIdx int
}

func (r *mapRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	r.hint.Resize(size)
	r.hint.Move(fyne.NewPos(0, size.Height/2-10))

	if size != r.builtForSize {
		r.widget.geometryDirty = true
	}
}

func (r *mapRenderer) MinSize() fyne.Size {
	return fyne.NewSize(400, 300)
}

func (r *mapRenderer) Refresh() {
	if !r.widget.HasData() {
		r.worldLines = nil
		r.platforms = nil
		r.markers = nil
		r.hoverOverlay = nil
		r.markerStateValid = false
		r.hint.Show()
		canvas.Refresh(r.widget)
		return
	}
	r.hint.Hide()

	if r.widget.dataDirty {
		// Expensive: only happens on SetData (new TOML pasted).
		r.buildWorldGeometry()
		r.markerStateValid = false // force a real marker rebuild below
		r.widget.dataDirty = false
	}

	if r.widget.geometryDirty {
		r.repositionWorldGeometry() // cheap: reposition only, never reallocates

		cam := r.widget.cam
		size := r.widget.Size()
		purePan := r.markerStateValid && cam.Zoom == r.lastZoom && size == r.lastMarkerSize

		if purePan {
			dx := float32(-(cam.OffsetX - r.lastOffsetX) * cam.Zoom)
			dy := float32(-(cam.OffsetZ - r.lastOffsetZ) * cam.Zoom)
			r.shiftMarkers(dx, dy)
		} else {
			r.widget.rebuildClusters()
			r.rebuildObjectMarkers()
		}

		r.lastOffsetX, r.lastOffsetZ, r.lastZoom, r.lastMarkerSize = cam.OffsetX, cam.OffsetZ, cam.Zoom, size
		r.markerStateValid = true

		r.widget.geometryDirty = false
		r.builtForSize = size
	}

	r.rebuildHoverOverlay()

	canvas.Refresh(r.widget)
}

// buildWorldGeometry allocates every track line and platform footprint.
// Only called when the underlying data changed (widget.dataDirty) — never
// on pan/zoom. No text labels are allocated here any more — see the
// package comment above.
func (r *mapRenderer) buildWorldGeometry() {
	r.worldLines = nil
	r.platforms = nil

	for _, t := range r.widget.tracks {
		col := colorTrack
		if t.Section.IsPoints {
			col = colorTrackPoints
		}
		r.addWorldPolyline(t.Points, col)
	}

	for i := range r.widget.platformDrawables {
		pd := &r.widget.platformDrawables[i]
		for fi := range pd.footprints {
			r.platforms = append(r.platforms, r.buildPlatformPolygon(pd, fi))
		}
	}
}

// buildPlatformPolygon allocates canvas lines for one footprint.
// For ribbon footprints (isRibbon=true) it builds one solid stroke per
// pair of matching offset-edge / centerline points — these span the full
// platform width and tile into a solid filled band when drawn with
// StrokeWidth = band width. For explicit polygon footprints (isRibbon=false)
// it falls back to a closed outline.
func (r *mapRenderer) buildPlatformPolygon(pd *platformDrawable, fi int) worldPolygon {
	poly := pd.footprints[fi]
	wp := worldPolygon{pd: pd, footprintIdx: fi}

	if !pd.isRibbon {
		for i := 0; i < len(poly); i++ {
			wp.segs = append(wp.segs, canvas.NewLine(colorPlatformEdge))
		}
		return wp
	}

	n := len(poly)
	if n < 4 || n%2 != 0 {
		return wp
	}
	half := n / 2
	for i := 0; i < half; i++ {
		wp.segs = append(wp.segs, canvas.NewLine(colorPlatformFill))
	}
	return wp
}

func (r *mapRenderer) addWorldPolyline(points []Point2D, col color.Color) {
	for i := 0; i < len(points)-1; i++ {
		line := canvas.NewLine(col)
		line.StrokeWidth = trackLineWidth
		r.worldLines = append(r.worldLines, worldLine{
			obj: line,
			p1:  points[i],
			p2:  points[i+1],
		})
	}
}

// repositionWorldGeometry moves every cached line to match the current
// camera, without allocating anything. This is what runs on every
// pan/zoom/resize instead of a full rebuild.
func (r *mapRenderer) repositionWorldGeometry() {
	size := r.widget.Size()
	cam := r.widget.cam

	for i := range r.worldLines {
		wl := &r.worldLines[i]
		x1, y1 := cam.WorldToScreen(wl.p1, size.Width, size.Height)
		x2, y2 := cam.WorldToScreen(wl.p2, size.Width, size.Height)
		wl.obj.Position1 = fyne.NewPos(x1, y1)
		wl.obj.Position2 = fyne.NewPos(x2, y2)
		wl.obj.Refresh()
	}

	for i := range r.platforms {
		r.repositionPlatform(&r.platforms[i])
	}
}

// repositionPlatform repositions a platform footprint's strokes for the
// current camera. For ribbon footprints each stroke runs from
// offsetEdge[i] to the matching centerline point (centerEdge is stored
// reversed by OffsetRibbon, so index half-1-i undoes that). StrokeWidth
// is set to the full band width so the strokes tile into a solid filled
// rectangle with no gaps. For explicit polygon footprints it's a closed
// outline.
func (r *mapRenderer) repositionPlatform(wp *worldPolygon) {
	poly := wp.pd.footprints[wp.footprintIdx]
	size := r.widget.Size()
	cam := r.widget.cam

	if !wp.pd.isRibbon {
		n := len(poly)
		if n < 2 || len(wp.segs) != n {
			return
		}
		for i := 0; i < n; i++ {
			next := (i + 1) % n
			x1, y1 := cam.WorldToScreen(poly[i], size.Width, size.Height)
			x2, y2 := cam.WorldToScreen(poly[next], size.Width, size.Height)
			e := wp.segs[i]
			e.Position1 = fyne.NewPos(x1, y1)
			e.Position2 = fyne.NewPos(x2, y2)
			e.StrokeWidth = 2
			e.Refresh()
		}
		return
	}

	n := len(poly)
	if n < 4 || n%2 != 0 || len(wp.segs) == 0 {
		return
	}
	half := n / 2
	offsetEdge := poly[:half]
	centerEdge := poly[half:]

	// Each cross-stroke i runs from offsetEdge[i] to the matching
	// centerline point and needs a StrokeWidth wide enough to cover the
	// gap to its NEIGHBOURING cross-strokes along the platform, or gaps
	// open up ("teeth") wherever real sample spacing exceeds the width.
	//
	// SampleTrack samples at fixed Bezier-parameter steps, so spacing
	// between consecutive offsetEdge points varies with local curvature/
	// segment length — it is NOT constant, so a single zoom-derived
	// constant (the old `platformWidthWorld*cam.Zoom`) is wrong: it's
	// too narrow wherever points are sparser than average (gaps/teeth,
	// exactly what's visible in the screenshot) and wastefully wide
	// wherever they're denser. Instead, measure each stroke's actual
	// screen-space neighbour gaps directly and size to cover the larger
	// of the two (so it laps onto both neighbours), plus a little slop.
	screenOffset := make([]fyne.Position, half)
	for i := 0; i < half; i++ {
		x, y := cam.WorldToScreen(offsetEdge[i], size.Width, size.Height)
		screenOffset[i] = fyne.NewPos(x, y)
	}

	for i := 0; i < half; i++ {
		c := centerEdge[half-1-i]
		x2, y2 := cam.WorldToScreen(c, size.Width, size.Height)
		x1, y1 := screenOffset[i].X, screenOffset[i].Y

		gap := float32(0)
		if i > 0 {
			if d := dist2D(x1, y1, screenOffset[i-1].X, screenOffset[i-1].Y); d > gap {
				gap = d
			}
		}
		if i < half-1 {
			if d := dist2D(x1, y1, screenOffset[i+1].X, screenOffset[i+1].Y); d > gap {
				gap = d
			}
		}

		strokeWidth := gap + 2 // +2px slop so adjacent strokes overlap rather than just touch
		if strokeWidth < 3 {
			strokeWidth = 3
		}

		seg := wp.segs[i]
		seg.Position1 = fyne.NewPos(x1, y1)
		seg.Position2 = fyne.NewPos(x2, y2)
		seg.StrokeWidth = strokeWidth
		seg.Refresh()
	}
}

// rebuildObjectMarkers rebuilds object dots + direction arrows.
// Reallocated on zoom/resize/data changes (cluster membership is
// screen-space) but NOT on pure panning — see shiftMarkers.
func (r *mapRenderer) rebuildObjectMarkers() {
	r.markers = nil
	cam := r.widget.cam
	size := r.widget.Size()

	for _, c := range r.widget.objectClusters {
		if len(c.Members) == 1 {
			p := c.Members[0]
			col := objectColor(p.obj.Type)
			dot := canvas.NewCircle(col)
			dot.Position1 = fyne.NewPos(c.ScreenPos.X-objectDotRadius, c.ScreenPos.Y-objectDotRadius)
			dot.Position2 = fyne.NewPos(c.ScreenPos.X+objectDotRadius, c.ScreenPos.Y+objectDotRadius)
			r.markers = append(r.markers, dot)

			if dirVec, ok := p.arrowVector(); ok {
				r.markers = append(r.markers, r.buildDirectionArrow(p.pt, dirVec, col, cam, size)...)
			}
			continue
		}

		// Overlapping objects: lay them out as small dots side by side
		// (wrapping into rows if there are many), instead of collapsing
		// into one big blob with a count. Each dot keeps its own type
		// color, so you can tell at a glance what's there before even
		// hovering — hover still lists every member individually.
		const perRow = 4
		const dotRad = objectDotRadius * 0.75
		spacing := dotRad*2 + 3

		n := len(c.Members)
		rows := (n + perRow - 1) / perRow
		for idx, p := range c.Members {
			row := idx / perRow
			col := idx % perRow

			rowCount := perRow
			if row == rows-1 {
				rowCount = n - row*perRow
			}
			rowWidth := float32(rowCount-1) * spacing
			x := c.ScreenPos.X - rowWidth/2 + float32(col)*spacing
			y := c.ScreenPos.Y - float32(rows-1)*spacing/2 + float32(row)*spacing

			dot := canvas.NewCircle(objectColor(p.obj.Type))
			dot.Position1 = fyne.NewPos(x-dotRad, y-dotRad)
			dot.Position2 = fyne.NewPos(x+dotRad, y+dotRad)
			r.markers = append(r.markers, dot)
		}
	}
}

// buildDirectionArrow draws a small chevron (two wings + a short shaft)
// pointing along dirVec from world point pt, computed in world space
// then projected to screen, so its apparent size scales with zoom like
// everything else.
func (r *mapRenderer) buildDirectionArrow(pt Point2D, dirVec Point2D, col color.Color, cam *Camera, size fyne.Size) []fyne.CanvasObject {
	tipWorld := Point2D{X: pt.X + dirVec.X*signalArrowLength, Z: pt.Z + dirVec.Z*signalArrowLength}

	// Perpendicular to dirVec, for the chevron's two back corners.
	px, pz := -dirVec.Z, dirVec.X

	back1 := Point2D{
		X: pt.X + dirVec.X*(signalArrowLength*0.4) + px*signalArrowWidth,
		Z: pt.Z + dirVec.Z*(signalArrowLength*0.4) + pz*signalArrowWidth,
	}
	back2 := Point2D{
		X: pt.X + dirVec.X*(signalArrowLength*0.4) - px*signalArrowWidth,
		Z: pt.Z + dirVec.Z*(signalArrowLength*0.4) - pz*signalArrowWidth,
	}

	tipX, tipY := cam.WorldToScreen(tipWorld, size.Width, size.Height)
	b1X, b1Y := cam.WorldToScreen(back1, size.Width, size.Height)
	b2X, b2Y := cam.WorldToScreen(back2, size.Width, size.Height)
	originX, originY := cam.WorldToScreen(pt, size.Width, size.Height)

	shaft := canvas.NewLine(col)
	shaft.StrokeWidth = 2
	shaft.Position1 = fyne.NewPos(originX, originY)
	shaft.Position2 = fyne.NewPos(tipX, tipY)

	wing1 := canvas.NewLine(col)
	wing1.StrokeWidth = 2
	wing1.Position1 = fyne.NewPos(tipX, tipY)
	wing1.Position2 = fyne.NewPos(b1X, b1Y)

	wing2 := canvas.NewLine(col)
	wing2.StrokeWidth = 2
	wing2.Position1 = fyne.NewPos(tipX, tipY)
	wing2.Position2 = fyne.NewPos(b2X, b2Y)

	return []fyne.CanvasObject{shaft, wing1, wing2}
}

// shiftMarkers translates every marker canvas object by a screen-space
// delta, without reallocating anything. Used on a pure pan, where
// cluster membership can't have changed (translation preserves relative
// screen distances).
func (r *mapRenderer) shiftMarkers(dx, dy float32) {
	for _, obj := range r.markers {
		switch o := obj.(type) {
		case *canvas.Circle:
			o.Position1 = fyne.NewPos(o.Position1.X+dx, o.Position1.Y+dy)
			o.Position2 = fyne.NewPos(o.Position2.X+dx, o.Position2.Y+dy)
			o.Refresh()
		case *canvas.Line:
			o.Position1 = fyne.NewPos(o.Position1.X+dx, o.Position1.Y+dy)
			o.Position2 = fyne.NewPos(o.Position2.X+dx, o.Position2.Y+dy)
			o.Refresh()
		}
	}
}

// rebuildHoverOverlay rebuilds the highlight ring, the hover tooltip
// (which is now the ONLY place any name/label text appears), and the
// measurement line/label. Cheap — at most a handful of canvas objects —
// so it's safe to call on every mouse-move frame.
func (r *mapRenderer) rebuildHoverOverlay() {
	r.hoverOverlay = nil

	size := r.widget.Size()
	cam := r.widget.cam

	r.rebuildMeasurementOverlay(cam, size)

	if r.widget.hoverInfo == "" {
		r.tooltipBg.Hide()
		r.tooltipTitle.Hide()
		r.tooltipDetail.Hide()
		return
	}

	// Highlight ring around whatever's hovered, skipped for platform
	// hovers since the whole footprint is already the highlight.
	if r.widget.hoverPlatform == nil {
		var sx, sy float32
		if r.widget.hoverCluster != nil {
			sx, sy = r.widget.hoverCluster.ScreenPos.X, r.widget.hoverCluster.ScreenPos.Y
		} else {
			sx, sy = cam.WorldToScreen(r.widget.hoverWorldPt, size.Width, size.Height)
		}

		radius := objectDotRadius + 3
		highlightColor := colorTrackHover
		if r.widget.hoverIsObject {
			highlightColor = colorObjectHover
		}
		ring := canvas.NewCircle(color.Transparent)
		ring.StrokeColor = highlightColor
		ring.StrokeWidth = 2
		ring.Position1 = fyne.NewPos(sx-radius, sy-radius)
		ring.Position2 = fyne.NewPos(sx+radius, sy+radius)
		r.hoverOverlay = append(r.hoverOverlay, ring)
	}

	r.tooltipTitle.Text = r.widget.hoverInfo
	r.tooltipTitle.Refresh()
	r.tooltipDetail.Text = r.widget.hoverDetail
	r.tooltipDetail.Refresh()

	titleW := r.tooltipTitle.MinSize().Width
	titleH := r.tooltipTitle.MinSize().Height
	detailW := r.tooltipDetail.MinSize().Width
	detailH := r.tooltipDetail.MinSize().Height

	textW := titleW
	if detailW > textW {
		textW = detailW
	}
	textH := titleH
	const gap = float32(2)
	if r.widget.hoverDetail != "" {
		textH += gap + detailH
	}

	pad := float32(8)
	tx := r.widget.hoverPos.X + 14
	ty := r.widget.hoverPos.Y + 14

	// keep tooltip on-screen if near the right/bottom edge
	if tx+textW+pad*2 > size.Width {
		tx = size.Width - textW - pad*2
	}
	if ty+textH+pad*2 > size.Height {
		ty = size.Height - textH - pad*2
	}
	if tx < 0 {
		tx = 0
	}
	if ty < 0 {
		ty = 0
	}

	r.tooltipBg.Resize(fyne.NewSize(textW+pad*2, textH+pad*2))
	r.tooltipBg.Move(fyne.NewPos(tx, ty))
	r.tooltipTitle.Move(fyne.NewPos(tx+pad, ty+pad))
	if r.widget.hoverDetail != "" {
		r.tooltipDetail.Move(fyne.NewPos(tx+pad, ty+pad+titleH+gap))
		r.tooltipDetail.Show()
	} else {
		r.tooltipDetail.Hide()
	}

	r.tooltipBg.Show()
	r.tooltipTitle.Show()
}

// rebuildMeasurementOverlay draws the distance-measurement line and
// label. Resolves the path via MapWidget.findMeasurePath, which can span
// multiple connected track sections (not just one), drawing each
// section's actual curve rather than a single straight chord across
// junctions. Falls back to a straight line + straight-line distance only
// when the two points aren't on a connected track at all.
func (r *mapRenderer) rebuildMeasurementOverlay(cam *Camera, size fyne.Size) {
	start := r.widget.measureStart
	if start == nil {
		return
	}

	end := r.widget.measureEnd
	if end == nil {
		// Live preview: follow the cursor until the second click locks
		// the measurement in.
		live := r.widget.snapToTrack(r.widget.hoverPos)
		end = &live
	}

	path, ok := r.widget.findMeasurePath(start, end)

	if ok {
		for _, seg := range path.segments {
			track := r.widget.findSampledTrack(seg.track)
			if track == nil {
				continue
			}
			pts := track.PointsBetweenOffsets(seg.from, seg.to)
			if seg.from > seg.to {
				for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
					pts[i], pts[j] = pts[j], pts[i]
				}
			}
			for i := 0; i < len(pts)-1; i++ {
				x1, y1 := cam.WorldToScreen(pts[i], size.Width, size.Height)
				x2, y2 := cam.WorldToScreen(pts[i+1], size.Width, size.Height)
				segLine := canvas.NewLine(colorMeasure)
				segLine.StrokeWidth = 2
				segLine.Position1 = fyne.NewPos(x1, y1)
				segLine.Position2 = fyne.NewPos(x2, y2)
				r.hoverOverlay = append(r.hoverOverlay, segLine)
			}
		}
	} else {
		x1, y1 := cam.WorldToScreen(start.WorldPt, size.Width, size.Height)
		x2, y2 := cam.WorldToScreen(end.WorldPt, size.Width, size.Height)
		line := canvas.NewLine(colorMeasure)
		line.StrokeWidth = 2
		line.Position1 = fyne.NewPos(x1, y1)
		line.Position2 = fyne.NewPos(x2, y2)
		r.hoverOverlay = append(r.hoverOverlay, line)
	}

	x1, y1 := cam.WorldToScreen(start.WorldPt, size.Width, size.Height)
	x2, y2 := cam.WorldToScreen(end.WorldPt, size.Width, size.Height)

	for _, pt := range [][2]float32{{x1, y1}, {x2, y2}} {
		dot := canvas.NewCircle(colorMeasure)
		dot.Position1 = fyne.NewPos(pt[0]-3, pt[1]-3)
		dot.Position2 = fyne.NewPos(pt[0]+3, pt[1]+3)
		r.hoverOverlay = append(r.hoverOverlay, dot)
	}

	var labelText string
	switch {
	case ok && len(path.segments) == 1:
		labelText = fmt.Sprintf("%.1fm along %s", path.total, path.segments[0].track)
	case ok:
		labelText = fmt.Sprintf("%.1fm along %d sections", path.total, len(path.segments))
	case start.OnTrack && end.OnTrack:
		d := dist(start.WorldPt, end.WorldPt)
		labelText = fmt.Sprintf("%.1fm straight-line (not connected)", d)
	default:
		d := dist(start.WorldPt, end.WorldPt)
		labelText = fmt.Sprintf("%.1fm straight-line", d)
	}

	labelBg := canvas.NewRectangle(colorPanelBg)
	label := canvas.NewText(labelText, colorMeasure)
	label.TextSize = 13
	midX := (x1 + x2) / 2
	midY := (y1 + y2) / 2

	w := label.MinSize().Width
	h := label.MinSize().Height
	const lpad = float32(5)
	labelBg.Resize(fyne.NewSize(w+lpad*2, h+lpad*2))
	labelBg.Move(fyne.NewPos(midX+8-lpad, midY-20-lpad))
	label.Move(fyne.NewPos(midX+8, midY-20))

	r.hoverOverlay = append(r.hoverOverlay, labelBg, label)
}

func objectColor(objType string) color.Color {
	switch objType {
	case "SIGNAL", "FIXED_DISTANT":
		return colorObjectSignal
	case "BUFFER":
		return colorObjectBuffer
	default:
		return colorObjectOther
	}
}

func (r *mapRenderer) Objects() []fyne.CanvasObject {
	objs := []fyne.CanvasObject{r.bg, r.hint}
	for _, p := range r.platforms {
		objs = append(objs, lineObjs(p.segs)...)
	}
	for _, wl := range r.worldLines {
		objs = append(objs, wl.obj)
	}
	objs = append(objs, r.markers...)
	objs = append(objs, r.hoverOverlay...)
	objs = append(objs, r.tooltipBg, r.tooltipTitle, r.tooltipDetail)
	return objs
}

func lineObjs(lines []*canvas.Line) []fyne.CanvasObject {
	out := make([]fyne.CanvasObject, len(lines))
	for i, l := range lines {
		out[i] = l
	}
	return out
}

func (r *mapRenderer) Destroy() {}
