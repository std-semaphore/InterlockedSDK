package main

import (
	"fmt"
	"image/color"
	"math"
	"sort"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// Colors. Kept as package vars rather than inline literals so the palette
// is easy to retune in one place.
var (
	colorTrack         = color.NRGBA{R: 0x8a, G: 0x9b, B: 0xa8, A: 0xff}
	colorTrackPoints   = color.NRGBA{R: 0xe0, G: 0xa4, B: 0x3a, A: 0xff} // points/junctions stand out
	colorTrackHover    = color.NRGBA{R: 0xff, G: 0xd6, B: 0x6b, A: 0xff}
	colorObjectSignal  = color.NRGBA{R: 0x4c, G: 0xaf, B: 0x50, A: 0xff}
	colorObjectBuffer  = color.NRGBA{R: 0xe5, G: 0x39, B: 0x35, A: 0xff}
	colorObjectOther   = color.NRGBA{R: 0x42, G: 0xa5, B: 0xf5, A: 0xff}
	colorObjectHover   = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	colorObjectCluster = color.NRGBA{R: 0xff, G: 0x8f, B: 0x00, A: 0xff} // stands out from any single object color
	colorPlatformFill  = color.NRGBA{R: 0xff, G: 0x99, B: 0x1f, A: 0xff} // solid orange footprint
	colorPlatformEdge  = color.NRGBA{R: 0xff, G: 0x99, B: 0x1f, A: 0xff}
	colorLabel         = color.NRGBA{R: 0xc8, G: 0xcc, B: 0xd1, A: 0xff}
	colorMeasure       = color.NRGBA{R: 0xff, G: 0xeb, B: 0x3b, A: 0xff}
	colorBackground    = color.NRGBA{R: 0x1a, G: 0x1c, B: 0x1f, A: 0xff}
	colorPanelBg       = color.NRGBA{R: 0x14, G: 0x14, B: 0x16, A: 0xe6}
)

const (
	trackLineWidth  = float32(2)
	objectDotRadius = float32(5)
	hoverPickPixels = float32(10) // how close (in screen px) the mouse must be to register a hover

	// hoverGridCellSize buckets sampled track points in world space for
	// fast hover lookups / measurement-tool snapping. Should be comfortably
	// bigger than typical point spacing but small enough to keep buckets
	// from getting huge on dense curves. 50 world units works well for
	// this dataset's scale (meters).
	hoverGridCellSize = 50.0

	// clusterCellPx buckets objects in SCREEN space (unlike the hover
	// grid, which is world space) so that "overlapping" is judged by how
	// close things look on screen right now, not by world distance —
	// two signals 2m apart in the world should cluster when zoomed out
	// and separate again once zoomed in past that gap.
	clusterCellPx = float32(20)

	// All non-hover text labels (track names, object names, platform/
	// station names) are hidden by default and only shown for whatever's
	// currently under the cursor. This was the single biggest source of
	// visual clutter, so there's no zoom-based label gate any more —
	// hover is the only way labels appear.

	// platformWidthWorld is an assumed visual platform width in world
	// units (meters) for drawing — the data only gives boundary points
	// along the track, not an actual physical width, so this is just a
	// reasonable constant for legibility, not real platform geometry.
	platformWidthWorld = 4.0

	// platformGapWorld is the gap (world units) left between the track
	// line and the near edge of the platform band, so the platform
	// doesn't visually sit on top of (and obscure) the rails/track label.
	platformGapWorld = 2.0

	// platformOverlapMargin pads platform bounding boxes by this much
	// (world units) when checking for collisions between platforms, so
	// near-misses still steer towards the less-crowded side instead of
	// only reacting once boxes are already touching.
	platformOverlapMargin = 1.5

	// signalArrowLength/Width control the size (world units) of the
	// direction chevron drawn at each directional object. Kept small so
	// the arrows are a subtle indicator rather than a dominant visual.
	signalArrowLength = 3.0
	signalArrowWidth  = 1.4
)

// objPlacement is an object's resolved world position (and, for
// directional objects, the track's tangent at that point), computed once
// when data loads rather than on every hover check / every render frame.
type objPlacement struct {
	obj   *Object
	pt    Point2D
	dir   Point2D // unit tangent at pt, meaningful only if dirOK
	ok    bool    // false if the object's track has no curve data to place it on
	dirOK bool    // false if a tangent couldn't be computed (e.g. single-point track)
}

// arrowVector returns the world-space unit vector the object's direction
// arrow should point along, accounting for up/down. "both" or unrecognized
// directions draw no arrow at all (ok=false).
func (p *objPlacement) arrowVector() (Point2D, bool) {
	if !p.dirOK {
		return Point2D{}, false
	}
	switch p.obj.Direction {
	case "up":
		// Track tangent points in the direction of increasing offset,
		// which is the "down" direction (offset 0 is the up end after
		// normalizeDirection) — so "up" objects point against it.
		return Point2D{X: -p.dir.X, Z: -p.dir.Z}, true
	case "down":
		return p.dir, true
	default:
		return Point2D{}, false
	}
}

// objectCluster groups objects whose screen positions are close enough to
// be visually overlapping at the current zoom level. A cluster of 1 is
// just a normal object. Recomputed whenever the camera changes (see
// rebuildClusters), so it's always in screen-space sync with what's drawn.
type objectCluster struct {
	ScreenPos fyne.Position
	Members   []*objPlacement
}

// platformDrawable is a platform resolved to something we can actually
// draw: a filled footprint polygon plus a label anchor point. Computed
// once in SetData since it's pure world-space geometry, independent of
// the camera.
//
// footprint is in one of two shapes depending on isRibbon:
//   - isRibbon=true: the exact shape OffsetRibbon produces (offset edge,
//     then the original centerline reversed) — what buildPlatformPolygon/
//     repositionPlatform in renderer.go know how to turn into fill+edge
//     strokes.
//   - isRibbon=false: an arbitrary explicit polygon from the dataset's
//     own platformGeometry field, drawn as a plain outline only (no
//     special ribbon fill, since we can't assume anything about its
//     point ordering).
type platformDrawable struct {
	name string
	crs  string
	// footprints holds one ribbon polygon per track section the platform
	// spans. Single-track platforms have one entry; platforms crossing a
	// junction have one entry per section. Each entry uses the same format
	// as before: isRibbon=true → OffsetRibbon output (offset edge then
	// reversed centerline); isRibbon=false → explicit polygon outline.
	footprints [][]Point2D
	isRibbon   bool
	labelPt    Point2D
}

// gridPoint is one sampled track point, indexed spatially for hover
// lookups so we don't linear-scan every point on every mouse move.
type gridPoint struct {
	pt        Point2D
	trackName string
	offset    float64
}

type hoverGrid struct {
	cells map[[2]int][]gridPoint
}

func newHoverGrid(tracks []SampledTrack) *hoverGrid {
	g := &hoverGrid{cells: make(map[[2]int][]gridPoint)}
	for _, t := range tracks {
		for i, p := range t.Points {
			key := g.cellKey(p)
			g.cells[key] = append(g.cells[key], gridPoint{
				pt:        p,
				trackName: t.Section.Name,
				offset:    t.Offsets[i],
			})
		}
	}
	return g
}

func (g *hoverGrid) cellKey(p Point2D) [2]int {
	return [2]int{
		int(math.Floor(p.X / hoverGridCellSize)),
		int(math.Floor(p.Z / hoverGridCellSize)),
	}
}

// query returns candidate points within roughly worldRadius of center,
// by scanning the cells the search radius could reach. Callers still need
// to do an exact distance check (in screen space) on the results.
func (g *hoverGrid) query(center Point2D, worldRadius float64) []gridPoint {
	cellSpan := int(math.Ceil(worldRadius/hoverGridCellSize)) + 1
	baseX := int(math.Floor(center.X / hoverGridCellSize))
	baseZ := int(math.Floor(center.Z / hoverGridCellSize))

	var out []gridPoint
	for dx := -cellSpan; dx <= cellSpan; dx++ {
		for dz := -cellSpan; dz <= cellSpan; dz++ {
			if pts, ok := g.cells[[2]int{baseX + dx, baseZ + dz}]; ok {
				out = append(out, pts...)
			}
		}
	}
	return out
}

// MeasurePoint is one end of a distance measurement. If OnTrack is true,
// it's snapped to a sampled track point (Track/Offset are meaningful);
// otherwise it's a raw world click with no track association, and only
// straight-line distance can be reported for it.
type MeasurePoint struct {
	WorldPt Point2D
	Track   string
	Offset  float64
	OnTrack bool
}

// trackEndNode identifies one of the two physical ends of a track
// section (End 0 = offset 0, End 1 = offset Length). The measurement
// tool's connectivity graph is built over these nodes rather than over
// individual sample points, since a track only ever joins another track
// at one of its two ends.
type trackEndNode struct {
	track string
	end   int
}

type trackEdge struct {
	to     trackEndNode
	weight float64
}

// pathSegment is one track-section's contribution to a (possibly
// multi-section) measurement: walk `track` from offset `from` to offset
// `to`. from/to are not necessarily ordered low-to-high — direction
// matters so consecutive segments chain head-to-tail into one continuous
// path for drawing.
type pathSegment struct {
	track    string
	from, to float64
}

// measurePath is the result of resolving a measurement between two
// points, potentially across several connected track sections.
type measurePath struct {
	segments []pathSegment
	total    float64
}

// endpointEpsilon is how close two different tracks' endpoints need to
// be (world units) to be treated as physically joined for the purposes
// of multi-section measurement.
const endpointEpsilon = 0.5

// MapWidget is a custom widget rendering the parsed track data with
// pan/zoom and hover tooltips. It owns its own Camera.
type MapWidget struct {
	widget.BaseWidget

	data   *TrackData
	tracks []SampledTrack // pre-sampled, rebuilt whenever data changes

	objPlacements     []objPlacement
	platformDrawables []platformDrawable
	objectClusters    []objectCluster // recomputed in renderer.rebuildStatic, see comment there
	grid              *hoverGrid
	trackGraph        map[trackEndNode][]trackEdge // connectivity between track sections, for multi-section measurement

	cam *Camera

	// geometryDirty is true whenever the camera moved (pan/zoom) or the
	// view was resized, so cached canvas objects need repositioning and
	// object clusters need recomputing. It does NOT mean "reallocate
	// everything" — see dataDirty for that. Plain mouse-move hover
	// updates set neither flag; they only touch the small hover/
	// measurement overlay, which is what keeps hovering cheap.
	geometryDirty bool

	// dataDirty is true only when the underlying track/object/platform
	// data itself changed (i.e. after SetData). This is what triggers a
	// full reallocation of every canvas.Line/Text/Circle for the static
	// map content in the renderer. Panning and zooming must NOT set this
	// — they go through geometryDirty instead, which just repositions
	// the already-allocated objects. Recreating thousands of canvas
	// objects on every drag/scroll tick is what caused the lag (and very
	// likely the crashes, from GC/GL pressure) before this split existed.
	dataDirty bool

	// hoverInfo/hoverDetail hold the tooltip's title line and (optional)
	// extra detail lines, empty if nothing is hovered. Every text label
	// in the map (track names, object names, platform/station names) is
	// hover-only now — this tooltip is the ONLY place names ever appear.
	hoverInfo     string
	hoverDetail   string
	hoverIsObject bool           // true if hoverInfo refers to an object marker rather than a track point
	hoverWorldPt  Point2D        // world position of whatever is currently hovered, for drawing a highlight dot
	hoverCluster  *objectCluster // set instead of hoverWorldPt when hovering a multi-object cluster
	hoverPlatform *platformDrawable
	hoverPos      fyne.Position

	// Distance measurement tool: right-click sets the start point,
	// right-click again sets the end point and locks the measurement in,
	// right-click a third time starts a new measurement from scratch.
	// While only the start point is set, the line/label preview follows
	// the cursor live. Points snap to the nearest track sample (within
	// hoverPickPixels) so the reported distance is along-the-track, not
	// as-the-crow-flies, whenever possible.
	measureStart *MeasurePoint
	measureEnd   *MeasurePoint

	onStatus func(string) // callback to push status text to the parent (e.g. "Loaded 23 sections")
}

func NewMapWidget(onStatus func(string)) *MapWidget {
	m := &MapWidget{
		cam:      NewCamera(),
		onStatus: onStatus,
	}
	m.ExtendBaseWidget(m)
	return m
}

// SetData replaces the rendered track data, resamples curves, precomputes
// object/platform placements + the hover spatial index, and fits the
// camera to the new content.
func (m *MapWidget) SetData(data *TrackData) {
	m.data = data
	m.tracks = nil
	m.measureStart = nil
	m.measureEnd = nil
	m.clearHover()

	for i := range data.Sections {
		m.tracks = append(m.tracks, SampleTrack(&data.Sections[i]))
	}

	size := m.Size()
	if size.Width <= 1 || size.Height <= 1 {
		size = fyne.NewSize(800, 600)
	}

	var allPoints []Point2D
	for _, t := range m.tracks {
		allPoints = append(allPoints, t.Points...)
	}
	m.cam = FitToBounds(allPoints, size.Width, size.Height)

	// Resolve every object's world position (and direction tangent) once,
	// up front, instead of doing a track lookup + scan on every render and
	// every hover check.
	m.objPlacements = nil
	skippedObjects := 0
	for i := range data.Objects {
		obj := &data.Objects[i]
		track := m.findSampledTrack(obj.TrackSection)
		placement := objPlacement{obj: obj}
		if track != nil {
			placement.pt, placement.ok = track.PointAtOffset(obj.At)
			placement.dir, placement.dirOK = track.DirectionAtOffset(obj.At)
		}
		if !placement.ok {
			skippedObjects++
		}
		m.objPlacements = append(m.objPlacements, placement)
	}

	// buildTrackGraph must run BEFORE buildPlatformDrawables: any platform
	// whose up/down boundaries sit on two different track sections (the
	// common case) resolves its path via m.shortestPath, which reads
	// m.trackGraph. Building the graph after platforms were resolved left
	// it empty during that resolution, so shortestPath always failed and
	// every cross-section platform was silently dropped (the `continue`
	// in buildPlatformDrawables when bestPath == nil).
	m.buildTrackGraph()
	m.buildPlatformDrawables(data)

	m.grid = newHoverGrid(m.tracks)
	m.geometryDirty = true
	m.dataDirty = true

	if m.onStatus != nil {
		msg := fmt.Sprintf(
			"%d sections · %d objects · %d platforms",
			len(data.Sections), len(data.Objects), len(data.Platforms),
		)
		if skippedObjects > 0 {
			msg += fmt.Sprintf("  (%d objects skipped — no curve data)", skippedObjects)
		}
		m.onStatus(msg)
	}

	m.Refresh()
}

func (m *MapWidget) HasData() bool {
	return m.data != nil && len(m.data.Sections) > 0
}

func (m *MapWidget) clearHover() {
	m.hoverInfo = ""
	m.hoverDetail = ""
	m.hoverCluster = nil
	m.hoverPlatform = nil
}

// buildPlatformDrawables resolves each platform to one or more drawable
// ribbon footprints.
//
// Side selection prefers the side that:
//  1. Doesn't overlap any other track section's bounding box (avoid
//     landing on a parallel track). If both sides hit tracks, fall through.
//  2. Among surviving sides, pick the one with fewer collisions with
//     already-placed platform ribbons.
//  3. Tie-break by centroid distance from placed platforms.
func (m *MapWidget) buildPlatformDrawables(data *TrackData) {
	m.platformDrawables = nil
	var placedBoxes []bboxF
	var placedCentroids []Point2D

	// Pre-build per-track bounding boxes for track-collision checks.
	trackBoxes := make([]bboxF, len(m.tracks))
	for i, t := range m.tracks {
		trackBoxes[i] = bboxOf(t.Points)
	}

	platforms := make([]*Platform, len(data.Platforms))
	for i := range data.Platforms {
		platforms[i] = &data.Platforms[i]
	}
	sort.Slice(platforms, func(i, j int) bool {
		if platforms[i].StationCRS != platforms[j].StationCRS {
			return platforms[i].StationCRS < platforms[j].StationCRS
		}
		return platforms[i].Name < platforms[j].Name
	})

	for _, p := range platforms {
		if len(p.Geometry) >= 3 {
			m.platformDrawables = append(m.platformDrawables, platformDrawable{
				name:       p.Name,
				crs:        p.StationCRS,
				footprints: [][]Point2D{p.Geometry},
				isRibbon:   false,
				labelPt:    averagePoint(p.Geometry),
			})
			continue
		}

		if p.UpTrack == "" {
			continue
		}

		type baseSegment struct{ points []Point2D }
		var bases []baseSegment
		var allBase []Point2D

		if p.UpTrack == p.DownTrack {
			track := m.findSampledTrack(p.UpTrack)
			if track == nil {
				continue
			}
			pts := track.PointsBetweenOffsets(p.UpAt, p.DownAt)
			if len(pts) < 2 {
				continue
			}
			bases = []baseSegment{{pts}}
			allBase = pts
		} else {
			upTrack := m.findSampledTrack(p.UpTrack)
			downTrack := m.findSampledTrack(p.DownTrack)
			if upTrack == nil || downTrack == nil {
				continue
			}

			type opt struct{ startEnd, endEnd int }
			bestTotal := math.Inf(1)
			var bestPath []trackEndNode
			var bestOpt opt
			for _, o := range []opt{{0, 0}, {0, 1}, {1, 0}, {1, 1}} {
				lead := p.UpAt
				if o.startEnd == 1 {
					lead = upTrack.Length - p.UpAt
				}
				trail := p.DownAt
				if o.endEnd == 1 {
					trail = downTrack.Length - p.DownAt
				}
				d, path, ok := m.shortestPath(
					trackEndNode{p.UpTrack, o.startEnd},
					trackEndNode{p.DownTrack, o.endEnd},
				)
				if !ok {
					continue
				}
				total := lead + d + trail
				if total < bestTotal {
					bestTotal = total
					bestPath = path
					bestOpt = o
				}
			}
			if bestPath == nil {
				continue
			}

			exitUp := 0.0
			if bestOpt.startEnd == 1 {
				exitUp = upTrack.Length
			}
			if pts := upTrack.PointsBetweenOffsets(p.UpAt, exitUp); len(pts) >= 2 {
				bases = append(bases, baseSegment{pts})
				allBase = append(allBase, pts...)
			}

			for i := 0; i+1 < len(bestPath); i++ {
				a, b := bestPath[i], bestPath[i+1]
				if a.track != b.track {
					continue
				}
				tr := m.findSampledTrack(a.track)
				if tr == nil {
					continue
				}
				from, to := 0.0, tr.Length
				if a.end == 1 {
					from, to = tr.Length, 0.0
				}
				if pts := tr.PointsBetweenOffsets(from, to); len(pts) >= 2 {
					bases = append(bases, baseSegment{pts})
					allBase = append(allBase, pts...)
				}
			}

			entryDown := 0.0
			if bestOpt.endEnd == 1 {
				entryDown = downTrack.Length
			}
			if pts := downTrack.PointsBetweenOffsets(entryDown, p.DownAt); len(pts) >= 2 {
				bases = append(bases, baseSegment{pts})
				allBase = append(allBase, pts...)
			}

			if len(bases) == 0 {
				continue
			}
		}

		offset := platformWidthWorld/2 + platformGapWorld

		type candidate struct {
			ribbons  [][]Point2D
			boxes    []bboxF
			centroid Point2D
		}

		makeSide := func(sign float64) candidate {
			var ribbons [][]Point2D
			var boxes []bboxF
			var pts []Point2D
			for _, seg := range bases {
				r := OffsetRibbon(seg.points, sign*offset)
				ribbons = append(ribbons, r)
				boxes = append(boxes, bboxOf(r))
				pts = append(pts, r...)
			}
			return candidate{ribbons, boxes, averagePoint(pts)}
		}

		posCandidate := makeSide(+1)
		negCandidate := makeSide(-1)

		// Collect which platform-track sections this platform sits on,
		// so we don't penalise a ribbon for overlapping its own base track.
		ownTracks := map[string]bool{p.UpTrack: true, p.DownTrack: true}

		// Count how many OTHER tracks each side overlaps.
		countTrackHits := func(c candidate) int {
			n := 0
			for _, box := range c.boxes {
				padded := padBox(box, platformOverlapMargin)
				for ti, tb := range trackBoxes {
					if ownTracks[m.tracks[ti].Section.Name] {
						continue
					}
					if boxesOverlap(padded, tb) {
						n++
					}
				}
			}
			return n
		}

		countPlatformCollisions := func(c candidate) int {
			n := 0
			for _, box := range c.boxes {
				padded := padBox(box, platformOverlapMargin)
				for _, existing := range placedBoxes {
					if boxesOverlap(padded, existing) {
						n++
					}
				}
			}
			return n
		}

		posTrackHits := countTrackHits(posCandidate)
		negTrackHits := countTrackHits(negCandidate)

		var chosen candidate
		switch {
		case posTrackHits < negTrackHits:
			// Positive side avoids other tracks better — use it.
			chosen = posCandidate
		case negTrackHits < posTrackHits:
			// Negative side avoids other tracks better — use it.
			chosen = negCandidate
		default:
			// Both sides hit the same number of other tracks (or none).
			// Fall back to platform-collision count, then centroid distance.
			posPC := countPlatformCollisions(posCandidate)
			negPC := countPlatformCollisions(negCandidate)
			if negPC < posPC {
				chosen = negCandidate
			} else if posPC < negPC {
				chosen = posCandidate
			} else if len(placedCentroids) > 0 {
				minDistPos := math.Inf(1)
				minDistNeg := math.Inf(1)
				for _, c := range placedCentroids {
					if d := dist(posCandidate.centroid, c); d < minDistPos {
						minDistPos = d
					}
					if d := dist(negCandidate.centroid, c); d < minDistNeg {
						minDistNeg = d
					}
				}
				if minDistNeg > minDistPos {
					chosen = negCandidate
				} else {
					chosen = posCandidate
				}
			} else {
				chosen = posCandidate
			}
		}

		for _, box := range chosen.boxes {
			placedBoxes = append(placedBoxes, box)
		}
		placedCentroids = append(placedCentroids, chosen.centroid)

		m.platformDrawables = append(m.platformDrawables, platformDrawable{
			name:       p.Name,
			crs:        p.StationCRS,
			footprints: chosen.ribbons,
			isRibbon:   true,
			labelPt:    averagePoint(allBase),
		})
	}
}

// rebuildClusters groups objPlacements whose screen positions land in the
// same screen-space bucket. Called from renderer.rebuildStatic (i.e. only
// when geometryDirty — on data load, pan, or zoom), since cluster
// membership only depends on the camera, not on hover state.
func (m *MapWidget) rebuildClusters() {
	size := m.Size()

	type accum struct {
		sumX, sumY float32
		members    []*objPlacement
	}
	buckets := map[[2]int]*accum{}
	var order [][2]int

	for i := range m.objPlacements {
		p := &m.objPlacements[i]
		if !p.ok {
			continue
		}
		sx, sy := m.cam.WorldToScreen(p.pt, size.Width, size.Height)
		key := [2]int{int(sx / clusterCellPx), int(sy / clusterCellPx)}

		b, exists := buckets[key]
		if !exists {
			b = &accum{}
			buckets[key] = b
			order = append(order, key)
		}
		b.sumX += sx
		b.sumY += sy
		b.members = append(b.members, p)
	}

	m.objectClusters = m.objectClusters[:0]
	for _, key := range order {
		b := buckets[key]
		n := float32(len(b.members))
		m.objectClusters = append(m.objectClusters, objectCluster{
			ScreenPos: fyne.NewPos(b.sumX/n, b.sumY/n),
			Members:   b.members,
		})
	}
}

// --- fyne.Widget plumbing -------------------------------------------------

func (m *MapWidget) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(colorBackground)
	hint := canvas.NewText("Ctrl+V to paste track data", color.NRGBA{R: 0xaa, G: 0xaa, B: 0xaa, A: 0xff})
	hint.Alignment = fyne.TextAlignCenter
	hint.TextSize = 16

	tooltipTitle := canvas.NewText("", colorObjectHover)
	tooltipTitle.TextSize = 13
	tooltipTitle.TextStyle = fyne.TextStyle{Bold: true}

	tooltipDetail := canvas.NewText("", colorLabel)
	tooltipDetail.TextSize = 12

	tooltipBg := canvas.NewRectangle(colorPanelBg)
	tooltipBg.Hide()
	tooltipTitle.Hide()
	tooltipDetail.Hide()

	r := &mapRenderer{
		widget:        m,
		bg:            bg,
		hint:          hint,
		tooltipBg:     tooltipBg,
		tooltipTitle:  tooltipTitle,
		tooltipDetail: tooltipDetail,
	}
	return r
}

// --- mouse interaction -----------------------------------------------------
// Fyne dispatches scroll via desktop.Scrollable, drag via fyne.Draggable,
// mouse-move-without-drag via desktop.Hoverable, and clicks (used here for
// the measurement tool) via desktop.Mouseable.

func (m *MapWidget) Scrolled(ev *fyne.ScrollEvent) {
	if !m.HasData() {
		return
	}

	// Raw scroll deltas vary a lot between mice (small, discrete steps) and
	// trackpads (large, continuous pixel-ish values), so a plain linear
	// "factor := 1 + DY*k" makes trackpads feel wildly fast. Clamping the
	// input delta before converting to a zoom factor keeps each scroll
	// "step" feeling roughly the same size regardless of device, and the
	// exponential curve (rather than linear) keeps zooming smooth at both
	// the very-zoomed-in and very-zoomed-out ends.
	delta := float64(ev.Scrolled.DY)
	const maxDelta = 40.0
	if delta > maxDelta {
		delta = maxDelta
	}
	if delta < -maxDelta {
		delta = -maxDelta
	}

	const sensitivity = 0.0015
	factor := math.Exp(delta * sensitivity)

	size := m.Size()
	m.cam.ZoomAt(factor, ev.Position.X, ev.Position.Y, size.Width, size.Height)
	m.geometryDirty = true
	m.Refresh()
}

func (m *MapWidget) Dragged(ev *fyne.DragEvent) {
	if !m.HasData() {
		return
	}
	m.cam.Pan(ev.Dragged.DX, ev.Dragged.DY)
	m.clearHover() // hide tooltip while dragging
	m.geometryDirty = true
	m.Refresh()
}

func (m *MapWidget) DragEnd() {}

func (m *MapWidget) MouseIn(ev *desktop.MouseEvent) {}

func (m *MapWidget) MouseMoved(ev *desktop.MouseEvent) {
	if !m.HasData() {
		return
	}
	m.hoverPos = ev.Position
	m.updateHover(ev.Position)
	// Deliberately NOT setting geometryDirty here — hover and the live
	// measurement preview only change the small overlay layer, which
	// mapRenderer rebuilds every Refresh() regardless. The expensive
	// track/marker geometry is left untouched.
	m.Refresh()
}

func (m *MapWidget) MouseOut() {
	m.clearHover()
	m.Refresh()
}

// MouseDown implements desktop.Mouseable. Right-click drives the distance
// measurement tool: 1st click sets the start point, 2nd sets the end
// point and locks the measurement in, 3rd starts a fresh measurement.
// Left-click is left alone since Draggable already owns it for panning.
func (m *MapWidget) MouseDown(ev *desktop.MouseEvent) {
	if !m.HasData() || ev.Button != desktop.MouseButtonSecondary {
		return
	}

	mp := m.snapToTrack(ev.Position)

	switch {
	case m.measureStart == nil, m.measureEnd != nil:
		m.measureStart = &mp
		m.measureEnd = nil
	default:
		m.measureEnd = &mp
	}
	m.Refresh()
}

func (m *MapWidget) MouseUp(ev *desktop.MouseEvent) {}

// snapToTrack resolves a screen position to a MeasurePoint, snapping to
// the nearest sampled track point within hoverPickPixels if one exists,
// so measurements are reported along-the-track rather than as a raw
// straight-line click-to-click distance whenever possible.
func (m *MapWidget) snapToTrack(screenPos fyne.Position) MeasurePoint {
	size := m.Size()
	worldPt := m.cam.ScreenToWorld(screenPos.X, screenPos.Y, size.Width, size.Height)

	if gp, ok := m.findNearestTrackPoint(screenPos); ok {
		return MeasurePoint{WorldPt: gp.pt, Track: gp.trackName, Offset: gp.offset, OnTrack: true}
	}
	return MeasurePoint{WorldPt: worldPt, OnTrack: false}
}

// findNearestTrackPoint is the shared "what track point is under the
// cursor" lookup used by both hover and the measurement tool.
func (m *MapWidget) findNearestTrackPoint(screenPos fyne.Position) (gridPoint, bool) {
	if m.grid == nil {
		return gridPoint{}, false
	}

	size := m.Size()
	cursorWorld := m.cam.ScreenToWorld(screenPos.X, screenPos.Y, size.Width, size.Height)
	worldRadius := float64(hoverPickPixels) / m.cam.Zoom
	candidates := m.grid.query(cursorWorld, worldRadius)

	bestDist := float32(hoverPickPixels)
	var best gridPoint
	found := false

	for _, c := range candidates {
		sx, sy := m.cam.WorldToScreen(c.pt, size.Width, size.Height)
		d := dist2D(sx, sy, screenPos.X, screenPos.Y)
		if d < bestDist {
			bestDist = d
			best = c
			found = true
		}
	}

	return best, found
}

// findHoveredPlatform returns the platform (if any) whose footprint
// polygon contains the given screen position, used so hovering a
// platform's filled area shows its name — platforms have no track
// samples of their own to hit-test against otherwise.
func (m *MapWidget) findHoveredPlatform(screenPos fyne.Position) *platformDrawable {
	size := m.Size()
	worldPt := m.cam.ScreenToWorld(screenPos.X, screenPos.Y, size.Width, size.Height)

	for i := range m.platformDrawables {
		pd := &m.platformDrawables[i]
		for _, fp := range pd.footprints {
			if pointInPolygon(worldPt, fp) {
				return pd
			}
		}
	}
	return nil
}

// updateHover finds the nearest object cluster, platform, or track point
// to the cursor and sets the tooltip fields accordingly. Priority order:
// object clusters first (most specific thing to inspect), then platform
// footprints, then bare track points.
func (m *MapWidget) updateHover(screenPos fyne.Position) {
	m.clearHover()

	bestClusterDist := float32(hoverPickPixels)
	var bestCluster *objectCluster

	for i := range m.objectClusters {
		c := &m.objectClusters[i]
		d := dist2D(c.ScreenPos.X, c.ScreenPos.Y, screenPos.X, screenPos.Y)
		if d < bestClusterDist {
			bestClusterDist = d
			bestCluster = c
		}
	}

	if bestCluster != nil {
		m.hoverIsObject = true
		m.hoverCluster = bestCluster
		if len(bestCluster.Members) == 1 {
			p := bestCluster.Members[0]
			m.hoverInfo = p.obj.Name
			m.hoverDetail = fmt.Sprintf("%s · %s", p.obj.Type, directionLabel(p.obj.Direction))
		} else {
			names := make([]string, len(bestCluster.Members))
			for i, p := range bestCluster.Members {
				names[i] = fmt.Sprintf("%s [%s]", p.obj.Name, p.obj.Type)
			}
			sort.Strings(names)
			m.hoverInfo = fmt.Sprintf("%d overlapping objects", len(names))
			m.hoverDetail = joinWithSep(names, "  ·  ")
		}
		return
	}

	if pd := m.findHoveredPlatform(screenPos); pd != nil {
		m.hoverPlatform = pd
		m.hoverIsObject = false
		m.hoverInfo = pd.name
		m.hoverDetail = fmt.Sprintf("Platform · %s", pd.crs)
		return
	}

	if gp, ok := m.findNearestTrackPoint(screenPos); ok {
		m.hoverInfo = gp.trackName
		m.hoverDetail = fmt.Sprintf("%.1fm from up end", gp.offset)
		m.hoverIsObject = false
		m.hoverWorldPt = gp.pt
	}
}

func directionLabel(d string) string {
	switch d {
	case "up":
		return "facing up"
	case "down":
		return "facing down"
	case "both":
		return "both directions"
	default:
		return "direction unknown"
	}
}

func joinWithSep(items []string, sep string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += sep
		}
		out += s
	}
	return out
}

func (m *MapWidget) findSampledTrack(name string) *SampledTrack {
	for i := range m.tracks {
		if m.tracks[i].Section.Name == name {
			return &m.tracks[i]
		}
	}
	return nil
}

// buildTrackGraph connects track sections that physically meet, so
// measurements can span junctions instead of being limited to a single
// section. Each track contributes two nodes (its two ends); an "intrinsic"
// edge joins a track's own two ends with weight = its length, and a
// "coincidence" edge (weight 0) joins any two different tracks' ends that
// land within endpointEpsilon of each other.
func (m *MapWidget) buildTrackGraph() {
	m.trackGraph = make(map[trackEndNode][]trackEdge)

	add := func(a, b trackEndNode, w float64) {
		m.trackGraph[a] = append(m.trackGraph[a], trackEdge{b, w})
		m.trackGraph[b] = append(m.trackGraph[b], trackEdge{a, w})
	}

	for _, t := range m.tracks {
		if len(t.Points) == 0 {
			continue
		}
		add(trackEndNode{t.Section.Name, 0}, trackEndNode{t.Section.Name, 1}, t.Length)
	}

	for i := range m.tracks {
		ti := &m.tracks[i]
		if len(ti.Points) == 0 {
			continue
		}
		endsI := [2]Point2D{ti.Points[0], ti.Points[len(ti.Points)-1]}

		for j := i + 1; j < len(m.tracks); j++ {
			tj := &m.tracks[j]
			if len(tj.Points) == 0 {
				continue
			}
			endsJ := [2]Point2D{tj.Points[0], tj.Points[len(tj.Points)-1]}

			for a := 0; a < 2; a++ {
				for b := 0; b < 2; b++ {
					if dist(endsI[a], endsJ[b]) <= endpointEpsilon {
						add(trackEndNode{ti.Section.Name, a}, trackEndNode{tj.Section.Name, b}, 0)
					}
				}
			}
		}
	}
}

// shortestPath runs Dijkstra over the track connectivity graph between
// two track-end nodes. The graph is small (2 nodes per track section), so
// a plain O(V^2) scan is plenty fast — no need for a priority queue.
func (m *MapWidget) shortestPath(start, end trackEndNode) (float64, []trackEndNode, bool) {
	if start == end {
		return 0, []trackEndNode{start}, true
	}

	dist := map[trackEndNode]float64{start: 0}
	prev := map[trackEndNode]trackEndNode{}
	visited := map[trackEndNode]bool{}

	for {
		var u trackEndNode
		best := math.Inf(1)
		found := false
		for n, d := range dist {
			if visited[n] {
				continue
			}
			if d < best {
				best = d
				u = n
				found = true
			}
		}
		if !found || u == end {
			break
		}
		visited[u] = true

		for _, e := range m.trackGraph[u] {
			nd := dist[u] + e.weight
			if old, ok := dist[e.to]; !ok || nd < old {
				dist[e.to] = nd
				prev[e.to] = u
			}
		}
	}

	d, ok := dist[end]
	if !ok {
		return 0, nil, false
	}

	path := []trackEndNode{end}
	cur := end
	for cur != start {
		p, ok := prev[cur]
		if !ok {
			return 0, nil, false
		}
		path = append(path, p)
		cur = p
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return d, path, true
}

// findMeasurePath resolves a measurement between two snapped points into
// a sequence of per-track segments plus a total distance. If both points
// are on the same track, it's a single segment. Otherwise it tries all
// four combinations of "which end of the start track" / "which end of
// the end track" to leave from, runs the connectivity graph for each, and
// keeps whichever combination gives the shortest total path. Returns
// ok=false if either point isn't on a track, or no connecting path
// exists (caller should fall back to straight-line distance).
func (m *MapWidget) findMeasurePath(start, end *MeasurePoint) (measurePath, bool) {
	if !start.OnTrack || !end.OnTrack {
		return measurePath{}, false
	}

	if start.Track == end.Track {
		return measurePath{
			segments: []pathSegment{{track: start.Track, from: start.Offset, to: end.Offset}},
			total:    math.Abs(end.Offset - start.Offset),
		}, true
	}

	ta := m.findSampledTrack(start.Track)
	tb := m.findSampledTrack(end.Track)
	if ta == nil || tb == nil {
		return measurePath{}, false
	}

	type option struct {
		startEnd, endEnd int
		lead, trail      float64
	}
	options := []option{
		{0, 0, start.Offset, end.Offset},
		{0, 1, start.Offset, tb.Length - end.Offset},
		{1, 0, ta.Length - start.Offset, end.Offset},
		{1, 1, ta.Length - start.Offset, tb.Length - end.Offset},
	}

	bestTotal := math.Inf(1)
	var bestPath []trackEndNode
	var bestOpt option
	for _, opt := range options {
		d, path, ok := m.shortestPath(trackEndNode{start.Track, opt.startEnd}, trackEndNode{end.Track, opt.endEnd})
		if !ok {
			continue
		}
		total := opt.lead + d + opt.trail
		if total < bestTotal {
			bestTotal = total
			bestPath = path
			bestOpt = opt
		}
	}
	if bestPath == nil {
		return measurePath{}, false // not connected — caller falls back to straight-line
	}

	var segs []pathSegment

	if bestOpt.startEnd == 0 {
		segs = append(segs, pathSegment{start.Track, start.Offset, 0})
	} else {
		segs = append(segs, pathSegment{start.Track, start.Offset, ta.Length})
	}

	// Any intermediate track sections fully traversed show up in the
	// Dijkstra path as a same-track node pair; cross-track pairs are
	// zero-weight junction jumps with no geometry of their own.
	for i := 0; i+1 < len(bestPath); i++ {
		a, b := bestPath[i], bestPath[i+1]
		if a.track != b.track {
			continue
		}
		tr := m.findSampledTrack(a.track)
		if tr == nil {
			continue
		}
		from, to := 0.0, tr.Length
		if a.end == 1 {
			from, to = tr.Length, 0
		}
		segs = append(segs, pathSegment{a.track, from, to})
	}

	if bestOpt.endEnd == 0 {
		segs = append(segs, pathSegment{end.Track, 0, end.Offset})
	} else {
		segs = append(segs, pathSegment{end.Track, tb.Length, end.Offset})
	}

	return measurePath{segments: segs, total: bestTotal}, true
}

func dist2D(x1, y1, x2, y2 float32) float32 {
	dx := x1 - x2
	dy := y1 - y2
	return float32(math.Sqrt(float64(dx*dx + dy*dy)))
}

// --- small geometry helpers used by platform drawing -----------------------

func averagePoint(points []Point2D) Point2D {
	if len(points) == 0 {
		return Point2D{}
	}
	var sumX, sumZ float64
	for _, p := range points {
		sumX += p.X
		sumZ += p.Z
	}
	n := float64(len(points))
	return Point2D{X: sumX / n, Z: sumZ / n}
}

// bboxF is an axis-aligned world-space bounding box, used for the
// platform overlap heuristic.
type bboxF struct {
	minX, maxX, minZ, maxZ float64
}

func bboxOf(points []Point2D) bboxF {
	if len(points) == 0 {
		return bboxF{}
	}
	b := bboxF{minX: points[0].X, maxX: points[0].X, minZ: points[0].Z, maxZ: points[0].Z}
	for _, p := range points[1:] {
		if p.X < b.minX {
			b.minX = p.X
		}
		if p.X > b.maxX {
			b.maxX = p.X
		}
		if p.Z < b.minZ {
			b.minZ = p.Z
		}
		if p.Z > b.maxZ {
			b.maxZ = p.Z
		}
	}
	return b
}

func boxesOverlap(a, b bboxF) bool {
	return a.minX <= b.maxX && a.maxX >= b.minX && a.minZ <= b.maxZ && a.maxZ >= b.minZ
}

func padBox(b bboxF, margin float64) bboxF {
	return bboxF{minX: b.minX - margin, maxX: b.maxX + margin, minZ: b.minZ - margin, maxZ: b.maxZ + margin}
}

// pointInPolygon is a standard even-odd ray-casting test, used to hit-test
// the cursor against a platform's filled footprint polygon.
func pointInPolygon(p Point2D, poly []Point2D) bool {
	n := len(poly)
	if n < 3 {
		return false
	}
	inside := false
	j := n - 1
	for i := 0; i < n; i++ {
		pi, pj := poly[i], poly[j]
		if (pi.Z > p.Z) != (pj.Z > p.Z) {
			slope := (p.Z - pi.Z) / (pj.Z - pi.Z)
			xCross := pi.X + slope*(pj.X-pi.X)
			if p.X < xCross {
				inside = !inside
			}
		}
		j = i
	}
	return inside
}
