// Package display lists the displays of the desktop the agent runs on, in
// the form of docs/protocol.md §3.1 display.list.
package display

import "errors"

// Info is one display of the desktop. Width and Height are in physical
// pixels, X and Y place the display on the virtual desktop, and Scale is
// DPI/96.
type Info struct {
	// Index orders the displays: the primary display is 0, the others
	// follow from left to right and then from top to bottom.
	Index int `json:"index"`
	// Name is the output's device name, such as \\.\DISPLAY1, and Adapter
	// the description of its graphics adapter.
	Name      string  `json:"name"`
	Adapter   string  `json:"adapter"`
	X         int     `json:"x"`
	Y         int     `json:"y"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	DPI       int     `json:"dpi"`
	Scale     float64 `json:"scale"`
	Primary   bool    `json:"primary"`
	RefreshHz int     `json:"refresh_hz"`
}

// ErrUnsupported is the error of List on a system without display support.
var ErrUnsupported = errors.New("display information needs a Windows agent")
