package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

type unitPerformance struct {
	MassTonnes          float64
	MaxSpeedMph         float64
	MaxTractiveEffortKn float64
	ContinuousPowerKw   float64
	ServiceBrakeRate    float64
}

type tiplocPosition struct {
	ID     string
	Track  string
	Offset float64
}

type profileGraph struct {
	tracks []SampledTrack
	graph  map[trackEndNode][]trackEdge
}

func timingProfileCmd() *cobra.Command {
	var dir string
	var dataPath string
	var maxSpeed float64

	cmd := &cobra.Command{
		Use:   "timing-profile [path]",
		Short: "Generate timing profiles for every timing group from TIPLOCs and track data",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				dir = args[0]
			}
			if dir == "" {
				dir, _ = os.Getwd()
			}
			if dataPath == "" {
				exePath, err := os.Executable()
				if err != nil {
					return fmt.Errorf("get executable path: %w", err)
				}
				dataPath = filepath.Join(filepath.Dir(exePath), "data", "kestby.toml")
			}

			trackData, err := openFile(dataPath)
			if err != nil {
				return fmt.Errorf("load track data %s: %w", dataPath, err)
			}
			tiplocs, err := loadTiplocDefs(dir)
			if err != nil {
				return err
			}

			graph := newProfileGraph(trackData)
			positions := locateTimingProfileTIPLOCs(tiplocs, trackData)
			if len(positions) < 2 {
				return fmt.Errorf("need at least two TIPLOCs with map positions; found %d", len(positions))
			}

			groupsDir := filepath.Join("TimingProfiles", "Groups")
			files, err := os.ReadDir(groupsDir)
			if err != nil {
				return fmt.Errorf("read %s: %w", groupsDir, err)
			}

			wrote := 0
			for _, file := range files {
				if file.IsDir() || filepath.Ext(file.Name()) != ".toml" {
					continue
				}

				groupPath := filepath.Join(groupsDir, file.Name())
				perf, err := readUnitPerformance(groupPath)
				if err != nil {
					return err
				}

				if maxSpeed > 0 && maxSpeed < perf.MaxSpeedMph {
					perf.MaxSpeedMph = maxSpeed
				}

				id := strings.TrimSuffix(file.Name(), ".toml")
				description := fmt.Sprintf("%s generated timing profile", id)

				groupFileContent, err := os.ReadFile(groupPath)
				if err != nil {
					return fmt.Errorf("read %s: %w", groupPath, err)
				}
				unitRegex := regexp.MustCompile(`(?m)^Units\s*=\s*\[(.*?)\]`)
				unitMatch := unitRegex.FindStringSubmatch(string(groupFileContent))
				var units []string
				if len(unitMatch) > 1 {
					unitStr := unitMatch[1]
					unitStr = strings.ReplaceAll(unitStr, `"`, "")
					for _, u := range strings.Split(unitStr, ",") {
						units = append(units, strings.TrimSpace(u))
					}
				}
				if len(units) == 0 {
					units = []string{id}
				}

				content, count := buildTimingProfileTOML(id, description, units, perf, graph, positions)

				out := filepath.Join(dir, "TimingProfiles", id+".toml")
				if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
					return fmt.Errorf("create %s: %w", filepath.Dir(out), err)
				}
				if err := os.WriteFile(out, []byte(content), 0o644); err != nil {
					return fmt.Errorf("write %s: %w", out, err)
				}

				fmt.Fprintf(os.Stderr, "✓ %s (%d segments)\n", id, count)
				wrote++
			}

			if wrote == 0 {
				return fmt.Errorf("no timing groups found in %s", groupsDir)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&dir, "dir", "d", "", "Timetable directory (default: current directory)")
	cmd.Flags().StringVar(&dataPath, "track-data", "", "Track data file (default: ./data/kestby.toml relative to binary)")
	cmd.Flags().Float64Var(&maxSpeed, "max-speed", 0, "Maximum train speed in mph, capped against each timing group's MaxSpeedMph")
	return cmd
}

func readUnitPerformance(path string) (unitPerformance, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return unitPerformance{}, fmt.Errorf("read %s: %w", path, err)
	}
	text := string(data)
	get := func(name string) (float64, error) {
		re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `\s*=\s*([-+]?[0-9]*\.?[0-9]+)`)
		m := re.FindStringSubmatch(text)
		if len(m) != 2 {
			return 0, fmt.Errorf("%s: missing %s", path, name)
		}
		v, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			return 0, fmt.Errorf("%s: parse %s: %w", path, name, err)
		}
		return v, nil
	}

	mass, err := get("MassTonnes")
	if err != nil {
		return unitPerformance{}, err
	}
	maxSpeed, err := get("MaxSpeedMph")
	if err != nil {
		return unitPerformance{}, err
	}
	te, err := get("MaxTractiveEffortKn")
	if err != nil {
		return unitPerformance{}, err
	}
	power, err := get("ContinuousPowerKw")
	if err != nil {
		return unitPerformance{}, err
	}
	brake, err := get("ServiceBrakeRateMps2")
	if err != nil {
		return unitPerformance{}, err
	}

	return unitPerformance{
		MassTonnes:          mass,
		MaxSpeedMph:         maxSpeed,
		MaxTractiveEffortKn: te,
		ContinuousPowerKw:   power,
		ServiceBrakeRate:    brake,
	}, nil
}

func newProfileGraph(data *TrackData) profileGraph {
	g := profileGraph{graph: map[trackEndNode][]trackEdge{}}
	for i := range data.Sections {
		g.tracks = append(g.tracks, SampleTrack(&data.Sections[i]))
	}
	add := func(a, b trackEndNode, w float64) {
		g.graph[a] = append(g.graph[a], trackEdge{b, w})
		g.graph[b] = append(g.graph[b], trackEdge{a, w})
	}
	for _, t := range g.tracks {
		if len(t.Points) > 0 {
			add(trackEndNode{t.Section.Name, 0}, trackEndNode{t.Section.Name, 1}, t.Length)
		}
	}
	for i := range g.tracks {
		ti := &g.tracks[i]
		if len(ti.Points) == 0 {
			continue
		}
		endsI := [2]Point2D{ti.Points[0], ti.Points[len(ti.Points)-1]}
		for j := i + 1; j < len(g.tracks); j++ {
			tj := &g.tracks[j]
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
	return g
}

func (g profileGraph) sampledTrack(name string) *SampledTrack {
	for i := range g.tracks {
		if g.tracks[i].Section.Name == name {
			return &g.tracks[i]
		}
	}
	return nil
}

func (g profileGraph) shortestPath(start, end trackEndNode) (float64, []trackEndNode, bool) {
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
			if !visited[n] && d < best {
				best = d
				u = n
				found = true
			}
		}
		if !found || u == end {
			break
		}
		visited[u] = true
		for _, e := range g.graph[u] {
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

func (g profileGraph) pathBetween(a, b tiplocPosition) ([]pathSegment, float64, bool) {
	if a.Track == b.Track {
		return []pathSegment{{track: a.Track, from: a.Offset, to: b.Offset}}, math.Abs(b.Offset - a.Offset), true
	}
	ta := g.sampledTrack(a.Track)
	tb := g.sampledTrack(b.Track)
	if ta == nil || tb == nil {
		return nil, 0, false
	}
	type option struct {
		startEnd, endEnd int
		lead, trail      float64
	}
	options := []option{
		{0, 0, a.Offset, b.Offset},
		{0, 1, a.Offset, tb.Length - b.Offset},
		{1, 0, ta.Length - a.Offset, b.Offset},
		{1, 1, ta.Length - a.Offset, tb.Length - b.Offset},
	}
	bestTotal := math.Inf(1)
	var bestPath []trackEndNode
	var bestOpt option
	for _, opt := range options {
		d, path, ok := g.shortestPath(trackEndNode{a.Track, opt.startEnd}, trackEndNode{b.Track, opt.endEnd})
		if ok && opt.lead+d+opt.trail < bestTotal {
			bestTotal = opt.lead + d + opt.trail
			bestPath = path
			bestOpt = opt
		}
	}
	if bestPath == nil {
		return nil, 0, false
	}
	var segs []pathSegment
	if bestOpt.startEnd == 0 {
		segs = append(segs, pathSegment{track: a.Track, from: a.Offset, to: 0})
	} else {
		segs = append(segs, pathSegment{track: a.Track, from: a.Offset, to: ta.Length})
	}
	for i := 0; i+1 < len(bestPath); i++ {
		x, y := bestPath[i], bestPath[i+1]
		if x.track != y.track {
			continue
		}
		tr := g.sampledTrack(x.track)
		if tr == nil {
			continue
		}
		from, to := 0.0, tr.Length
		if x.end == 1 {
			from, to = tr.Length, 0
		}
		segs = append(segs, pathSegment{track: x.track, from: from, to: to})
	}
	if bestOpt.endEnd == 0 {
		segs = append(segs, pathSegment{track: b.Track, from: 0, to: b.Offset})
	} else {
		segs = append(segs, pathSegment{track: b.Track, from: tb.Length, to: b.Offset})
	}
	return segs, bestTotal, true
}

// stationThroatMargin is how close (in the same units as track/section
// lengths — normally metres) a TIPLOC's position needs to be to a junction
// end for that junction to count as "at" that TIPLOC. Tune this if direct
// routes are being wrongly filtered (raise it) or bogus long routes are
// slipping through (lower it).
const stationThroatMargin = 250.0

// routeIsDirect reports whether the resolved path between positions[i] and
// positions[j] does not run through any other known TIPLOC position along
// the way. If it does, the pair isn't a genuine direct route — it's really
// two (or more) shorter segments overlapping end to end.
//
// Two checks are made:
//  1. Does the path literally traverse the exact track a third TIPLOC sits
//     on, passing its offset? (catches same-line pass-throughs)
//  2. Does the path exit through a junction/end-node that is directly or
//     transitively connected (via zero-weight "same physical point" edges)
//     to a track a third TIPLOC sits near? (catches pass-throughs where the
//     path takes a different line through the same station throat)
//
// routeIsDirect reports whether the resolved path is continuous and does not
// branch unexpectedly through complex junctions.
// routeIsDirect reports whether the resolved path between positions[i] and
// positions[j] is a continuous path without branching through junctions
// where the path could split to other destinations.
// routeIsDirect reports whether the resolved path doesn't make any illegal
// 180-degree reversals at junctions (arriving on one track and exiting back
// the same way via a different track through the same junction).
// routeIsDirect reports whether the resolved path always moves forward in a
// consistent direction and never backs up on itself through junctions.
// routeIsDirect reports whether the resolved path between positions[i] and
// positions[j] does not run through any other known TIPLOC position along
// the way. If it does, the pair isn't a genuine direct route — it's really
// two (or more) shorter segments overlapping end to end.

// pathPassesThrough reports whether pos lies on the resolved path: either
// directly on a track the path traverses (between that segment's from/to),
// or just beyond a junction the path exits through, when pos sits within
// stationThroatMargin of the corresponding end of its own track and that
// end is part of the same physical junction (zero-weight cluster).
func (g profileGraph) pathPassesThrough(segs []pathSegment, pos tiplocPosition) bool {
	const eps = 1e-6
	for _, seg := range segs {
		if seg.track == pos.Track {
			lo, hi := math.Min(seg.from, seg.to), math.Max(seg.from, seg.to)
			if pos.Offset >= lo-eps && pos.Offset <= hi+eps {
				return true
			}
		}

		exitNode, ok := g.segmentExitNode(seg)
		if !ok {
			continue
		}
		cluster := g.zeroWeightCluster(exitNode)

		tr := g.sampledTrack(pos.Track)
		if tr == nil {
			continue
		}
		if pos.Offset <= stationThroatMargin && cluster[trackEndNode{pos.Track, 0}] {
			return true
		}
		if pos.Offset >= tr.Length-stationThroatMargin && cluster[trackEndNode{pos.Track, 1}] {
			return true
		}
	}
	return false
}

// segmentExitNode returns the track-end node a segment exits through, if it
// reaches all the way to one end of its track. Segments that stop partway
// along a track (i.e. terminate at a TIPLOC's own offset) return ok=false.
func (g profileGraph) segmentExitNode(seg pathSegment) (trackEndNode, bool) {
	tr := g.sampledTrack(seg.track)
	if tr == nil {
		return trackEndNode{}, false
	}
	const eps = 0.01
	if math.Abs(seg.to) < eps {
		return trackEndNode{seg.track, 0}, true
	}
	if math.Abs(seg.to-tr.Length) < eps {
		return trackEndNode{seg.track, 1}, true
	}
	return trackEndNode{}, false
}

// zeroWeightCluster returns every node directly or transitively joined to
// start by zero-weight edges — i.e. every track end that is physically the
// same point (junctions, crossovers), regardless of which specific line the
// path took through that point.
func (g profileGraph) zeroWeightCluster(start trackEndNode) map[trackEndNode]bool {
	seen := map[trackEndNode]bool{start: true}
	queue := []trackEndNode{start}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, e := range g.graph[n] {
			if e.weight == 0 && !seen[e.to] {
				seen[e.to] = true
				queue = append(queue, e.to)
			}
		}
	}
	return seen
}

func (g profileGraph) pathSpeedLimit(segs []pathSegment, trainMaxMph float64) float64 {
	limit := trainMaxMph
	if limit <= 0 {
		limit = 25
	}
	for _, seg := range segs {
		tr := g.sampledTrack(seg.track)
		if tr == nil {
			continue
		}
		sectionLimit := speedLimitForSegment(tr.Section.SpeedLimit, math.Min(seg.from, seg.to), math.Max(seg.from, seg.to))
		if sectionLimit > 0 && sectionLimit < limit {
			limit = sectionLimit
		}
	}
	return limit
}

func speedLimitForSegment(limits []SpeedLimit, from, to float64) float64 {
	if len(limits) == 0 {
		return 0
	}
	sort.Slice(limits, func(i, j int) bool { return limits[i].AppliesFrom < limits[j].AppliesFrom })
	limit := 0.0
	for _, sl := range limits {
		if sl.AppliesFrom <= from {
			limit = sl.PermittedSpeed
		}
		if sl.AppliesFrom > from && sl.AppliesFrom <= to && (limit == 0 || sl.PermittedSpeed < limit) {
			limit = sl.PermittedSpeed
		}
	}
	if limit == 0 {
		limit = limits[0].PermittedSpeed
	}
	return limit
}

func estimatePassTime(distanceM, speedMph float64) float64 {
	v := mphToMPS(speedMph)
	if v <= 0 {
		return 0
	}
	return distanceM / v
}

func estimateStopTime(distanceM, speedMph float64, perf unitPerformance) float64 {
	v := mphToMPS(speedMph)
	if v <= 0 || distanceM <= 0 {
		return 0
	}
	accel := estimateAcceleration(perf, v)
	brake := perf.ServiceBrakeRate
	if brake <= 0 {
		brake = 0.7
	}
	accelDist := v * v / (2 * accel)
	brakeDist := v * v / (2 * brake)
	if accelDist+brakeDist <= distanceM {
		cruise := distanceM - accelDist - brakeDist
		return v/accel + cruise/v + v/brake
	}
	peak := math.Sqrt((2 * distanceM * accel * brake) / (accel + brake))
	return peak/accel + peak/brake
}

func estimateAcceleration(perf unitPerformance, targetSpeedMPS float64) float64 {
	if perf.MassTonnes <= 0 {
		return 0.35
	}
	forceAccel := perf.MaxTractiveEffortKn * 1000 / (perf.MassTonnes * 1000)
	powerAccel := forceAccel
	if perf.ContinuousPowerKw > 0 && targetSpeedMPS > 0 {
		powerAccel = perf.ContinuousPowerKw * 1000 / (perf.MassTonnes * 1000 * targetSpeedMPS)
	}
	accel := math.Min(forceAccel, powerAccel)
	if accel <= 0 {
		return 0.35
	}
	return accel
}

func mphToMPS(mph float64) float64 {
	return mph * 0.44704
}

func formatProfileDuration(seconds float64) string {
	rounded := int(math.Ceil(seconds/15.0)) * 15
	mins := rounded / 60
	rem := rounded % 60
	switch rem {
	case 0:
		return strconv.Itoa(mins)
	case 15:
		return fmt.Sprintf("%dQ", mins)
	case 30:
		return fmt.Sprintf("%dH", mins)
	default:
		return fmt.Sprintf("%dT", mins)
	}
}

func quoteStringList(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		quoted = append(quoted, strconv.Quote(v))
	}
	return strings.Join(quoted, ", ")
}

// routeIsDirect is no longer needed - accept all valid paths

func locateTimingProfileTIPLOCs(defs map[string]rawTiplocFile, data *TrackData) []tiplocPosition {
	var out []tiplocPosition

	// Add defined TIPLOCs
	for id, def := range defs {
		switch {
		case def.Custom != nil:
			out = append(out, tiplocPosition{ID: id, Track: def.Custom.Section, Offset: def.Custom.At})
		case def.Object != nil:
			for _, obj := range data.Objects {
				if obj.Name == def.Object.Object {
					out = append(out, tiplocPosition{ID: id, Track: obj.TrackSection, Offset: obj.At})
					break
				}
			}
		case def.Station != nil:
			if pos, ok := stationTimingPosition(def.Station.CRS, data); ok {
				pos.ID = id
				out = append(out, pos)
			}
		}
	}

	// Add fringe points (track ends that don't connect to other tracks and aren't blocked by buffers)
	g := newProfileGraph(data)
	bufferEnds := findBufferBlockedEnds(data)

	for _, track := range g.tracks {
		for end := 0; end < 2; end++ {
			node := trackEndNode{track.Section.Name, end}

			// Skip if blocked by buffer
			if bufferEnds[node] {
				continue
			}

			// If this node has no zero-weight edges to other tracks, it's a fringe
			isFringe := true
			for _, edge := range g.graph[node] {
				if edge.weight == 0 { // Zero-weight = connected junction
					isFringe = false
					break
				}
			}
			if isFringe {
				offset := 0.0
				if end == 1 {
					offset = track.Length
				}
				out = append(out, tiplocPosition{ID: track.Section.Name, Track: track.Section.Name, Offset: offset})
			}
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// findBufferBlockedEnds returns the set of track ends that have buffers, making them inaccessible
func findBufferBlockedEnds(data *TrackData) map[trackEndNode]bool {
	blocked := make(map[trackEndNode]bool)
	for _, obj := range data.Objects {
		if obj.Type == "BUFFER" {
			// Buffers block the end of a track, preventing routing through
			// Treat as blocking end 0 or 1 depending on buffer position
			// For simplicity, if a buffer exists on a track, consider that end blocked
			blocked[trackEndNode{obj.TrackSection, 0}] = true
			blocked[trackEndNode{obj.TrackSection, 1}] = true
		}
	}
	return blocked
}

func stationTimingPosition(crs string, data *TrackData) (tiplocPosition, bool) {
	var candidates []tiplocPosition
	for _, p := range data.Platforms {
		if p.StationCRS != crs {
			continue
		}
		// Collect all platforms for this station
		candidates = append(candidates, tiplocPosition{Track: p.UpTrack, Offset: p.UpAt})
		if p.UpTrack != p.DownTrack {
			candidates = append(candidates, tiplocPosition{Track: p.DownTrack, Offset: p.DownAt})
		}
	}
	if len(candidates) == 0 {
		return tiplocPosition{}, false
	}
	// Return first candidate
	return candidates[0], true
}

func buildTimingProfileTOML(id, description string, units []string, perf unitPerformance, graph profileGraph, positions []tiplocPosition) (string, int) {
	var b strings.Builder
	fmt.Fprintln(&b, "[profile]")
	fmt.Fprintf(&b, "description = %q\n", description)
	fmt.Fprintf(&b, "units = [%s]\n\n", quoteStringList(units))

	count := 0
	for i := 0; i < len(positions); i++ {
		for j := i + 1; j < len(positions); j++ {
			segs, total, ok := graph.pathBetween(positions[i], positions[j])
			if !ok {
				continue
			}
			speedMph := graph.pathSpeedLimit(segs, perf.MaxSpeedMph)
			passSecs := estimatePassTime(total, speedMph)
			stopSecs := estimateStopTime(total, speedMph, perf)
			fmt.Fprintln(&b, "[[segment]]")
			fmt.Fprintf(&b, "route = %q\n", positions[i].ID+":"+positions[j].ID)
			fmt.Fprintf(&b, "stop = %q\n", formatProfileDuration(stopSecs))
			fmt.Fprintf(&b, "pass = %q\n\n", formatProfileDuration(passSecs))
			count++
		}
	}
	return b.String(), count
}
