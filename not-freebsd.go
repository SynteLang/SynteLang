//go:build !freebsd

package main

// don't run oss if not freebsd
func setupOSS() (setupSoundcard, bool) {
	p("OSS backend not available")
	return setupSoundcard{}, false
}
