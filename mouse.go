//go:build !(linux || freebsd) && !android

package main

import (
	"math"
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

func mouseRead() {
	msg("mouse not currently supported via SDL")
	return
	var mx, my float64

	for {
		sdl.PumpEvents()
		x, y, mflag := sdl.GetRelativeMouseState()
		if x != 0 {
			msg("%v", x)
		}

		mouse.Left, mouse.Right, mouse.Middle = 0, 0, 0
		if mflag&1 == 1 { // left button
			mouse.Left = 1
		}
		if mflag>>1&1 == 1 { // middle button
			mouse.Middle = 1
		}
		if mflag>>2&1 == 1 { // right button
			mouse.Right = 1
		}

		mx += float64(x)/math.MaxInt32
		my += float64(y)/math.MaxInt32
		if mouse.mc {
			mouse.X = math.Pow(10, mx/10)
			mouse.Y = math.Pow(10, my/10)
		} else {
			mouse.X = mx / 5
			mouse.Y = my / 5
		}
		display.MouseX = mouse.X
		display.MouseY = mouse.Y
		time.Sleep(416 * time.Microsecond) // coarse loop timing
	}
}
