//go:build (linux || android)

package main

// don't import nor compile sdl on linux/android to avoid slow compile on SBCs and mobile
// portaudio is known to work well as a backend so is sufficient
func setupSDL() (setupSoundcard, bool) {
	p("SDL backend not available")
	return setupSoundcard{}, false
}
