package birdseye

import "math"

// The picture's frame and camera. North is up, as on an ordinary map. The
// camera stands south of the pin, looks down at it at Pitch, and aims at
// the point above the pin on the pinned building's roof (the ground when
// no footprint holds the pin), so that point is at (PinX, PinY) of every
// picture and a page's HTML marker needs no position of its own. Its
// distance grows with the radius, so every disc fills the frame alike.
const (
	Width  = 1600
	Height = 1000
	PinX   = 0.5
	PinY   = 0.58

	// distanceRatio is the camera's ground distance behind the pin per
	// metre of radius (520 m for a 360 m disc).
	distanceRatio = 520.0 / 360.0
	pitchDegrees  = 47.0
	// fill is how much of the frame's width the disc spans.
	fill = 1.02
)

// camera projects metres around the pin onto the frame.
type camera struct {
	d, h   float64 // the camera's ground distance south of the pin and height
	target float64 // the height it aims at above the pin
	cp, sp float64
	f      float64 // focal length in frame units
}

func newCamera(radius, target float64) camera {
	p := pitchDegrees * math.Pi / 180
	d := radius * distanceRatio
	h := d * math.Tan(p)
	return camera{
		d: d, h: h, target: target,
		cp: math.Cos(p), sp: math.Sin(p),
		f: Width / (2 * radius) * fill * math.Hypot(d, h),
	}
}

// project maps a point x east, y north, z up (metres) to the frame.
func (c camera) project(x, y, z float64) point {
	vy, vz := y+c.d, z-c.target-c.h
	depth := vy*c.cp - vz*c.sp
	up := vy*c.sp + vz*c.cp
	return point{Width*PinX + c.f*x/depth, Height*PinY - c.f*up/depth}
}

// scale is frame units per metre at the pin, for line widths.
func (c camera) scale() float64 { return c.f / math.Hypot(c.d, c.h) }

// foot is where the camera stands on the ground; the back-to-front order
// is judged from it.
func (c camera) foot() point { return point{0, -c.d} }

// southEdge is the line south of which no building is drawn, in metres
// north of the pin (so a negative number): halfway between the disc's
// edge and the camera's foot. The ground there is already below the
// frame, and a footprint that ran on (a hall some hundred metres long,
// its centre in the disc) would pass under and behind the camera, where
// the projection turns it inside out and the back-to-front order, judged
// from the foot, has no meaning.
func southEdge(radius float64) float64 { return -radius * (1 + distanceRatio) / 2 }
