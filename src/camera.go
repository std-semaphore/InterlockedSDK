package main

// Camera maps world coordinates (X/Z from the track data) to screen pixels.
// World Z maps DIRECTLY to screen Y (no flip). Mapping Z straight onto Y
// keeps every direction (pan, zoom-at-cursor) naturally consistent — a
// flip here would need matching sign-flips in Pan/ScreenToWorld too, and
// getting any one of those wrong produces exactly the "reversed" feel this
// is fixing. If you want north-is-up instead of north-is-down, that's a
// cosmetic call for later — not worth the bug surface for a quick tool.
type Camera struct {
	// OffsetX/OffsetZ: world-space point currently at the center of the screen.
	OffsetX, OffsetZ float64
	Zoom             float64 // screen pixels per world unit
}

func NewCamera() *Camera {
	return &Camera{Zoom: 1.0}
}

// WorldToScreen converts a world point to screen pixel coordinates, given
// the current viewport size (so the camera offset is always screen-centered).
func (c *Camera) WorldToScreen(p Point2D, viewW, viewH float32) (float32, float32) {
	dx := (p.X - c.OffsetX) * c.Zoom
	dz := (p.Z - c.OffsetZ) * c.Zoom

	sx := float64(viewW)/2 + dx
	sy := float64(viewH)/2 + dz

	return float32(sx), float32(sy)
}

// ScreenToWorld is the inverse of WorldToScreen, used to keep the point
// under the cursor fixed while zooming, and to convert drag deltas to pan.
func (c *Camera) ScreenToWorld(sx, sy float32, viewW, viewH float32) Point2D {
	dx := float64(sx) - float64(viewW)/2
	dz := float64(sy) - float64(viewH)/2

	x := c.OffsetX + dx/c.Zoom
	z := c.OffsetZ + dz/c.Zoom

	return Point2D{X: x, Z: z}
}

// ZoomAt adjusts zoom by a multiplicative factor while keeping the world
// point currently under (sx, sy) visually fixed on screen.
func (c *Camera) ZoomAt(factor float64, sx, sy float32, viewW, viewH float32) {
	before := c.ScreenToWorld(sx, sy, viewW, viewH)

	c.Zoom *= factor
	if c.Zoom < 0.01 {
		c.Zoom = 0.01
	}
	if c.Zoom > 500 {
		c.Zoom = 500
	}

	after := c.ScreenToWorld(sx, sy, viewW, viewH)

	// shift offset by the world-space drift so `before` stays under the cursor
	c.OffsetX += before.X - after.X
	c.OffsetZ += before.Z - after.Z
}

// Pan shifts the camera by a screen-space pixel delta. Dragging the mouse
// right/down moves the visible content right/down, which means the
// world-space center-of-view moves left/up by the same screen delta
// (divided by zoom to convert pixels to world units) — hence the minus sign
// on both axes, with no extra flip since WorldToScreen no longer flips Z.
func (c *Camera) Pan(dxScreen, dyScreen float32) {
	c.OffsetX -= float64(dxScreen) / c.Zoom
	c.OffsetZ -= float64(dyScreen) / c.Zoom
}

// FitToBounds centers and scales the camera so all given points are visible
// with some margin, used once on data load.
func FitToBounds(points []Point2D, viewW, viewH float32) *Camera {
	c := NewCamera()

	if len(points) == 0 {
		return c
	}

	minX, maxX := points[0].X, points[0].X
	minZ, maxZ := points[0].Z, points[0].Z

	for _, p := range points {
		if p.X < minX {
			minX = p.X
		}
		if p.X > maxX {
			maxX = p.X
		}
		if p.Z < minZ {
			minZ = p.Z
		}
		if p.Z > maxZ {
			maxZ = p.Z
		}
	}

	c.OffsetX = (minX + maxX) / 2
	c.OffsetZ = (minZ + maxZ) / 2

	spanX := maxX - minX
	spanZ := maxZ - minZ
	if spanX < 1 {
		spanX = 1
	}
	if spanZ < 1 {
		spanZ = 1
	}

	const margin = 0.85 // leave 15% padding around the content
	zoomX := float64(viewW) * margin / spanX
	zoomZ := float64(viewH) * margin / spanZ

	if zoomX < zoomZ {
		c.Zoom = zoomX
	} else {
		c.Zoom = zoomZ
	}
	if c.Zoom <= 0 {
		c.Zoom = 1
	}

	return c
}
