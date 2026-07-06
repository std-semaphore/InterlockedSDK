package main

import (
	"fmt"

	"github.com/pelletier/go-toml/v2"
)

// --- Raw TOML shape -----------------------------------------------------
// These structs mirror the exact TOML produced by the Godot exporter plugin.
// Field names matter for unmarshalling (toml tags), nesting matters for
// the dotted-table keys (e.g. [section.T16], [object.Signal1]).

type rawDoc struct {
	Section map[string]rawSection `toml:"section"`
	Object  map[string]rawObject  `toml:"object"`
	Station map[string]rawStation `toml:"station"`
}

type rawSection struct {
	CurveData  []rawCurvePoint `toml:"curve_data"`
	IsPoints   bool            `toml:"isPoints"`
	SpeedLimit []rawSpeedLimit `toml:"speed_limit"`
}

type rawCurvePoint struct {
	Position [3]float64 `toml:"position"`
	In       [3]float64 `toml:"in"`
	Out      [3]float64 `toml:"out"`
}

type rawSpeedLimit struct {
	AppliesFrom    float64 `toml:"appliesFrom"`
	PermittedSpeed float64 `toml:"permittedSpeed"`
}

type rawObject struct {
	Type         string  `toml:"type"`
	TrackSection string  `toml:"track_section"`
	At           float64 `toml:"at"`
	Direction    string  `toml:"direction"`
}

type rawStation struct {
	Platforms map[string]rawPlatform `toml:"platforms"`
}

type rawPlatform struct {
	UpBoundary       rawBoundary  `toml:"upBoundary"`
	DownBoundary     rawBoundary  `toml:"downBoundary"`
	PlatformGeometry [][3]float64 `toml:"platformGeometry"`
}

type rawBoundary struct {
	TrackSection string  `toml:"track_section"`
	At           float64 `toml:"at"`
}

// --- Parsed/usable model -------------------------------------------------
// Position/In/Out are kept in X/Z (Y is always 0 in this dataset — it's a
// top-down track plan), converted to plain 2D points for drawing.

type Point2D struct {
	X, Z float64
}

type CurvePoint struct {
	Pos Point2D
	In  Point2D // tangent handle, relative to Pos (matches Godot's Curve3D convention)
	Out Point2D
}

type TrackSection struct {
	Name       string
	IsPoints   bool
	Curve      []CurvePoint
	SpeedLimit []SpeedLimit
}

type SpeedLimit struct {
	AppliesFrom    float64
	PermittedSpeed float64
}

type Object struct {
	Name         string
	Type         string
	TrackSection string
	At           float64
	Direction    string
}

type Platform struct {
	StationCRS string
	Name       string
	UpTrack    string
	UpAt       float64
	DownTrack  string
	DownAt     float64
	Geometry   []Point2D
}

type TrackData struct {
	Sections  []TrackSection
	Objects   []Object
	Platforms []Platform
}

func toPoint2D(v [3]float64) Point2D {
	// data is [x, y, z]; y is always 0 for this top-down dataset, so we
	// drop it and keep x/z as the 2D plane.
	return Point2D{X: v[0], Z: v[2]}
}

// ParseTrackData parses the pasted TOML text into the renderable model.
// Map iteration order isn't stable in Go, so output ordering of sections/
// objects/platforms doesn't matter for rendering (each is positioned by
// its own coordinates, not by list order), but we sort names for any
// future list/debug display so it's not visually random run to run.
func ParseTrackData(text string) (*TrackData, error) {
	var doc rawDoc
	if err := toml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, fmt.Errorf("toml parse error: %w", err)
	}

	data := &TrackData{}

	for name, sec := range doc.Section {
		ts := TrackSection{
			Name:     name,
			IsPoints: sec.IsPoints,
		}
		for _, cp := range sec.CurveData {
			ts.Curve = append(ts.Curve, CurvePoint{
				Pos: toPoint2D(cp.Position),
				In:  toPoint2D(cp.In),
				Out: toPoint2D(cp.Out),
			})
		}
		for _, sl := range sec.SpeedLimit {
			ts.SpeedLimit = append(ts.SpeedLimit, SpeedLimit{
				AppliesFrom:    sl.AppliesFrom,
				PermittedSpeed: sl.PermittedSpeed,
			})
		}
		// Make sure every section's curve runs right-to-left ("up"),
		// since object "at" offsets and PointAtOffset both assume
		// offset=0 is the up end. See normalizeDirection in curve.go for
		// why this matters and its limitations.
		data.Sections = append(data.Sections, ts)
	}

	for name, obj := range doc.Object {
		data.Objects = append(data.Objects, Object{
			Name:         name,
			Type:         obj.Type,
			TrackSection: obj.TrackSection,
			At:           obj.At,
			Direction:    obj.Direction,
		})
	}

	for stationCRS, station := range doc.Station {
		for platformName, p := range station.Platforms {
			platform := Platform{
				StationCRS: stationCRS,
				Name:       platformName,
				UpTrack:    p.UpBoundary.TrackSection,
				UpAt:       p.UpBoundary.At,
				DownTrack:  p.DownBoundary.TrackSection,
				DownAt:     p.DownBoundary.At,
			}
			for _, g := range p.PlatformGeometry {
				platform.Geometry = append(platform.Geometry, toPoint2D(g))
			}
			data.Platforms = append(data.Platforms, platform)
		}
	}

	if len(data.Sections) == 0 {
		return nil, fmt.Errorf("no [section.*] tables found — is this the right data?")
	}

	return data, nil
}
