package core

// ---------------------------------------------------------------------------
// Geometry
// ---------------------------------------------------------------------------

// Rect describes a rectangular region on the terminal screen.
type Rect struct {
	X, Y, W, H int
}

// NewRect constructs a Rect.
func NewRect(x, y, w, h int) Rect { return Rect{x, y, w, h} }

// Right returns the exclusive right edge (X + W).
func (r Rect) Right() int { return r.X + r.W }

// Bottom returns the exclusive bottom edge (Y + H).
func (r Rect) Bottom() int { return r.Y + r.H }

// Inner returns the rectangle inset by d on every side.
func (r Rect) Inner(d int) Rect {
	return Rect{r.X + d, r.Y + d, r.W - 2*d, r.H - 2*d}
}

// Contains reports whether (x,y) is inside the rectangle.
func (r Rect) Contains(x, y int) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// IsZero reports whether the rect is empty.
func (r Rect) IsZero() bool { return r.W == 0 && r.H == 0 }

