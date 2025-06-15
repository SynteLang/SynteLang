//go:build android

package main

// mouse functionality is implemented via SDL (not linux nor android) and natively for Linux or FreeBSD. This file deals with the remaining Android case.
func mouseRead() {
	msg("mouse not supported")
}

