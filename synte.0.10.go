//go:build freebsd && amd64

/*
	Syntə is an audio live coding environment

	The name is pronounced 'sinter', which means to create something by
	fusing many tiny elements together under intense heat

	The input syntax is in EBNF = operator [ " " operand ] .
	Where an operand can be a ( name = letter { letter | digit } ) | ( number = float [ type ] ["/" float [type] ) .
	A letter is defined as any UTF-8 character excluding + - . 0 1 2 3 4 5 6 7 8 9
	A float matches the floating point literal in the Go language specification.
	A type can be one of the following tokens: "hz", "s", "bpm", "db", "!" .
	A list of operators is given below.
	Lists of operations may be composed into functions with multiple arguments.
	The function syntax is = function [ operand ] "," [ operand ] "," [ operand ] .

	Protect your hearing when listening to a system capable of more than 85dB SPL

	Motivation:
		Fun

	Features:
		Audio synthesis √
		Wav playback √
		Mouse control √
		Telemetry / code display √
		Finite recursion with enumeration (not yet implemented)
		Anything can be connected to anything else within a listing √
		Feedback permitted (see above) √
		Groups of operators can be defined, named and instantiated √
		Support for pitch control with useful constants √
		Frequency scaling √
		Predefined functions and operators √
		Flexible synchronisation of co-running listings √

	Author: Dan Arves
	Available for workshops, talks and performances: dancehazard@gmail.com

	This work is not currently licensed
	© 2022

*/

// Go code in this file not suitable for reference or didactic purposes
// This is a protoype

package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	. "math" // don't do this!
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe" // :D
)

// constants for setting format and rate of OSS interface
// these values are from 'sys/sys/soundcard.h' on freebsd13.0
// works for Linux ALSA driver in 16bit mode - must change Sound Engine first!
const (
	// set output only
	IOC_INOUT = 0xC0000000
	// set bit width to 32bit
	SNDCTL_DSP_SETFMT = IOC_INOUT | (0x04&((1<<13)-1))<<16 | 0x50<<8 | 0x05
	//	SNDCTL_DSP_SETFMT	= 0xC0045005
	// Format in Little Endian
	AFMT_S32_LE  = 0x00001000 // use only if supported by soundcard and driver
	AFMT_S16_LE  = 0x00000010
	SELECTED_FMT = AFMT_S16_LE
	// Format in Big Endian
	//AFMT_S32_BE = 0x00002000
	// for Stereo
	//	SNDCTL_DSP_CHANNELS = 0xC0045003
	// set Sample Rate, specific rate defined below
	//	SNDCTL_DSP_SPEED	= IOC_INOUT |(0x04 & ((1 << 13)-1))<<16 | 0x50 << 8 | 0x02
	SNDCTL_DSP_SPEED = 0xC0045002
	SAMPLE_RATE      = 48000 //hertz
	//CONV_FACTOR      = MaxInt32
	CONV_FACTOR = MaxInt16

	WAV_TIME    = 4 //seconds
	WAV_LENGTH  = WAV_TIME * SAMPLE_RATE
	TAPE_LENGTH = 1 //seconds
	MAX_WAVS    = 12
)

// terminal colours, eg. sf("%stest%s test", red, reset)
const (
	reset   = "\x1b[0m"
	bold    = "\x1b[1m"
	italic  = "\x1b[3m"
	red     = "\x1b[31;1m"
	green   = "\x1b[32m"
	yellow  = "\x1b[33m"
	blue    = "\x1b[34m"
	magenta = "\x1b[35m"
	cyan    = "\x1b[36m"
)

type ops struct {
	Opd bool
	N   int
}

var operators = map[string]ops{ // would be nice if switch indexes could be generated from a common root
	// bool indicates if operand used
	"in":      ops{true, 4},
	"out":     ops{true, 2},
	"out+":    ops{true, 3},
	"+":       ops{true, 1},
	"sine":    ops{false, 5},
	"mod":     ops{true, 6},
	"gt":      ops{true, 7},
	"lt":      ops{true, 8},
	"mul":     ops{true, 9},
	"abs":     ops{false, 10},
	"tanh":    ops{false, 11},
	"clip":    ops{true, 14},
	"noise":   ops{false, 15},
	"pow":     ops{true, 12},
	"base":    ops{true, 13},
	"push":    ops{false, 16},
	"pop":     ops{false, 17},
	"tape":    ops{true, 18},
	"tap":     ops{true, 19},
	"+tap":    ops{true, 20},
	"f2c":     ops{false, 21},
	"index":   ops{false, 24}, // change to signal?
	"degrade": ops{true, 37},
	"wav":     ops{true, 22},
	"8bit":    ops{true, 23},
	"x":       ops{true, 9}, // alias of mul
	"<sync":   ops{true, 25},
	">sync":   ops{false, 26},
	//  "nsync":  true, 27},
	"level":  ops{true, 28},
	"*":      ops{true, 9}, // alias of mul
	"from":   ops{true, 29},
	"sgn":    ops{false, 30},
	".>sync": ops{false, 26},
	//	".nsync": true, 0},
	"/":      ops{true, 32},
	"sub":    ops{true, 33},
	"setmix": ops{true, 34},
	"print":  ops{false, 35},
	".level": ops{true, 28},
	"\\":     ops{true, 36},
	"set½":   ops{true, 38},

	// specials
	"]":    ops{false, 0},
	":":    ops{true, 0},
	"fade": ops{true, 0},
	"del":  ops{true, 0},
	//	"propa":	ops{true, 0},
	//	"jl0":		ops{true, 0},
	//	"self":		ops{true, 0},
	"erase":   ops{true, 0},
	"mute":    ops{true, 0},
	"solo":    ops{true, 0},
	"release": ops{true, 0},
	"unmute":  ops{false, 0},
	".mute":   ops{true, 0},
	".del":    ops{true, 0},
	".solo":   ops{true, 0},
	"//":      ops{true, 0}, // comments
}

// listing is a slice of { operator, index and operand }
type listing []struct {
	Op  string
	Opd string
	N   int `json:"-"`
	Opn int `json:"-"`
}

// 'global' transfer variable
var transfer struct {
	Listing []listing
	Signals [][]float64
	Wavs    [][]float64 //sample
}

// communication variables
var (
	stop     = make(chan struct{}) // confirm on close()
	pause    = make(chan bool)     // bool is purely semantic
	started  bool                  // latch
	transmit = make(chan bool)
	accepted = make(chan bool)
	exit     bool // shutdown

	info    = make(chan string)
	carryOn = make(chan bool)
	mute    []float64 // should really be in transfer struct?
	level   []float64
)

var ( // misc
	SampleRate float64
	TLlen      int
	fade       float64 = Pow(1e-4, 1/(275e-3*SAMPLE_RATE)) // 275ms
	protected  bool    = true
	release    float64 = Pow(8000, -1.0/(0.5*SAMPLE_RATE)) // 500ms
)

type noise uint64 // move to S.E.

var mouse struct {
	X, // -255 to 255
	Y,
	Left, // 0 or 1
	Right,
	Middle float64
}

type Disp struct {
	List    int
	Mode    string // func add fon/foff
	Vu      float64
	Clip    bool
	Load    time.Duration
	Info    string
	MouseX  float64
	MouseY  float64
	Protect bool
	Paused  bool
	Mute    []bool
	SR      float64
	GR      bool
}

var display Disp = Disp{
	Mode: "off",
	SR:   48000,
}

type wavs []struct {
	Name string
	Data []float64
}
type sample []float64

const advisory = `
Protect your hearing if listening to audio on a system capable of
more than 85dB SPL

You will experience permanent and irrevocable hearing damage if
you exceed these limits:

		 85dB SPL	8 hours			eg. Lorry
		 91dB SPL	2 hours			eg. Lawnmower
		 97dB SPL	30 minutes		
		103dB SPL	7 minutes		eg. Drill
		112dB SPL	< 1 minute		eg. Typical Club Sound System

	SPL = Sound Pressure Level (A-weighted)
`

func main() {
	for i := 0; i < 45; i++ { // to preserve extant std out
		pf("\n")
	}
	record := true
	// open audio output (everything is a file...)
	file := "/dev/dsp"
	f, rr := os.OpenFile(file, os.O_WRONLY, 0644)
	if e(rr) {
		p(rr)
		p("soundcard not available, shutting down...")
		time.Sleep(3 * time.Second)
		os.Exit(0)
	}
	defer f.Close()
	w := bufio.NewWriter(f)

	// set bit format
	var req uint32 = SNDCTL_DSP_SETFMT
	var data uint32 = SELECTED_FMT
	_, _, ern := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(f.Fd()),
		uintptr(req),
		uintptr(unsafe.Pointer(&data)),
	)
	if ern != 0 {
		p("set format:", ern)
		time.Sleep(time.Second)
	}
	var format uint32
	if data != SELECTED_FMT {
		p("Incorrect bit format! Change requested format in file")
		os.Exit(1)
	}
	switch {
	case data == AFMT_S32_LE:
		format = 32
	case data == AFMT_S16_LE:
		format = 16
	}

	// set sample rate
	req = SNDCTL_DSP_SPEED
	data = SAMPLE_RATE
	_, _, ern = syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(f.Fd()),
		uintptr(req),
		uintptr(unsafe.Pointer(&data)),
	)
	if ern != 0 {
		p("set rate:", ern)
		time.Sleep(time.Second)
	}
	SampleRate = float64(data)
	display.SR = SampleRate

	// sound engine
	go SoundEngine(w)
	// mouse values
	go mouseRead()

	go infodisplay()
	info <- "clear"
	<-carryOn

	// process wav
	var wavsLoaded bool
	var wavSlice wavs
	wavNames := ""
	if wavSlice, wavsLoaded = decodeWavs(); !wavsLoaded {
		info <- "no wavs loaded"
		<-carryOn
	}
	info <- ""

	// signals map with predefined constants, mutable
	sg := map[string]float64{
		"ln2":      Ln2,
		"ln3":      Log(3),
		"ln5":      Log(5),
		"E":        E, // e
		"E-1":      1 / (E - 1),
		"Pi":       Pi,  // π
		"Phi":      Phi, // φ
		"invSR":    1 / SampleRate,
		"SR":       SampleRate,
		"Epsilon":  SmallestNonzeroFloat64, // ε, epsilon
		"wavR":     1.0 / WAV_LENGTH,
		"semitone": Pow(2, 1.0/12),
		//"+inf":	MaxFloat64, // not actual ∞ or +Inf, which would blat the sound engine
	}
	transfer.Wavs = make([][]float64, 0, len(wavSlice))
	wmap := map[string]bool{}
	for i, w := range wavSlice {
		wavNames += w.Name + " "
		wmap[w.Name] = true
		transfer.Wavs = append(transfer.Wavs, w.Data)
		sg[w.Name] = float64(i)
		sg["len"+w.Name] = float64(len(w.Data) - 1)
	}
	TLlen = int(SampleRate * TAPE_LENGTH)
	//signals slice with reserved signals
	reserved := []string{ // order is important
		"dac",
		"",     // nil signal for unused operand
		"α",    // cvflt coeff, no longer in use
		"sync", // remove
		"",     // tempo, deprecated
		"mousex",
		"mousey",
		"butt1",
		"butt3",
		"butt2",
	}
	var sig []float64 // local signals
	funcs := make(map[string]listing)
	// load functions from files and assign to funcs
	load(&funcs, "functions.json")
	for k, f := range funcs { // add funcs to operators map
		hasOpd := false
		for _, o := range f {
			if o.Opd == "@" { // set but don't reset
				hasOpd = true
			}
		}
		o := operators[k]
		o.Opd = hasOpd
		operators[k] = o
	}
	var funcsave bool
	dispListings := []listing{}
	code := &dispListings
	priorMutes := []float64{}
	solo := map[int]bool{}

start:
	for { // main loop
		newListing := listing{}
		dispListing := listing{}
		sig = make([]float64, len(reserved), 30) // capacity is nominal
		out := map[string]struct{}{}
		for _, v := range reserved {
			out[v] = struct{}{}
		}
		fIn := false // true = inside function definition
		st := 0      // func def start
		var num struct {
			Ber float64
			Is  bool
		}
		var hasTape bool

	input:
		// this loop is due to be refactored. Likely type parsing will happen at end and include expression evaluation
		for { // input loop
			pf("\033[H\033[2J") // this clears prior error messages!
			p("> Format:", format, "bit")
			p("> Rate:", SampleRate, "Hz")
			pf("\n%sSynt\u0259%s running...\n\n", cyan, reset)
			pf("Protect your hearing above 85dB SPL\n\n")
			if len(wavNames) > 0 {
				pf(" %swavs:%s %s\n\n", italic, reset, wavNames)
			}
			pf("\n%s%d%s:", cyan, len(dispListings), reset)
			for i, o := range dispListing {
				switch dispListing[i].Op {
				case "in", "pop", "tap", "index", "]":
					pf("\t  %s%s %s%s\n", yellow, o.Op, o.Opd, reset)
				default:
					_, f := funcs[dispListing[i].Op]
					//_, r := sg[dispListing[i].Op] // colour for predefined
					switch {
					case f:
						pf("\t\u21AA %s%s %s%s\n", magenta, o.Op, o.Opd, reset)
					//case r:
					//	pf("\t\u21AA %s%s %s%s\n", cyan, o.Op, o.Opd, reset)
					default:
						pf("\t\u21AA %s%s %s%s\n", yellow, o.Op, o.Opd, reset)
					}
				}
			}
			s := bufio.NewScanner(os.Stdin)
			s.Split(bufio.ScanWords)
			op, opd := "", ""
			pf("\t  ")
			pf("%s", yellow)
			s.Scan()
			pf("%s", reset)
			op = s.Text()
			if op == "func!" || op == "deleted" {
				msg("%s%soperator not permitted%s", red, italic, reset)
				continue
			}
			_, f := funcs[op]
			var operands = []string{}
			r := op[:1] == "!" // to overwrite no-operand function, reconsider notation
			if op2, in := operators[op]; (in && op2.Opd) || (!in && !f) || r {
				if r { // hack to overwrite no-operand functions
					op = op[1:]
				}
				pf("%s", yellow)
				s.Scan()
				opd = s.Text()
				pf("%s", reset)
				if !in && !f && opd != "[" {
					//if not defining a new function, must be extant operator or function
					msg("%s%soperator or function doesn't exist, create with \"[\" operand%s", red, italic, reset)
					continue input
				}
				num.Is = false
				operands = strings.Split(opd, ",")
				if !f && len(operands) > 1 {
					msg("%s%sonly functions can have multiple operands%s", red, italic, reset)
					continue
				}
				for i, opd := range operands { // opd is shadowed
					// future: evaluate multiple operands in function case below only
					wav := wmap[opd] // wavs can start with a number
					if len(opd) == 0 {
						msg("%s%sblank in expression %d ignored%s", red, italic, i+1, reset)
						continue
					}
					if strings.ContainsAny(opd[:1], "+-.0123456789") && !wav { // process a number or fraction
						expression := []string{}
						if strings.Contains(opd, "*") {
							expression = strings.Split(opd, "*")
						} else {
							expression = strings.Split(opd, "/")
						}
						n, cool := parseType(expression[0], op)
						if !cool {
							msg("%snot a valid number or name in expression %d%s", italic, i+1, reset)
							continue input
						}
						num.Ber = n
						num.Is = true
						if len(expression) == 2 {
							op2, cool := parseType(expression[1], op)
							if !cool {
								msg("%ssecond operand in expression %d not a valid number%s", italic, i+1, reset)
								continue input
							}
							switch {
							case strings.Contains(opd, "*"):
								num.Ber *= op2
							case strings.Contains(opd, "/"):
								num.Ber /= op2
							}
						}
						operands[i] = fmt.Sprint(num.Ber)
						if len(expression) > 2 {
							msg("%s%s third operand in expression %d ignored%s", red, italic, i+1, reset)
						}
					}
				}
			}
			name := ""
			if f && opd != "[" { // because syntax swap
				name = op
				op = "func!"
			}
			switch op {
			case ":": //mode setting
				switch opd {
				case "exit":
					p("\nexiting...")
					if display.Paused {
						for i := range mute {
							mute[i] = 0
							display.Mute[i] = true
						}
						time.Sleep(51 * time.Millisecond)
						<-pause
					}
					exit = true
					if started {
						<-stop
					}
					p("Stopped")
					d := Disp{}
					d = Disp{Info: "clear"} // clear info display on exit
					save(d, "infodisplay.json")
					if funcsave {
						if !save(funcs, "functions.json") {
							msg("functions not saved!")
						}
					}
					break start
				case "erase":
					msg("%sinput erased%s", italic, reset)
					continue start
				case "foff":
					funcsave = false
					display.Mode = "off"
					continue
				case "fon":
					funcsave = true
					display.Mode = "on"
					continue
				case "pause":
					if started && !display.Paused {
						for i := range mute { // save mutes
							mute[i] = 0
						}
						time.Sleep(51 * time.Millisecond) // wait for mutes
						pause <- true
						display.Paused = true
					} else if !started {
						msg("%snot started%s", italic, reset)
					}
					continue
				case "play":
					if display.Paused {
						for i := range mute { // restore mutes
							mute[i] = priorMutes[i]
						}
						time.Sleep(51 * time.Millisecond) // wait for mutes
						<-pause
						display.Paused = false
					}
					continue
				case "unprotected":
					protected = !protected
					continue
				case "clear":
					info <- "clear"
					<-carryOn
					continue
				case "verbose":
					switch code {
					case &dispListings:
						code = &transfer.Listing
					case &transfer.Listing:
						code = &dispListings
					}
					if !save(*code, "displaylisting.json") {
						msg("%slisting display not updated, check %s'displaylisting.json'%s exists%s",
							italic, reset, italic, reset)
					}
					continue
				case "restart":
					go SoundEngine(w)
					transfer.Listing = transfer.Listing[:len(transfer.Listing)-1]
					transfer.Signals = transfer.Signals[:len(transfer.Signals)-1]
					mute = mute[:len(mute)-1]
					display.Mute = display.Mute[:len(display.Mute)-1]
					level = level[:len(level)-1]
					display.List--
					transmit <- true
					<-accepted
					msg("\tSound Engine restarted")
					continue
				default:
					msg("%s%sunrecognised mode%s", red, italic, reset)
					continue
				}
			case "in":
				if len(newListing) == 0 {
					break
				}
				switch dispListing[len(dispListing)-1].Op {
				case "out", "push", "tape", ">sync", "out+", "print":
					break
				case "in", "pop", "tap", "index":
					dispListing = dispListing[:len(dispListing)-1]
					newListing = newListing[:len(newListing)-1]
					msg("prior operation replaced")
				default:
					msg("%s%sno out before in, necklace broken%s", red, italic, reset)
					continue
				}
			case "out":
				_, in := out[opd]
				switch {
				case opd == "dac" && fIn:
					msg("%s%soutput to dac not possible within function%s", red, italic, reset)
					continue
				case num.Is:
					msg("%s%soutput to number not permitted%s", red, italic, reset)
					continue
				case in && opd[:1] != "^" && opd != "dac":
					msg("%s%sduplicate output to signal, c'est interdit%s", red, italic, reset)
					continue
				case opd == "@":
					msg("%s%scan't send to @, represents function operand%s", red, italic, reset)
					continue
				}
				out[opd] = struct{}{}
			case "out+":
				if opd == "dac" {
					msg("%s%scan't use dac with out+%s", red, italic, reset)
					continue
				}
			case "func!":
				function := make(listing, len(funcs[name]))
				copy(function, funcs[name])
				n := 0
				for _, o := range dispListing {
					if o.Op == name {
						n++
					}
				}
				s := sf(".%s%d", name, n)
				m := 0
				for i, o := range function {
					if len(o.Opd) == 0 {
						continue
					}
					if o.Opd == "dac" {
						continue
					}
					if _, r := sg[o.Opd]; r {
						continue
					}
					switch o.Opd[:1] {
					case "^", "@":
						continue
					}
					if _, rr = strconv.ParseFloat(o.Opd, 64); !e(rr) {
						continue
					}
					if o.Op == "out" {
						out[o.Opd] = struct{}{}
					}
					function[i].Opd += s

				}
				for i, o := range function {
					switch o.Opd {
					case "@":
						o.Opd = operands[0]
						if m < 1 {
							m = 1
						}
					case "@1":
						if len(operands) > 1 {
							o.Opd = operands[1]
						}
						if m < 2 {
							m = 2
						}
					case "@2":
						if len(operands) > 2 {
							o.Opd = operands[2]
						}
						m = 3
					}
					function[i] = o
				}
				l := len(operands)
				if m < l {
					n := l - m
					switch {
					case n == 1:
						msg("%slast operand ignored%s", italic, reset)
					case n > 1:
						msg("%slast %d operands ignored%s", italic, n, reset)
					}
				}
				if m > l {
					switch {
					case m == 1:
						msg("%s%sthe function requires an operand%s", red, italic, reset)
						continue
					case m > 1:
						msg("%s%sthe function requires %d operands%s", red, italic, m, reset)
						continue
					}
				}
				dispListing = append(dispListing, listing{{Op: name, Opd: opd}}...) // only display name
				newListing = append(newListing, function...)
				if o := newListing[len(newListing)-1]; o.Op == "out" && o.Opd == "dac" {
					break input
				}
				continue
			case "del", ".del":
				n, rr := strconv.Atoi(opd)
				if e(rr) {
					msg("%s%soperand not an integer%s", red, italic, reset)
					continue
				}
				if n > len(transfer.Listing)-1 || n < 0 {
					msg("%s%sindex out of range%s", red, italic, reset)
					continue
				}
				if display.Paused {
					for i := range mute { // restore mutes
						mute[i] = priorMutes[i]
					}
					<-pause
					display.Paused = false
					msg("\t%splay resumed...%s", italic, reset)
				}
				mute[n] = 0                       // wintermute
				time.Sleep(51 * time.Millisecond) // wait for envelope to complete
				transfer.Listing[n] = listing{{Op: "deleted"}}
				dispListings[n] = listing{{Op: "deleted"}}
				display.List--
				transmit <- true
				<-accepted
				if !save(*code, "displaylisting.json") {
					msg("%slisting display not updated, check %s'displaylisting.json'%s exists%s",
						italic, reset, italic, reset)
				}
				if op[:1] == "." && len(newListing) > 0 {
					dispListing = append(dispListing, listing{{Op: "mix"}}...)
					newListing = append(newListing, listing{{Op: "setmix", Opd: "^freq"}}...) // hacky
					op, opd, operands[0] = "out", "dac", "dac"
					break
				}
				continue
			case "]":
				if !fIn || len(newListing[st+1:]) < 1 {
					msg("%s%sno function definition%s", red, italic, reset)
					continue
				}
				hasOpd := false
				for _, o := range newListing[st+1:] {
					if o.Opd == "@" { // set but don't reset
						hasOpd = true
					}
				}
				o := operators[newListing[st].Op]
				o.Opd = hasOpd
				operators[newListing[st].Op] = o
				funcs[newListing[st].Op] = newListing[st+1:]
				dispListing = append(dispListing, listing{{Op: op, Opd: opd}}...)
				msg("%sfunction assigned to:%s %s", italic, reset, newListing[st].Op)
				fIn = false
				continue
			case "fade":
				if !num.Is {
					msg("%s%snot a valid number%s", red, italic, reset)
					continue
				}
				fade = num.Ber
				if fade > 1.0/4800 { // minimum fade time
					fade = 1.0 / 4800
				}
				if fade < 2e-7 { // maximum fade time
					fade = 2e-7
				}
				msg("%sfade set to%s %.3gs", italic, reset, 1/(fade*SampleRate))
				fade = Pow(1e-4, fade) // approx -80dB in t=fade
				continue
			case "pop":
				if len(newListing) == 0 {
					msg("can't start a new listing with pop mate, stack empty...")
					continue
				}
				p := 0
				for _, o := range newListing {
					if o.Op == "push" {
						p++
					}
					if o.Op == "pop" {
						p--
					}
				}
				if p <= 0 {
					msg("%s%spop before push%s", red, italic, reset)
					continue
				}
				switch newListing[len(newListing)-1].Op {
				case "out", "push", "tape", ">sync":
					break
				case "in", "pop", "tap", "index":
					newListing = newListing[:len(newListing)-1]
					dispListing = dispListing[:len(dispListing)-1]
					msg("prior operation replaced")
					continue
				default:
					msg("%s%sno out before pop, necklace broken%s", red, italic, reset)
					continue
				}
			case "tape":
				if hasTape { // add +tape operator later
					msg("%swill overwrite prior tape%s", italic, reset)
				}
				hasTape = true
			case "degrade":
				if len(transfer.Listing) == 0 {
					msg("%scan't use degrade in first listing%s", italic, reset)
					continue
				}
				msg("%sno register is safe...%s", italic, reset)
			case "erase":
				if fIn {
					msg("%s%scan't erase by line within function definition, sorry%s", red, italic, reset)
					continue
				}
				n, rr := strconv.Atoi(opd)
				if e(rr) {
					msg("%s%soperand not an integer:%s %v", red, italic, reset, rr)
					continue
				}
				if n > len(dispListing) {
					msg("%s%snumber greater than length of necklace%s", red, italic, reset)
					continue
				}
				for ; n > 0; n-- {
					lastOp := dispListing[len(dispListing)-1].Op
					count := 1
					if _, in := funcs[lastOp]; in {
						count = len(funcs[lastOp])
					}
					newListing = newListing[:len(newListing)-count]
					dispListing = dispListing[:len(dispListing)-1]
				}
				out = map[string]struct{}{}
				for _, o := range newListing {
					if o.Op == "out" {
						out[o.Opd] = struct{}{}
					}
				}
				continue
			case "wav":
				if fIn && opd != "@" && funcsave {
					msg("%s%scan't use specific wav in function defintion%s", red, italic, reset)
					continue
				}
				if !wmap[opd] {
					msg("%s%sname isn't in wav list%s", red, italic, reset)
					continue
				}
			case "mute", ".mute":
				i, rr := strconv.Atoi(opd)
				if e(rr) {
					msg("%s%soperand not an integer:%s %v", red, italic, reset, rr)
					continue
				}
				i %= len(transfer.Listing) // hacky bounds constraint
				if i < 0 {
					i = -i
				}
				mute[i] = 1 - mute[i]
				display.Mute[i] = mute[i] == 0
				priorMutes[i] = mute[i]
				if op[:1] == "." && len(newListing) > 0 {
					dispListing = append(dispListing, listing{{Op: "mix"}}...)
					newListing = append(newListing, listing{{Op: "setmix", Opd: "^freq"}}...) // hacky
					op, opd, operands[0] = "out", "dac", "dac"
					break
				}
				continue
			case "level", ".level":
				if len(transfer.Listing) == 0 {
					msg("no running listings")
					continue
				}
				i, rr := strconv.Atoi(opd)
				if e(rr) {
					msg("%s%soperand not an integer:%s %v", red, italic, reset, rr)
					continue
				}
				i %= len(transfer.Listing)
				if i < 0 {
					i = -i
				}
				operands[0] = strconv.Itoa(i)
			case "solo", ".solo":
				if len(transfer.Listing) == 0 {
					msg("no running listings")
					continue
				}
				i, rr := strconv.Atoi(opd)
				if e(rr) {
					msg("%s%soperand not an integer:%s %v", red, italic, reset, rr)
					continue
				}
				// apply modulus to i for programmatic arguments
				i %= len(transfer.Listing) + 1 // +1 to allow solo of current listing input when sent
				if i < 0 {
					i = -i
				}
				if solo[i] {
					for i := range mute { // i is shadowed
						mute[i] = priorMutes[i]
						display.Mute[i] = mute[i] == 0
						solo[i] = false
					}
				} else {
					for i := range mute { // i is shadowed
						priorMutes[i] = mute[i]
						mute[i] = 0
						display.Mute[i] = true
					}
					if i < len(transfer.Listing) { // only solo extant listings, new will be unmuted
						mute[i] = 1
						display.Mute[i] = false
					}
					solo[i] = true
				}
				if op[:1] == "." && len(newListing) > 0 {
					dispListing = append(dispListing, listing{{Op: "mix"}}...)
					newListing = append(newListing, listing{{Op: "setmix", Opd: "^freq"}}...) // hacky
					op, opd, operands[0] = "out", "dac", "dac"
					break
				}
				continue
			case "release":
				v, _ := strconv.ParseFloat(operands[0], 64) // error already checked in parseType()
				if v < 1.041e-6 {                           // ~20s
					v = 1.041e-6
				}
				if v > 1.04e-3 { // ~5ms
					v = 1.04e-3
				}
				release = Pow(8000, -v)
				msg("%slimiter release set to:%s %.fms", italic, reset, 1000/(v*SampleRate))
				continue
			case "unmute": // should be in modes?
				if len(transfer.Listing) == 0 {
					msg("no running listings")
					continue
				}
				for i := range mute {
					mute[i] = 1
					display.Mute[i] = false
				}
				continue
			case "tap", "index":
				switch newListing[len(newListing)-1].Op {
				case "out", "push", "tape", ">sync":
					break
				case "in", "pop", "tap", "index":
					newListing = newListing[:len(newListing)-1]
					dispListing = dispListing[:len(dispListing)-1]
					msg("prior operation replaced")
					continue
				default:
					msg("%s%sno out before %s, necklace broken%s", red, italic, op, reset)
					continue
				}
			case "from", "nsync", ".nsync":
				if len(transfer.Listing) == 0 { // consolidate this check into a fallthrough case?
					msg("no running listings")
					continue
				}
				i, rr := strconv.Atoi(opd)
				if e(rr) {
					msg("%s%soperand not an integer:%s %v", red, italic, reset, rr)
					continue
				}
				if len(transfer.Listing) < i || len(transfer.Listing) < 0 {
					msg("%s%slisting doesn't exist:%s %v", red, italic, reset, rr)
					continue
				}
				operands[0] = opd
			case "noise": // set ^freq for `mix` function
				newListing = append(newListing, listing{{Op: "set½", Opd: "^freq"}}...)
			default:
				// parse other types (refactor change)
			}

			if opd == "[" { // function definition
				if _, ok := funcs[op]; ok {
					msg("%s%swill overwrite existing function!%s", red, italic, reset)
				} else if _, ok := operators[op]; ok {
					msg("%s%sduplicate, use another name%s", red, italic, reset)
					continue
				}
				st = len(newListing) // because current input hasn't been added yet
				fIn = true
				msg("%sbegin function definition%s", italic, reset)
				msg("%suse @ for operand signal%s", italic, reset)
			}
			// check operator exists, must be after mode commands above.
			_, f = funcs[op]
			if _, ok := operators[op]; !ok && opd != "[" && !f { // f is shadowed
				msg("%s%soperator not defined%s", red, italic, reset)
				continue
			}
			// elipsis in append because listing is a slice
			if len(dispListing) == 0 || dispListing[len(dispListing)-1].Op != "mix" { // also hacky
				dispListing = append(dispListing, listing{{Op: op, Opd: opd}}...)
			}
			if len(operands) == 0 { // for zero-operand functions
				operands = []string{""}
			}
			if op != "]" {
				newListing = append(newListing, listing{{Op: op, Opd: operands[0]}}...)
			}
			if (op == "out" && opd == "dac") ||
				op == ".>sync" || op == ".nsync" || op == ".level" {
				info <- "clear"
				<-carryOn
				break
			}
		}
		// end of input

		for _, o := range newListing { // assign dc signals or empty to sg map
			if _, ok := sg[o.Opd]; ok || len(o.Opd) == 0 { // use operators map here?
				continue
			}
			// check for float could be eliminated if have number type assigned by a parser
			if n, rr := strconv.ParseFloat(o.Opd, 64); !e(rr) {
				sg[o.Opd] = n // valid number assigned
			} else {
				i := 0
				if o.Opd[:1] == "^" {
					i++
				}
				switch o.Opd[i : i+1] {
				case "'":
					msg("%s %sadded as default 1%s", o.Opd, italic, reset)
					sg[o.Opd] = 1 // default of register
				case "\"":
					msg("%s %sadded as default 0.5%s", o.Opd, italic, reset)
					sg[o.Opd] = 0.5 // default of register
				default:
					sg[o.Opd] = 0
				}
			}
		}

		i := len(sig)          // to ignore reserved signals
		for k, v := range sg { // assign signals to slice from map
			sig = append(sig, v)
			for ii, o := range newListing {
				if o.Opd == k {
					o.N = i
				}
				for i, pre := range reserved {
					if o.Opd == pre {
						o.N = i // shadowed
					}
				}
				o.Opn = operators[o.Op].N
				newListing[ii] = o
			}
			i++
		}

		dispListings = append(dispListings, dispListing)
		if display.Paused {
			for i := range mute { // restore mutes
				mute[i] = priorMutes[i]
			}
			time.Sleep(51 * time.Millisecond) // wait for mutes
			<-pause
			display.Paused = false
			msg("\t%splay resumed...%s", italic, reset)
		}
		//transfer to sound engine
		transfer.Listing = append(transfer.Listing, newListing)
		transfer.Signals = append(transfer.Signals, sig)
		mute = append(mute, 1)
		priorMutes = append(priorMutes, 1)
		display.Mute = append(display.Mute, false)
		level = append(level, 1)
		display.List++
		transmit <- true
		<-accepted
		if !started {
			started = true
		}

		if record {
			timestamp := time.Now().Format("02-01-06.15:04")
			f := "recordings/listing." + timestamp + ".json" // shadowed
			if !save(newListing, f) {                        // save as plain text instead?
				msg("%slisting not recorded, check recordings/ directory exists%s", italic, reset)
			}
		}
		if !save(*code, "displaylisting.json") {
			msg("%slisting display not updated, check file %s'displaylisting.json'%s exists%s",
				italic, reset, italic, reset)
		}
		/*
			stats := new(debug.GCStats)
			debug.ReadGCStats(stats)
			msg("___GC statistics:___")
			msg("No.: %v", stats.NumGC)
			msg("Tot.: %v", stats.PauseTotal)
			msg("Avg.: %v", stats.PauseTotal/time.Duration(stats.NumGC))
			msg("Distr.: %v", stats.PauseQuantiles)
		*/
	}
	/*
		f, rr = os.Create("heapdump.txt")
		if e(rr) {
			p(rr)
		}
		debug.WriteHeapDump(f.Fd())
		f.Close()
	*/
}

func parseType(expr, op string) (n float64, b bool) {
	//func parseType(expr, op string) (y func(), b bool) { // possible upgrade
	// or expression evaluated here instead
	switch op { // ignore for following operators
	case "mute", ".mute", "del", ".del", "solo", ".solo", "level", ".level", "from":
		return 0, true
	default:
		// process expression below
	}
	var rr error
	switch {
	case len(expr) > 1 && expr[len(expr)-1:] == "!":
		//return func(n float64) float64 {
		if n, rr = strconv.ParseFloat(expr[:len(expr)-1], 64); e(rr) {
			msg("if at first you don't succeed...")
			return 0, false
		}
		msg("proceed with caution...")
		//return n, true
		//}, true
	case len(expr) > 2 && expr[len(expr)-2:] == "ms":
		if n, rr = strconv.ParseFloat(expr[:len(expr)-2], 64); e(rr) {
			msg("erm s")
			return 0, false
		}
		n = 1 / ((n / 1000) * SampleRate)
	case len(expr) > 1 && expr[len(expr)-1:] == "s":
		if n, rr = strconv.ParseFloat(expr[:len(expr)-1], 64); e(rr) {
			msg("seven seconds away")
			return 0, false
		}
		n = 1 / (n * SampleRate)
	case len(expr) > 2 && expr[len(expr)-2:] == "hz":
		if n, rr = strconv.ParseFloat(expr[:len(expr)-2], 64); e(rr) {
			msg("the truth hertz")
			return 0, false
		}
		n /= SampleRate
		ny := 2e4 / SampleRate
		if n < -ny || n > ny {
			msg("inaudible frequency >20kHz")
			return 0, false
		}
	case len(expr) > 2 && expr[len(expr)-2:] == "db": // 0dB = 1
		if n, rr = strconv.ParseFloat(expr[:len(expr)-2], 64); e(rr) {
			msg("for whom the decibel tolls")
			return 0, false
		}
		n /= 20
		n = Pow(10, n)
	case len(expr) > 3 && expr[len(expr)-3:] == "bpm":
		if n, rr = strconv.ParseFloat(expr[:len(expr)-3], 64); e(rr) {
			msg("beats me what went wrong there")
			return 0, false
		}
		if n > 300 {
			msg("gabber territory")
		}
		if n > 3000 {
			msg("%fbpm? You're 'aving a larf mate", n)
			return 0, false
		}
		n /= 60
		n /= SampleRate
	case len(expr) > 4 && expr[len(expr)-4:] == "mins":
		if n, rr = strconv.ParseFloat(expr[:len(expr)-4], 64); e(rr) {
			msg("time waits for no-one")
			return 0, false
		}
		n *= 60
		n = 1 / (n * SampleRate)
	default:
		if n, rr = strconv.ParseFloat(expr, 64); e(rr) {
			msg("rung rong number")
			return 0, false
		}
		if Abs(n) > 20 {
			msg("exceeds sensible values, do you mean %.[1]fhz, %.[1]fs, or %.[1]fbpm?", n)
			return 0, false
		}
	}
	return n, true
}

// decodeWavs is a somewhat hacky implementation that works for now. A maximum of WAV_LENGTH samples are sent to the main routine. All files are currently converted from stereo to mono. Differing sample rates are not currently converted. Header is assumed to be 44 bytes.
func decodeWavs() (wavs, bool) {
	var filelist []string
	var w wavs
	var wav struct {
		Name string
		Data []float64
	}
	files, rr := os.ReadDir("./wavs")
	if e(rr) {
		msg("%sno wavs:%s %v", italic, reset, rr)
		return nil, false
	}
	limit := 0
	for _, file := range files {
		name := file.Name()
		if name[len(name)-4:] != ".wav" {
			continue
		}
		filelist = append(filelist, name)
		limit++
		if limit > MAX_WAVS {
			break
		}
	}
	if len(filelist) == 0 {
		msg("no wav files found")
		return nil, false
	}
	pf("%sProcessing wavs...%s", italic, reset)
	for _, file := range filelist {
		r, rr := os.Open("./wavs/" + file)
		if e(rr) {
			msg("error loading: %s %s", file, rr)
			continue
		}
		data := make([]byte, 44+8*WAV_LENGTH) // enough for 32bit stereo @ WAV_LENGTH
		n, rr := io.ReadFull(r, data)
		if rr == io.ErrUnexpectedEOF {
			data = data[:n] // truncate silent data
		} else if e(rr) {
			msg("error reading: %s %s", file, rr)
			continue
		}
		// check format=1, channels <3, rate, bits=16or32, skip otherwise
		format := binary.LittleEndian.Uint16(data[20:22])
		if format != 1 {
			msg("can only decode PCM format, soz: %s", file)
			continue
		}
		channels := int(binary.LittleEndian.Uint16(data[22:24]))
		if channels > 2 {
			msg("neither mono nor stereo: %s %s", file, rr)
			continue
		}
		SR := binary.LittleEndian.Uint32(data[24:28])
		if SR%22050 != 0 && SR%48000 != 0 {
			msg("Warning: non-standard sample rate: %s", file)
			time.Sleep(time.Second)
		}
		bits := binary.LittleEndian.Uint16(data[34:36])
		to := channels * WAV_LENGTH
		rb := bytes.NewReader(data[44:])
		switch bits { // generify these cases
		case 16:
			samples := make([]int16, to)
			rr := binary.Read(rb, binary.LittleEndian, &samples)
			if e(rr) {
				msg("error decoding: %s %s", file, rr)
				continue
			}
			// convert to syntə format
			n := 0
			s := 0.0
			wav.Data = make([]float64, 0, to)
			for i := 0; i < to; i += channels {
				if channels == 2 {
					s = (float64(samples[i]) + float64(samples[i+1])) / (2 * MaxInt16)
				} else {
					s = float64(samples[i]) / MaxInt16
				}
				wav.Data = append(wav.Data, s)
				n++
			}
		case 24:
			d := make([]byte, 0, len(data)*2)
			for i := 44; i < len(data)-3; i += 3 { // byte stuffing
				word := append(data[i:i+3], byte(0))
				d = append(d, word...)
			}
			rb = bytes.NewReader(d)
			samples := make([]int32, to)
			rr := binary.Read(rb, binary.LittleEndian, &samples)
			if e(rr) {
				msg("error decoding: %s %s", file, rr)
				continue
			}
			// convert to syntə format
			n := 0
			s := 0.0
			wav.Data = make([]float64, 0, to)
			for i := 0; i < to-channels; i += channels {
				if channels == 2 {
					s = (float64(samples[i]) + float64(samples[i+1])) / (2 * MaxInt32)
				} else {
					s = float64(samples[i]) / MaxInt32
				}
				wav.Data = append(wav.Data, s)
				n++
			}
		case 32:
			samples := make([]int32, to)
			rr := binary.Read(rb, binary.LittleEndian, &samples)
			if e(rr) {
				msg("error decoding: %s %s", file, rr)
				continue
			}
			// convert to syntə format
			n := 0
			s := 0.0
			wav.Data = make([]float64, 0, to)
			for i := 0; i < to-channels; i += channels {
				if channels == 2 {
					s = (float64(samples[i]) + float64(samples[i+1])) / (2 * MaxInt32)
				} else {
					s = float64(samples[i]) / MaxInt32
				}
				wav.Data = append(wav.Data, s)
				n++
			}
		default:
			msg("%s: needs to be 32, 24 or 16 bit", file)
			continue
		}
		l := len(file)
		wav.Name = strings.Replace(file[:l-4], " ", "", -1)
		w = append(w, wav)
		r.Close()
		t := float64(len(wav.Data)) / float64(SR)
		c := "stereo"
		if channels == 1 {
			c = "mono  "
		}
		msg("%s\t%s  SR: %6d  bits: %v  %.3gs", file, c, SR, bits, t)
	}
	if len(w) == 0 {
		return nil, false
	}
	return w, true
}

// quick and basic decode of mouse bytes
func mouseRead() {
	file := "/dev/bpsm0"
	//file := "/dev/input/mice" // for Linux
	mf, rr := os.Open(file)
	if e(rr) {
		p("error opening '"+file+"':", rr)
		msg("mouse unavailable")
		return
	}
	defer mf.Close()
	m := bufio.NewReader(mf)
	bytes := make([]byte, 3)
	for {
		_, rr := io.ReadFull(m, bytes)
		mouse.Left, mouse.Right, mouse.Middle = 0, 0, 0
		if bytes[0]&1 == 1 { // left button
			mouse.Left = 1
		}
		if bytes[0]>>1&1 == 1 { // right button
			mouse.Right = 1
		}
		if bytes[0]>>2&1 == 1 { // middle button
			mouse.Middle = 1
		}
		if bytes[1] != 0 {
			if bytes[0]>>4&1 == 1 {
				mouse.X += float64(int8(bytes[1]-255)) / 255
			} else {
				mouse.X += float64(int8(bytes[1])) / 255
			}
		}
		if bytes[2] != 0 {
			if bytes[0]>>5&1 == 1 {
				mouse.Y += float64(int8(bytes[2]-255)) / 255
			} else {
				mouse.Y += float64(int8(bytes[2])) / 255
			}
		}
		if e(rr) {
			pf("%serror reading %s: %v\r", reset, file, rr)
			msg("error reading mouse data")
			return
		}
		if exit {
			break
		}
		display.MouseX = mouse.X
		display.MouseY = mouse.Y
		time.Sleep(42 * time.Microsecond) // coarse loop timing
	}
}

func infodisplay() {
	file := "infodisplay.json"
	n := 0
	for {
		display.Protect = protected

		select {
		case infoString := <-info:
			display.Info = infoString
		case carryOn <- true: // semaphore: received
			// continue
		default:
			// passthrough
		}
		if !save(display, file) {
			pf("%sinfo display not updated, check file %s%s%s exists%s\n",
				italic, reset, file, italic, reset)
			time.Sleep(2 * time.Second)
		}
		if exit { //display doesn't run during fade out
			break
		}
		time.Sleep(38627 * time.Microsecond) // coarse loop timing
		n++
		if n > 10 {
			display.Clip = false
			n = 0
		}
	}
}

// The Sound Engine does the bare minimum to generate audio
// The code has not been optimised, beyond certain design choices such as using slices instead of maps
// It is also freewheeling, it won't block on the action of any other goroutinue, only on IO, namely writing to soundcard
// The latency and jitter of the audio output is entirely dependent on the soundcard and its OS driver,
// except where the calculations don't complete in time under heavy load and the soundcard driver buffer underuns.
func SoundEngine(w *bufio.Writer) {
	// this doesn't work if placed in main: ¯\_(ツ)_/¯
	defer save([]listing{listing{{Op: advisory}}}, "displaylisting.json")
	defer func() { // fail gracefully
		if p := recover(); p != nil {
			msg("stack trace: %s", debug.Stack())
			msg("%v, %stype `: restart`%s", p, red, reset)
		}
	}()
	// intialise capacities upfront
	listings := make([]listing, 0, 24)
	sigs := make([][]float64, 0, 23)
	stacks := make([][]float64, 1, 21)
	wavs := make([][]float64, 0, MAX_WAVS)
	tapes := make([][]float64, 0, 26)

	var (
		no       noise   = noise(time.Now().UnixNano())
		r        float64        // result
		l, h     float64 = 1, 2 // limiter, hold
		dac      float64        // output
		env      float64 = 1    // for exit envelope
		peak     float64        // vu meter
		dither   float64
		n        int                                 // loop counter
		rate     time.Duration = time.Duration(7292) // loop timer, initialised to approximate resting rate
		lastTime time.Time     = time.Now()
		s        float64       = 1                               //sync=0
		p        bool                                            // play/pause
		ii       int                                             // sync intermediate
		penv     float64       = Pow(1e-4, 1/(SampleRate*50e-3)) // approx -80dB in 50ms
		mx, my   float64                                         // mouse smooth intermediates
		hpf, x   float64                                         // DC-blocking high pass filter
		hpf2560, x2560,
		hpf160, x160,
		det float64 // limiter detection
	)
	no *= 77777777777 // force overflow

	<-transmit // load first listing and start SoundEngine
	listings = transfer.Listing
	sigs = transfer.Signals
	wavs = transfer.Wavs
	tape := make([]float64, TLlen)
	tapes = append(tapes, tape)
	accepted <- true
	sync := make([]float64, 1)
	syncInhibit := make([]bool, 1, 27) // inhibitions
	peakfreq := make([]float64, 1, 28) // peak frequency for setlevel
	peakfreq[0] = 20 / SampleRate
	m := make([]float64, 1, 29)  // filter intermediate for mute
	lv := make([]float64, 1, 29) // filter intermediate for mute

	lastTime = time.Now()
	for {
		select {
		case <-pause:
			p = true
		case <-transmit:
			listings = make([]listing, len(transfer.Listing))
			copy(listings, transfer.Listing)
			sigs = make([][]float64, len(transfer.Signals))
			copy(sigs, transfer.Signals)
			stacks = make([][]float64, len(listings))
			accepted <- true
			// add additional sync inhibit and tape. Safe because transfer is always one extra listing
			tapes = append(tapes, tape)
			sync = append(sync, 0)
			syncInhibit = append(syncInhibit, false)
			peakfreq = append(peakfreq, 20/SampleRate)
			m = append(m, 0.0)
			lv = append(lv, 0.0)
		default:
			// play
		}

		if n%15127 == 0 { // arbitrary interval all-zeros protection for lfsr
			no ^= 1 << 27
		}

		for i, list := range listings {
			r = 0
			mx = (mx*765 + mouse.X) / 766 // lpf @ ~10Hz
			my = (my*765 + mouse.Y) / 766
			sigs[i][5] = mx
			sigs[i][6] = my
			sigs[i][7] = mouse.Left
			sigs[i][8] = mouse.Right
			sigs[i][9] = mouse.Middle
			for _, o := range list {
				switch o.Opn {
				case 0:
					// nop
				case 1: //"+":
					r += sigs[i][o.N]
				case 2: //"out":
					sigs[i][o.N] = r
				case 3: //"out+":
					sigs[i][o.N] += r
				case 4: //"in":
					r = sigs[i][o.N]
				case 5: //"sine":
					r = Sin(2 * Pi * r)
				case 6: //"mod":
					r = Mod(r, sigs[i][o.N])
				case 7: //"gt":
					if r >= sigs[i][o.N] {
						r = 1
					} else {
						r = 0
					}
				case 8: //"lt":
					if r <= sigs[i][o.N] {
						r = 1
					} else {
						r = 0
					}
				case 9: //"mul", "x", "*":
					r *= sigs[i][o.N]
				case 10: //"abs":
					r = Abs(r)
				case 11: //"tanh":
					r = Tanh(r)
				case 12: //"pow":
					if r == 0 && Signbit(sigs[i][o.N]) {
						r = Copysign(1e-308, r) // inverse is within upper range of float
					}
					r = Pow(r, sigs[i][o.N])
				case 13: //"base":
					r = Pow(sigs[i][o.N], r)
					if IsInf(r, 0) { // infinity to '93
						r = Nextafter(r, 0)
					}
				case 14: //"clip":
					switch {
					case sigs[i][o.N] == 0:
						if r > 1 {
							r = 1
						}
						if r < 0 {
							r = 0
						}
					case sigs[i][o.N] > 0:
						if r > sigs[i][o.N] {
							r = sigs[i][o.N]
						}
						if r < -sigs[i][o.N] {
							r = -sigs[i][o.N]
						}
					case sigs[i][o.N] < 0:
						if r < sigs[i][o.N] {
							r = sigs[i][o.N]
						}
						if r > -sigs[i][o.N] {
							r = -sigs[i][o.N]
						}
					}
				case 15: //"noise":
					no.ise() // roll a fresh one
					r *= (2*(float64(no)/MaxUint) - 1)
				case 16: //"push":
					stacks[i] = append(stacks[i], r)
				case 17: //"pop":
					r = stacks[i][len(stacks[i])-1]
					stacks[i] = stacks[i][:len(stacks[i])-1]
				case 18: //"tape":
					tapes[i][n%TLlen] = r
					sigs[i][o.N] = Abs(sigs[i][o.N])
					r = tapes[i][int(float64(n%TLlen)*sigs[i][o.N])%TLlen]
					// add lpf with sigs[i][o.N] as coefficient?
				case 19: //"tap":
					sigs[i][o.N] = Abs(1 - sigs[i][o.N])
					r = tapes[i][(n+int(TAPE_LENGTH/(sigs[i][o.N])))%TLlen]
				case 20: //"+tap":
					sigs[i][o.N] = Abs(1 - sigs[i][o.N])
					r += tapes[i][(n+int(SampleRate*TAPE_LENGTH*(sigs[i][o.N])))%TLlen]
				case 21: //"f2c":
					r = Abs(r)
					r = 1 / (1 + 1/(2*Pi*r))
				case 37: //"degrade": // needs more work
					no.ise()
					ii = (int(no >> 60)) % (len(listings) - 1)
					index := (int(no >> 59)) % (len(sigs[ii]) - 1)
					sigs[ii][index] += sigs[i][o.N] * r
					no.ise()
					ii = (int(no >> 60)) % (len(listings) - 1)
					index = (int(no >> 59)) % (len(sigs[ii]) - 1)
					r += sigs[i][o.N] * sigs[ii][index]
					//if ii< 0 { index*= -1 }
					//if index< 0 { index*= -1 }
				case 22: //"wav":
					r = Abs(r)
					r *= WAV_LENGTH
					r = wavs[int(sigs[i][o.N])][int(r)%len(wavs[int(sigs[i][o.N])])]
				case 23: //"8bit":
					r = float64(int8(r*sigs[i][o.N]*MaxInt8)) / (MaxInt8 * sigs[i][o.N])
				case 24: //"index":
					r = float64(i) // * sigs[i][o.N]
				case 25: //"<sync":
					r *= s * (1 - sync[i])
					r += (1 - s) * sync[i] * sigs[i][o.N] // phase offset
				case 26: //">sync", ".>sync":
					switch {
					case r <= 0 && s == 1 && !syncInhibit[i]:
						s = 0
						syncInhibit[i] = true
					case s == 0: // single sample pulse
						s = 1
					case r > 0:
						syncInhibit[i] = false
					}
				/*case 27: "nsync", ".nsync":
				ii = int(sigs[i][o.N])
				switch {
				case r <= 0 && sync[ii] == 0 && !syncInhibit[ii]:
					sync[ii] = 1
					syncInhibit[ii] = true
				case sync[ii] == 0:
					sync[ii] = 0
				case r > 0:
					syncInhibit[ii] = false
				}*/
				case 28: //"level", ".level":
					level[int(sigs[i][o.N])] = r
				case 29: //"from":
					r = sigs[int(sigs[i][o.N])][0]
				case 30: //"sgn":
					r = float64(Float64bits(r)>>63)*2 - 1
				case 31: //"deleted":
					sigs[i][0] = 0
				case 32: //"/":
					if sigs[i][o.N] == 0 {
						sigs[i][o.N] = Copysign(1e-308, sigs[i][o.N])
					}
					r /= sigs[i][o.N]
				case 33: //"sub":
					r -= sigs[i][o.N]
				case 34: //"setmix":
					a := Abs(sigs[i][o.N]) + 1e-6
					d := Log2(a / peakfreq[i])
					if d > 1 {
						d = 1
					}
					if d < -1 {
						d = -1
					}
					peakfreq[i] += a * (d * 40.0 / SampleRate)
					if Abs(d) < 0.01 {
						peakfreq[i] = a
					}
					r *= Min(1, 9/(Sqrt(peakfreq[i]*SampleRate)+4.5))
				case 35: //"print":
					if n%16384 == int(no>>51) && !exit { // dubious exit guard
						info <- sf("listing %d: %.3g", i, r)
					}
				case 38: //"set½": // for internal use
					sigs[i][o.N] = 0.015
				case 36: //"\\":
					if r == 0 {
						r = Copysign(1e-308, r)
					}
					r = sigs[i][o.N] / r
				default:
					// nop, r = r
				}
			}
			//info <- sf("%v\r", sigs[i][0]) // slo-mo debug, will cause long exit! Use ctrl-c
			if sigs[i][0] != sigs[i][0] { // test for NaN
				sigs[i][0] = 0
				info <- "NaN"
			}
			if IsInf(sigs[i][0], 0) { // infinity to '93
				if n%24000 == 0 {
					info <- sf("%v overflow", sigs[i][0])
				}
				sigs[i][0] = 0
			}
			m[i] = (m[i]*39 + mute[i]) / 40         // anti-click filter @ ~110Hz
			lv[i] = (lv[i]*7 + level[i]) / 8        // @ 1273Hz
			dac += sigs[i][0] * m[i] * m[i] * lv[i] // mute transition is quadratic
		}
		if n := len(listings); n > 4 {
			dac /= float64(n)
		} else {
			dac /= 4
		}
		hpf = (hpf + dac - x) * 0.9994 // hpf = 4.6Hz @ 48kHz SR
		x = dac
		dac = hpf
		if protected { // limiter
			// apply premphasis to detection
			hpf2560 = (hpf2560 + dac - x2560) * 0.749
			x2560 = dac
			hpf160 = (hpf160 + dac - x160) * 0.97948
			x160 = dac
			det = Abs(16*hpf2560+4*hpf160+dac) / 2
			if det > l {
				l = det // MC
				h = release
			}
			dac /= l
			h /= release
			l = (l-1)*(1/(h+1/(1-release))+release) + 1 // snubbed decay curve
			display.GR = l > 1+3e-4
		} else {
			display.GR = false
		}
		dac *= env // fade out
		no.ise()
		dither = float64(no) / MaxUint64
		no.ise()
		dither += float64(no)/MaxUint64 - 1
		dac *= (CONV_FACTOR - 1.0) / CONV_FACTOR // headroom for positive dither
		dac += dither / CONV_FACTOR              // dither dac value ±1 from xorshift lfsr
		if dac > 1 {                             // hard clip
			dac = 1
			display.Clip = true
		}
		if dac < -1 {
			dac = -1
			display.Clip = true
		}
		if abs := Abs(dac); abs > peak { // peak detect
			peak = abs
		}
		peak -= 8e-5 // meter ballistics
		if peak < 0 {
			peak = 0
		}
		display.Vu = peak
		dac *= CONV_FACTOR                               // convert
		rate = (rate*6999 + time.Since(lastTime)) / 7000 //weighted average
		//binary.Write(w, binary.LittleEndian, int32(dac)) // 32bit write
		binary.Write(w, binary.LittleEndian, int16(dac))
		lastTime = time.Now()
		display.Load = rate
		dac = 0

		if exit {
			env *= fade
			if env < 1e-4 {
				break
			}
		} else if p {
			env *= penv
			if env < 1e-4 {
				pause <- false // blocks until `: play`
				lastTime = time.Now()
				env = 1 // zero attack...
				p = false
			}
		}
		n++
	}
	w.Flush()
	close(stop)
}

func (n *noise) ise() {
	*n ^= *n << 13
	*n ^= *n >> 7
	*n ^= *n << 17
}

func load(data interface{}, f string) {
	Json, rr := os.ReadFile(f)
	rr2 := json.Unmarshal(Json, data)
	if e(rr) || e(rr2) {
		msg("Error loading '%s': %v %v", f, rr, rr2)
	}
}

func save(data interface{}, f string) bool {
	Json, rr := json.MarshalIndent(data, "", "\t")
	rr2 := os.WriteFile(f, Json, 0644)
	if e(rr) || e(rr2) {
		msg("Error saving '%s': %v %v", f, rr, rr2)
		return false
	}
	return true
}

// for monitoring
var p func(...interface{}) (int, error) = fmt.Println
var sf func(string, ...interface{}) string = fmt.Sprintf
var pf func(string, ...interface{}) (int, error) = fmt.Printf

// msg sends a formatted string to info display
func msg(s string, i ...interface{}) {
	info <- fmt.Sprintf(s, i...)
	<-carryOn
}

// error handling
func e(rr error) bool {
	return rr != nil
}
