//go:build (freebsd || linux) && amd64

/*
	Syntə is an audio live coding environment

	The name is pronounced 'sinter', which means to create something by
	fusing many tiny elements together under intense heat

	The input syntax is in EBNF = operator [ " " operand ] .
	Where an operand can be a ( name = letter { letter | digit } ) | ( number = float  ["/" float ] [type] ) .
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
	Available for workshops, talks and performances: synte@proton.me

	See licence file in this repository
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
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unsafe" // :D
)

// constants for setting format and rate of OSS interface
// these values are from 'sys/sys/soundcard.h' on freebsd13.0
// currently set to stereo - use `sudo sysctl dev.pcm.X.bitperfect=1` or timings will be wrong,
// where X is the output found in `cat /dev/sndstat`
const (
	// set output only
	IOC_INOUT = 0xC0000000
	// set bit width to 32bit
	SNDCTL_DSP_SETFMT = IOC_INOUT | (0x04&((1<<13)-1))<<16 | 0x50<<8 | 0x05
	//	SNDCTL_DSP_SETFMT	= 0xC0045005
	// Format in Little Endian
	AFMT_S32_LE  = 0x00001000 // use only if supported by soundcard and driver
	AFMT_S16_LE  = 0x00000010
	AFMT_S8      = 0x00000040
	SELECTED_FMT = AFMT_S16_LE
	// Format in Big Endian
	//AFMT_S32_BE = 0x00002000
	// for Stereo
	SNDCTL_DSP_CHANNELS = 0xC0045003
	STEREO              = 1
	MONO                = 0
	CHANNELS            = STEREO // will halve pitches/frequencies/tempos if mono!
	// set Sample Rate, specific rate defined below
	//	SNDCTL_DSP_SPEED	= IOC_INOUT |(0x04 & ((1 << 13)-1))<<16 | 0x50 << 8 | 0x02
	SNDCTL_DSP_SPEED = 0xC0045002
	SAMPLE_RATE      = 48000 //hertz

	WAV_TIME       = 4 //seconds
	WAV_LENGTH     = WAV_TIME * SAMPLE_RATE
	TAPE_LENGTH    = 1 //seconds
	MAX_WAVS       = 12
	RMS_INT        = SAMPLE_RATE / 8
	EXPORTED_LIMIT = 12
	NOISE_FREQ     = 0.0132 // geometric mean of audible spectrum
)

var convFactor = float64(MaxInt16)

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
	"tap":     ops{true, 20},
	"f2c":     ops{false, 21},
	"index":   ops{false, 24}, // change to signal?
	"degrade": ops{true, 37},
	"wav":     ops{true, 22},
	"8bit":    ops{true, 23},
	"x":       ops{true, 9}, // alias of mul
	"<sync":   ops{true, 25},
	">sync":   ops{false, 26},
	"level":   ops{true, 28},
	"*":       ops{true, 9}, // alias of mul
	"from":    ops{true, 29},
	"sgn":     ops{false, 30},
	".>sync":  ops{false, 26},
	"/":       ops{true, 32},
	"sub":     ops{true, 33},
	"setmix":  ops{true, 34},
	"print":   ops{false, 35},
	".level":  ops{true, 28},
	"\\":      ops{true, 36}, // "\"
	"-":       ops{true, 33}, // alias of sub
	"all":     ops{false, 40},
	"rms":     ops{true, 41},
	".out":    ops{true, 2}, // alias of out, not implemented
	//"":     ops{true, 19}, // unused
	//"":     ops{true, 27}, // unused
	//"":     ops{true, 38}, // unused
	//"":     ops{true, 39}, // unused

	// specials
	"]":       ops{false, 0},
	":":       ops{true, 0},
	"fade":    ops{true, 0},
	"del":     ops{true, 0},
	"erase":   ops{true, 0},
	"mute":    ops{true, 0},
	"m":       ops{true, 0}, // alias of mute
	"solo":    ops{true, 0},
	"release": ops{true, 0},
	"unmute":  ops{false, 0},
	".mute":   ops{true, 0},
	".del":    ops{true, 0},
	".solo":   ops{true, 0},
	"//":      ops{true, 0}, // comments
	"load":    ops{true, 0},
	"ld":      ops{true, 0}, // alias of load
	"[":       ops{true, 0},
	"save":    ops{true, 0},
	"ls":      ops{true, 0},
	"ct":      ops{true, 0}, // individual clip threshold
	"rld":     ops{true, 0},
	"r":       ops{true, 0}, // alias of rld
	"rpl":     ops{true, 0},
	".rpl":    ops{true, 0},
	"s":       ops{true, 0}, // alias of solo
	"e":       ops{true, 0}, // alias of erase
	//	"self":		ops{true, 0}, // function recursion
}

// listing is a slice of { operator, index and operand }
type listing []struct {
	Op  string
	Opd string
	N   int `json:"-"`
	Opn int `json:"-"`
}

// 'global' transfer variable
var transfer struct { // make this a slice of structs?
	Listing []listing
	Signals [][]float64
	Wavs    [][]float64 //sample
}

var Sigs []int // list of exported signals to be daisy-chained

// communication variables
var (
	stop     = make(chan struct{}) // confirm on close()
	pause    = make(chan bool)     // bool is purely semantic
	started  bool                  // latch
	transmit = make(chan bool)
	accepted = make(chan bool)
	exit     bool // shutdown

	info     = make(chan string, 96) // arbitrary buffer length, 48000Hz = 960 x 50Hz
	carryOn  = make(chan bool)
	infoff   = make(chan struct{}) // shut-off info display
	mute     []float64             // should really be in transfer struct?
	level    []float64
	muteSkip bool
)

var ( // misc
	SampleRate,
	initialR8 float64
	TLlen     int
	fade      float64 = Pow(1e-4, 1/(100e-3*SAMPLE_RATE)) // 100ms
	protected         = true
	release   float64 = Pow(8000, -1.0/(0.5*SAMPLE_RATE))  // 500ms
	DS        float64 = 1                                  // down-sample, integer as float type
	nyfC      float64 = 1 / (1 + 1/(2*Pi*2e4/SAMPLE_RATE)) // coefficient
	ct        float64 = 1                                  // individual listing clip threshold
	ext       bool    = false                              // loading external listing state
)

type noise uint64

var mouse = struct {
	X, // -255 to 255
	Y,
	Left, // 0 or 1
	Right,
	Middle float64
}{
	X: 1,
	Y: 1,
}
var mc = true

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
	Verbose bool
}

var display = Disp{
	Mode:   "off",
	MouseX: 1,
	MouseY: 1,
	SR:     48000,
}

type wavs []struct {
	Name string
	Data []float64
}
type sample []float64

const advisory = `
Protect your hearing when listening to any audio on a system capable of
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
	save([]listing{listing{{Op: advisory}}}, "displaylisting.json")
	record := true
	// open audio output (everything is a file...)
	file := "/dev/dsp"
	f, rr := os.OpenFile(file, os.O_WRONLY, 0644)
	if e(rr) {
		p(rr)
		p("soundcard not available, shutting down...")
		time.Sleep(3 * time.Second)
		os.Exit(1)
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
	if data != SELECTED_FMT {
		p("Incorrect bit format! Change requested format in file")
		os.Exit(1)
	}
	format := 16
	switch {
	case data == AFMT_S16_LE:
		convFactor = MaxInt16
	case data == AFMT_S32_LE:
		convFactor = MaxInt32
		format = 32
	case data == AFMT_S8:
		convFactor = MaxInt8
		format = 8
	default:
		p("\n--Incompatible bit format! Change requested format in file--\n")
		os.Exit(1)
	}

	// set channels here, stereo or mono
	req = SNDCTL_DSP_CHANNELS
	data = CHANNELS
	_, _, ern = syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(f.Fd()),
		uintptr(req),
		uintptr(unsafe.Pointer(&data)),
	)
	if ern != 0 {
		p("channels error") // do something else here
		time.Sleep(time.Second)
	}
	if data != CHANNELS {
		p("--requested channels not accepted--\n")
		p("--frequency accuracy may be affected!--\n") // covered in future upgrade
		time.Sleep(500 * time.Millisecond)
	}
	channels := ""
	switch data {
	case STEREO:
		channels = "stereo"
	case MONO:
		channels = "mono"
	default:
		// report error
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
		p("set rate:", ern) // do something else here
		time.Sleep(time.Second)
	}
	if data != SAMPLE_RATE {
		p("--requested sample rate not accepted--\n")
		time.Sleep(500 * time.Millisecond)
	}
	SampleRate = float64(data)
	initialR8 = SampleRate
	display.SR = SampleRate

	go SoundEngine(w, format)
	go infodisplay()
	msg("clear")
	go mouseRead()

	// process wav
	var wavsLoaded bool
	var wavSlice wavs
	wavNames := ""
	if wavSlice, wavsLoaded = decodeWavs(); !wavsLoaded {
		msg("no wavs loaded")
	}
	msg("")

	// signals map with predefined constants, mutable
	sg := map[string]float64{}
	transfer.Wavs = make([][]float64, 0, len(wavSlice))
	wmap := map[string]bool{}
	for _, w := range wavSlice {
		wavNames += w.Name + " "
		wmap[w.Name] = true
		transfer.Wavs = append(transfer.Wavs, w.Data)
	}
	TLlen = int(SampleRate * TAPE_LENGTH)
	//signals slice with reserved signals
	reserved := []string{ // order is important
		"dac",
		"", // nil signal for unused operand
		"pitch",
		"tempo",
		"mousex",
		"mousey",
		"butt1",
		"butt3",
		"butt2",
		"grid",
	}
	// add 12 reserved signals for inter-list signals
	lenReserved := len(reserved) // use this as starting point for exported signals
	Sigs = []int{2, 3, 9}        // pitch,tempo,grid
	for i := 0; i < EXPORTED_LIMIT; i++ {
		reserved = append(reserved, sf("***%d", i+lenReserved)) // placeholder
	}
	var lenExported int = 0
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
		operators[k] = ops{Opd: hasOpd}
	}
	var funcsave bool
	dispListings := []listing{}
	code := &dispListings
	priorMutes := []float64{}
	solo := map[int]bool{}
	unsolo := []float64{}
	reload := [2]int{-1, -1}

	go func() { // watchdog, anonymous to use variables in scope
		// assumes no panic while paused
		// This function will remove previously added listing and restart the sound engine
		for {
			select {
			case <-stop:
				if exit {
					go SoundEngine(w, format)
					break
				}
				if !started {
					continue
				}
				msg("%ssound engine halted... removing previous listing%s", italic, reset)
				stop = make(chan struct{})
				if reload[1] < 0 || reload[1] > len(transfer.Listing)-1 {
					reload[1] = len(transfer.Listing) - 1 // default is delete last added listing
				}
				go SoundEngine(w, format)
				transfer.Listing[reload[1]] = listing{{Op: "deleted"}}
				dispListings[reload[1]] = listing{{Op: "deleted"}}
				display.List--
				transmit <- true
				<-accepted
				if !save(*code, "displaylisting.json") {
					msg("%slisting display not updated, check %s'displaylisting.json'%s exists%s",
						italic, reset, italic, reset)
				}
				msg("\tSound Engine restarted")
				time.Sleep(500 * time.Millisecond) // wait to stabilise
			default:
				// nop
			}
			if exit { // should be in default case?
				break
			}
			time.Sleep(100 * time.Millisecond) // coarse loop timing
		}
	}()

	s := bufio.NewScanner(os.Stdin)
	s.Split(bufio.ScanWords)
	ext = false
start:
	for { // main loop
		newListing := listing{}
		dispListing := listing{}
		sig = make([]float64, len(reserved), 30) // capacity is nominal
		// signals map with predefined constants, mutable
		sg = map[string]float64{ // reset sg map because used by function add
			"ln2":      Ln2,
			"ln3":      Log(3),
			"ln5":      Log(5),
			"E":        E,   // e
			"Pi":       Pi,  // π
			"Phi":      Phi, // φ
			"invSR":    1 / SampleRate,
			"SR":       SampleRate,
			"Epsilon":  SmallestNonzeroFloat64, // ε, epsilon
			"wavR":     1.0 / WAV_LENGTH,
			"semitone": Pow(2, 1.0/12),
			"Tau":      2 * Pi, // 2π
			"ln7":      Log(7),
			"^freq":    NOISE_FREQ, // default frequency for setmix, suitable for noise
			"null":     0,
		}
		for i, w := range wavSlice {
			sg[w.Name] = float64(i)
			sg["l."+w.Name] = float64(len(w.Data)-1) / WAV_LENGTH
		}
		out := map[string]struct{}{}
		for _, v := range reserved {
			switch v {
			case "tempo", "pitch", "grid":
				continue
			}
			out[v] = struct{}{}
		}
		fIn := false // true = inside function definition
		st := 0      // func def start
		fun := 0     // don't worry the fun will increase!
		hasTape := false
		reload[0] = -1

	input:
		for { // input loop
			var num struct {
				Ber float64
				Is  bool
			}
			pf("\033[H\033[2J") // this clears prior error messages!
			p("> Format:", format, "bit")
			p("> Output:", channels)
			p("> Rate:", SampleRate, "Hz")
			pf("\n%sSyntə%s running...\n", cyan, reset)
			pf("Protect your hearing above 85dB SPL\n\n")
			if len(wavNames) > 0 {
				pf(" %swavs:%s %s\n\n", italic, reset, wavNames)
			}
			pf("\n%s%d%s:", cyan, len(dispListings), reset)
			for i, o := range dispListing {
				switch dispListing[i].Op {
				case "in", "pop", "tap", "index", "]", "from":
					pf("\t  %s%s %s%s\n", yellow, o.Op, o.Opd, reset)
				default:
					_, f := funcs[dispListing[i].Op]
					switch {
					case f:
						pf("\t\u21AA %s%s %s%s%s\n", magenta, o.Op, yellow, o.Opd, reset)
					default:
						pf("\t\u21AA %s%s %s%s\n", yellow, o.Op, o.Opd, reset)
					}
				}
			}
			op, opd := "", ""
			pf("\t  ")
			pf("%s", yellow)
			if !s.Scan() {
				s = bufio.NewScanner(os.Stdin)
				s.Split(bufio.ScanWords)
				ext = false
				continue
			}
			pf("%s", reset)
			op = s.Text()
			op = strings.TrimSuffix(op, ",") // to allow comma separation of tokens
			op2, in := operators[op]
			if !in {
				msg("%soperator or function doesn't exist:%s %s", italic, reset, op)
				s = bufio.NewScanner(os.Stdin) // empty scanner and return to std input
				s.Split(bufio.ScanWords)
				ext = false
				continue
			}
			_, f := funcs[op]
			var operands = []string{}
			if op2.Opd { // parse second token
				pf("%s", yellow)
				if !s.Scan() {
					s = bufio.NewScanner(os.Stdin)
					s.Split(bufio.ScanWords)
					ext = false
					continue
				}
				opd = s.Text()
				opd = strings.TrimSuffix(opd, ",") // to allow comma separation of tokens
				pf("%s", reset)
				if opd == "_" {
					continue
				}
				operands = strings.Split(opd, ",")
				if !f && len(operands) > 1 {
					msg("%s%sonly functions can have multiple operands%s", red, italic, reset)
					continue
				}
				wav := wmap[opd] && op == "wav" // wavs can start with a number
				if strings.ContainsAny(opd[:1], "+-.0123456789") && !wav && !f {
					if num.Ber, num.Is = parseType(opd, op); !num.Is {
						continue input // parseType will report error
					}
				}
			}

			if f { // parse function
				function := make(listing, len(funcs[op]))
				copy(function, funcs[op])
				s := sf(".%d", fun)
				type mm struct{ at, at1, at2 bool }
				M := mm{}
				for i, o := range function {
					if len(o.Opd) == 0 {
						continue
					}
					switch o.Opd {
					case "dac", "tempo", "pitch", "grid": // should be all reserved?
						continue
					case "@":
						M.at = true
					case "@1":
						M.at1 = true
					case "@2":
						M.at2 = true
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
					function[i].Opd += s // rename signal
					if o.Op == "out" {
						out[function[i].Opd] = struct{}{}
					}

				}
				m := 0
				switch M {
				case mm{false, false, false}:
					// nop
				case mm{true, false, false}:
					m = 1
				case mm{true, true, false}:
					m = 2
				case mm{true, true, true}:
					m = 3
				default:
					msg("malformed function") // probably not needed
					continue input
				}
				l := len(operands)
				if m < l {
					switch {
					case l-m == 1:
						msg("%slast operand ignored%s", italic, reset)
					case l-m > 1:
						msg("%slast %d operands ignored%s", italic, l-m, reset)
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
				for i, opd := range operands { // opd shadowed
					if operands[i] == "" {
						msg("empty argument %d", i+1)
						continue input
					}
					wav := wmap[opd] // wavs can start with a number
					if strings.ContainsAny(opd[:1], "+-.0123456789") && !wav {
						if _, ok := parseType(opd, ""); !ok {
							continue input // parseType will report error
						}
					}
				}
				for i, o := range function { // could this be replaced by loading into scanner instead?
					if len(o.Opd) == 0 {
						continue
					}
					switch o.Opd {
					case "@":
						o.Opd = operands[0]
					case "@1":
						o.Opd = operands[1]
					case "@2":
						o.Opd = operands[2]
					}
					function[i] = o
				}
				fun++                                                             // more fun, yay
				dispListing = append(dispListing, listing{{Op: op, Opd: opd}}...) // only display name
				newListing = append(newListing, function...)
				if o := newListing[len(newListing)-1]; o.Op == "out" && o.Opd == "dac" && !fIn {
					break input
				}
				continue
			}

			switch op {
			case ":": //mode setting
				if opd == "p" { // toggle pause/play
					switch {
					case display.Paused:
						opd = "play"
					default:
						opd = "pause"
					}
				}
				switch opd {
				case "exit":
					p("\nexiting...")
					if display.Paused {
						<-pause
					}
					exit = true
					if started {
						<-stop
					}
					p("Stopped")
					close(infoff)
					d := Disp{Mode: "off", Info: "clear"} // clear info display on exit
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
						for i := range mute { // save, and mute all
							priorMutes[i] = mute[i]
							mute[i] = 0
						}
						time.Sleep(75 * time.Millisecond) // wait for mutes
						pause <- true
						display.Paused = true
					} else if !started {
						msg("%snot started%s", italic, reset)
					}
					continue
				case "play":
					if !display.Paused {
						continue
					}
					for i := range mute { // restore mutes
						mute[i] = priorMutes[i]
					}
					time.Sleep(75 * time.Millisecond) // wait for mutes
					<-pause
					display.Paused = false
					continue
				case "unprotected":
					protected = !protected
					continue
				case "clear":
					msg("clear")
					continue
				case "verbose":
					switch code {
					case &dispListings:
						code = &transfer.Listing
					case &transfer.Listing:
						code = &dispListings
					}
					display.Verbose = !display.Verbose
					if !save(*code, "displaylisting.json") {
						msg("%slisting display not updated, check %s'displaylisting.json'%s exists%s",
							italic, reset, italic, reset)
					}
					continue
				case "stats":
					if !started {
						continue
					}
					stats := new(debug.GCStats)
					debug.ReadGCStats(stats)
					msg("___GC statistics:___")
					msg("No.: %v", stats.NumGC)
					msg("Tot.: %v", stats.PauseTotal)
					msg("Avg.: %v", stats.PauseTotal/time.Duration(stats.NumGC))
					msg("Distr.: %v", stats.PauseQuantiles)
					continue
				case "mc": // mouse curve, exp or lin
					mc = !mc
					continue
				case "muff": // Mute Off
					muteSkip = !muteSkip
					msg("mute skip = %v", muteSkip)
					continue

					/*////////////////////////////////////////////
					case "ds":
						DS++
						nyfC = 1 / (1 + (DS / Pi))
						//SampleRate = initialR8 / DS
						//display.SR = SampleRate
						info <- sf("sample rate divided by %.fx to %.fHz", DS, initialR8/DS)
						continue
						/*/ ///////////////////////////////////////////

				default:
					msg("%s%sunrecognised mode%s", red, italic, reset)
					continue
				}
			case "load", "ld":
				inputF, rr := os.Open("listings/" + opd + ".syt")
				if e(rr) {
					msg("%v", rr)
					continue
				}
				s = bufio.NewScanner(inputF)
				s.Split(bufio.ScanWords)
				ext = true
				continue
			case "save": // change to opd is index and prompt for name.
				n, rr := strconv.Atoi(opd)
				if e(rr) || n < 0 || n > len(transfer.Listing)-1 {
					msg("%s%soperand not valid%s", red, italic, reset)
					continue
				}
				pf("\tName: ")
				s.Scan()
				f := s.Text()
				f = "listings/" + f + ".syt"
				files, rr := os.ReadDir("./listings/")
				if e(rr) {
					msg("unable to access 'listings/': %s", rr)
					continue
				}
				for _, file := range files {
					ffs := file.Name()
					if ffs[len(ffs)-4:] != ".syt" {
						continue
					}
					if ffs == f {
						msg("duplicate name!")
						continue input
					}
				}
				content := ""
				for _, d := range dispListings[n] {
					content += d.Op + " " + d.Opd + "\n"
				}
				if rr := os.WriteFile(f, []byte(content), 0666); e(rr) {
					msg("%v", rr)
					continue
				}
				msg("...%d saved to %s", n, f)
				continue
			case "in":
				// nop
			case "out", "out+":
				_, in := out[opd]
				ExpSig := false
				for i := lenReserved; i < lenReserved+lenExported; i++ {
					if reserved[i] == opd {
						ExpSig = true
					}
				}
				switch {
				case num.Is:
					msg("%s%soutput to number not permitted%s", red, italic, reset)
					continue
				case in && opd[:1] != "^" && opd != "dac" && !ExpSig && op != "out+":
					msg("%s%sduplicate output to signal, c'est interdit%s", red, italic, reset)
					continue
				case opd == "@":
					msg("%s%scan't send to @, represents function operand%s", red, italic, reset)
					continue
				}
				out[opd] = struct{}{}
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
				mute[n] = 0         // wintermute
				if display.Paused { // this mute logic is not clear
					for i := range mute { // restore mutes
						if i != n {
							mute[i] = priorMutes[i]
						}
					}
					<-pause
					display.Paused = false
					msg("\t%splay resumed...%s", italic, reset)
				}
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
					op, opd = "out", "dac"
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
				o := operators[newListing[st].Opd]
				o.Opd = hasOpd
				operators[newListing[st].Opd] = o
				funcs[newListing[st].Opd] = newListing[st+1:]
				msg("%sfunction assigned to:%s %s", italic, reset, newListing[st].Opd)
				fIn = false
				continue start
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
			case "tape":
				if hasTape {
					msg("%sonly one tape per listing%s", italic, reset)
					continue
				}
				hasTape = true
			case "degrade":
				if len(transfer.Listing) == 0 {
					msg("%scan't use degrade in first listing%s", italic, reset)
					continue
				}
				msg("%sno register is safe...%s", italic, reset)
			case "erase", "e":
				n, rr := strconv.Atoi(opd)
				if e(rr) {
					msg("%s%soperand not an integer%s", red, italic, reset)
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
				if !wmap[opd] && opd != "@" {
					msg("%s%sname isn't in wav list%s", red, italic, reset)
					continue
				}
			case "mute", ".mute", "m":
				i, rr := strconv.Atoi(opd)
				if e(rr) {
					msg("%s%soperand not an integer%s", red, italic, reset)
					continue
				}
				if i < 0 || i > len(transfer.Listing)-1 {
					msg("listing index does not exist")
					continue
				}
				if display.Paused && i < len(transfer.Listing) { // exclude present listing
					priorMutes[i] = 1 - priorMutes[i]
				} else {
					mute[i] = 1 - mute[i]
				}
				display.Mute[i] = mute[i] == 0 // convert binary to boolean
				if op[:1] == "." && len(newListing) > 0 {
					dispListing = append(dispListing, listing{{Op: "mix"}}...)
					newListing = append(newListing, listing{{Op: "setmix", Opd: "^freq"}}...) // hacky
					op, opd = "out", "dac"
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
					msg("%s%soperand not an integer%s", red, italic, reset)
					continue
				}
				i %= len(transfer.Listing) + 1
				if i < 0 {
					i = -i
				}
				opd = strconv.Itoa(i)
			case "solo", ".solo", "s":
				if len(transfer.Listing) == 0 {
					msg("no running listings")
					continue
				}
				i, rr := strconv.Atoi(opd)
				if e(rr) {
					msg("%s%soperand not an integer%s", red, italic, reset)
					continue
				}
				i %= len(transfer.Listing) + 1 // +1 to allow solo of current listing input when sent
				if i < 0 {
					i = -i
				}
				if solo[i] {
					for i := range mute { // i is shadowed
						mute[i] = unsolo[i]
						priorMutes[i] = unsolo[i] // shonky
						display.Mute[i] = mute[i] == 0
						solo[i] = false
					}
				} else {
					for i := range mute { // i is shadowed
						unsolo[i] = mute[i]
						mute[i] = 0
						priorMutes[i] = 0 // shonky
						display.Mute[i] = true
					}
					if i < len(transfer.Listing) { // only solo extant listings, new will be unmuted
						mute[i] = 1
						priorMutes[i] = 1 // shonky
						display.Mute[i] = false
					}
					solo[i] = true
				}
				if op[:1] == "." && len(newListing) > 0 {
					dispListing = append(dispListing, listing{{Op: "mix"}}...)
					newListing = append(newListing, listing{{Op: "setmix", Opd: "^freq"}}...) // hacky
					op, opd = "out", "dac"
					break
				}
				continue
			case "release":
				if opd == "time" {
					msg("%slimiter release is:%s %.4gms", italic, reset,
						-1000/(Log(release)*SampleRate/Log(8000)))
					continue
				}
				if !num.Is {
					msg("not a number")
					continue
				}
				v := num.Ber
				if v < 1.041e-6 { // ~20s
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
					priorMutes[i] = 1 // shonky
					display.Mute[i] = false
				}
				continue
			case "from":
				if len(transfer.Listing) == 0 {
					msg("no running listings")
					continue
				}
			case "noise":
				newListing = append(newListing, listing{{Op: "push"}, {Op: "in", Opd: sf("%v", NOISE_FREQ)}, {Op: "out", Opd: "^freq"}, {Op: "pop"}}...)
			case "[":
				if _, ok := funcs[opd]; ok {
					msg("%s%swill overwrite existing function!%s", red, italic, reset)
				} else if _, ok := operators[opd]; ok {
					msg("%s%sduplicate of extant operator, use another name%s", red, italic, reset)
					continue
				}
				if opd == "deleted" {
					msg("%s%sname not permitted%s", red, italic, reset)
					continue
				}
				st = len(newListing) // because current input hasn't been added yet
				fIn = true
				msg("%sbegin function definition%s %s", italic, reset, opd)
				msg("%suse @ for operand signal%s", italic, reset)
			case "ls":
				if opd == "l" {
					opd += "istings"
				}
				dir := "./" + opd
				files, rr := os.ReadDir(dir)
				if e(rr) {
					msg("unable to access '%s': %s", dir, rr)
					continue
				}
				ext := ""
				switch dir {
				case "./wavs":
					ext = ".wav"
				default:
					ext = ".syt"
				}
				ls := ""
				for _, file := range files {
					f := file.Name()
					if f[len(f)-4:] != ext {
						continue
					}
					ls += f[:len(f)-4] + " "
				}
				if len(ls) == 0 {
					msg("no files")
					continue
				}
				msg("%s", ls)
				continue
			case "ct":
				if n, rr := strconv.ParseFloat(opd, 64); !e(rr) { // change to accept db type?
					ct = Pow(10, n/20)
					msg("%sclip threshold set to %.1g%sx", italic, ct, reset)
				}
				continue
			case "rld", "r":
				n, rr := strconv.Atoi(opd)
				if e(rr) {
					msg("%s%soperand not an integer%s", red, italic, reset)
					continue
				}
				reload[0] = n
				if n < 0 {
					n = -n
				}
				f := sf(".temp/%d.syt", n)
				inputF, rr := os.Open(f)
				if e(rr) {
					msg("%v", rr)
					reload[0] = -1
					continue
				}
				s = bufio.NewScanner(inputF)
				s.Split(bufio.ScanWords)
				ext = true
				continue
			case "rpl", ".rpl":
				n, rr := strconv.Atoi(opd)
				if e(rr) {
					msg("%s%soperand not an integer%s", red, italic, reset)
					continue
				}
				if n >= len(transfer.Listing) {
					msg("listing doesn't exist")
					continue
				}
				reload[0] = n
				msg("%swill replace listing %s%d%s on launch%s", italic, reset, n, italic, reset)
				if op[:1] == "." && len(newListing) > 0 {
					dispListing = append(dispListing, listing{{Op: "mix"}}...)
					newListing = append(newListing, listing{{Op: "setmix", Opd: "^freq"}}...) // hacky
					op, opd = "out", "dac"
					break
				}
				continue
			case "all":
				if len(transfer.Listing) == 0 {
					msg("all is meaningless in first listing")
					continue
				}
			case "/":
				if !num.Is {
					msg("divide by signal forbidden :)")
					continue
				}
			default:
				// nop
			}
			// end of switch

			// process exported signals
			alreadyIn := false
			for _, v := range reserved {
				if v == opd {
					alreadyIn = true
				}
			}
			_, inSg := sg[opd]
			if !inSg && !alreadyIn && !num.Is && unicode.IsUpper([]rune(opd)[0]) {
				if lenExported > EXPORTED_LIMIT {
					msg("we've ran out of exported signals :(")
					continue
				}
				reserved[lenReserved+lenExported] = opd
				Sigs = append(Sigs, lenReserved+lenExported)
				lenExported++
				msg("%s%s added to exported signals%s", opd, italic, reset)
			}

			// add to listing
			if len(dispListing) == 0 || op != "mix" && dispListing[len(dispListing)-1].Op != "mix" {
				dispListing = append(dispListing, listing{{Op: op, Opd: opd}}...)
			}
			newListing = append(newListing, listing{{Op: op, Opd: opd}}...)
			if fIn {
				continue
			}
			// break and launch
			switch op {
			case "out":
				if opd == "dac" {
					break input
				}
			case ".out", ".>sync", ".level", "//":
				break input
			}
			if !ext {
				msg(" ")
			}

		}
		// end of input

		for _, o := range newListing {
			if _, in := sg[o.Opd]; in || len(o.Opd) == 0 {
				continue
			}
			if strings.ContainsAny(o.Opd[:1], "+-.0123456789") { // wavs already in sg map
				sg[o.Opd], _ = parseType(o.Opd, o.Op) // number assigned, error checked above
			} else { // assign initial value
				i := 0
				if o.Opd[:1] == "^" {
					i++
				}
				switch o.Opd[i : i+1] {
				case "'":
					sg[o.Opd] = 1
				case "\"":
					sg[o.Opd] = 0.5
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
				for i, pre := range reserved { // reserved signals are added in order
					if o.Opd == pre {
						o.N = i // shadowed
					}
				}
				o.Opn = operators[o.Op].N
				newListing[ii] = o
			}
			i++
		}

		if display.Paused { // restart on launch if paused
			for i := range mute { // restore mutes
				mute[i] = priorMutes[i]
			}
			time.Sleep(51 * time.Millisecond) // wait for mutes
			<-pause
			display.Paused = false
			msg("\t%splay resumed...%s", italic, reset)
		}
		//transfer to sound engine // or if reload, replace existing at that index
		if reload[0] < 0 || reload[0] > len(transfer.Listing)-1 {
			dispListings = append(dispListings, dispListing)
			transfer.Listing = append(transfer.Listing, newListing)
			transfer.Signals = append(transfer.Signals, sig)
			mute = append(mute, 1)
			priorMutes = append(priorMutes, 1)
			unsolo = append(unsolo, 1)
			display.Mute = append(display.Mute, false)
			level = append(level, 1)
			display.List++
		} else {
			dispListings[reload[0]] = dispListing
			transfer.Listing[reload[0]] = newListing
			transfer.Signals[reload[0]] = sig
		}
		reload[1] = reload[0] // save index of previous added/reloaded listing
		transmit <- true
		<-accepted
		if !started {
			started = true
		}
		// save listing as <n>.syt for the reload
		f := sf(".temp/%d.syt", reload[0])
		if reload[0] < 0 {
			f = sf(".temp/%d.syt", len(transfer.Listing)-1)
		}
		content := ""
		for _, d := range dispListing {
			content += d.Op + " " + d.Opd + "\n"
		}
		if rr := os.WriteFile(f, []byte(content), 0666); e(rr) {
			msg("%v", rr)
			continue
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
		msg(".")
	}
}

// parseType() evaluates conversion of types
func parseType(expr, op string) (n float64, b bool) {
	switch op { // ignore for following operators
	case "mute", ".mute", "del", ".del", "solo", ".solo", "level", ".level", "from", "load", "save", "m", "reload", "r", "rpl", "s", "ld", "ls", "[": // this is a bit messy
		return 0, true
	default:
		// process expression below
	}
	/*if !strings.ContainsAny(expr[:1], "+-.0123456789") {
		return 0, true
	}*/
	switch {
	case len(expr) > 1 && expr[len(expr)-1:] == "!":
		if n, b = evaluateExpr(expr[:len(expr)-1]); !b {
			return 0, false
		}
		msg("proceed with caution...")
	case len(expr) > 2 && expr[len(expr)-2:] == "ms":
		if n, b = evaluateExpr(expr[:len(expr)-2]); !b {
			msg("erm s")
			return 0, false
		}
		n = 1 / ((n / 1000) * SampleRate)
		if !nyquist(n) {
			return 0, false
		}
	case len(expr) > 1 && expr[len(expr)-1:] == "s":
		if n, b = evaluateExpr(expr[:len(expr)-1]); !b {
			return 0, false
		}
		n = 1 / (n * SampleRate)
		if !nyquist(n) {
			return 0, false
		}
	case len(expr) > 2 && expr[len(expr)-2:] == "hz":
		if n, b = evaluateExpr(expr[:len(expr)-2]); !b {
			return 0, false
		}
		n /= SampleRate
		if !nyquist(n) {
			return 0, false
		}
	case len(expr) > 2 && expr[len(expr)-2:] == "db": // 0dB = 1
		if n, b = evaluateExpr(expr[:len(expr)-2]); !b {
			return 0, false
		}
		n /= 20
		n = Pow(10, n)
	case len(expr) > 3 && expr[len(expr)-3:] == "bpm":
		if n, b = evaluateExpr(expr[:len(expr)-3]); !b {
			return 0, false
		}
		if n > 300 {
			msg("gabber territory")
		}
		if n > 3000 {
			msg("%fbpm? You're 'aving a larf mate", n)
			return 0, false
		}
		if n < 10 {
			msg("erm, why?")
		}
		n /= 60
		n /= SampleRate
	case len(expr) > 4 && expr[len(expr)-4:] == "mins":
		if n, b = evaluateExpr(expr[:len(expr)-4]); !b {
			return 0, false
		}
		n *= 60
		n = 1 / (n * SampleRate)
	default:
		if n, b = evaluateExpr(expr); !b {
			return 0, false
		}
		if Abs(n) > 20 {
			msg("exceeds sensible values, do you mean %.[1]fhz, %.[1]fs, or %.[1]fbpm?", n)
			return 0, false
		}
	}
	if IsInf(n, 0) || n != n {
		msg("number not useful")
		return 0, false
	}
	return n, true
}
func nyquist(n float64) bool {
	ny := 2e4 / SampleRate
	if bounds(n, ny) {
		msg("%sinaudible frequency >20kHz%s", italic, reset)
		if bounds(n, 1) {
			msg("and frequency out of range, not accepted")
			return false
		}
	}
	return true
}
func bounds(a, b float64) bool {
	return a < -b || a > b
}

// evaluateExpr() does what it says on the tin
func evaluateExpr(expr string) (float64, bool) {
	opds := []string{expr}
	var rr error
	var n, n2 float64
	var op string
	for _, v := range []string{"*", "/", "+", "-"} {
		if strings.Contains(strings.TrimPrefix(expr, "-"), v) {
			opds = strings.SplitN(expr, v, 2)
			if strings.HasPrefix(expr, "-") {
				opds = strings.SplitN(strings.TrimPrefix(expr, "-"), v, 2)
				opds[0] = "-" + opds[0]
			}
			if strings.Contains(expr, "e") { // don't compute exponential notation
				opds = []string{expr}
			}
			op = v
			break
		}
	}
	if n, rr = strconv.ParseFloat(opds[0], 64); e(rr) {
		msg("not a number or a name in first part of expression")
		return 0, false
	}
	if len(opds) == 1 {
		return n, true
	}
	if len(opds) > 2 {
		msg("%s%s third operand in expression ignored%s", red, italic, reset)
		return 0, false
	}
	if n2, rr = strconv.ParseFloat(opds[1], 64); e(rr) {
		msg("not a number or a name in second part of expression")
		return 0, false
	}
	switch op {
	case "*":
		n *= n2
	case "/":
		n /= n2
	case "+":
		n += n2
	case "-":
		n -= n2
	}
	return n, true
}

// decodeWavs is a somewhat hacky implementation that works for now.
// A maximum of WAV_LENGTH samples are sent to the main routine.
// All files are currently converted from stereo to mono.
// Differing sample rates are not currently converted. Header is assumed to be 44 bytes.
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
		if len(data) < to {
			to = len(data[44:]) / channels
		}
		//msg("len wav: %v, to: %v", len(data), to)
		rb := bytes.NewReader(data[44:])
		switch bits { // generify these cases
		case 16:
			samples := make([]int16, to)
			rr := binary.Read(rb, binary.LittleEndian, &samples)
			if rr == io.ErrUnexpectedEOF {
				msg("winging it")
			} else if e(rr) {
				msg("error decoding: %s %s", file, rr)
				continue
			}
			// convert to syntə format
			s := 0.0
			wav.Data = make([]float64, 0, to)
			//for i := 0; i < to; i += channels {
			for i := 0; i < to-channels+1; i += channels {
				if channels == 2 {
					s = (float64(samples[i]) + float64(samples[i+1])) / (2 * MaxInt16)
				} else {
					s = float64(samples[i]) / MaxInt16
				}
				wav.Data = append(wav.Data, s)
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
			s := 0.0
			wav.Data = make([]float64, 0, to)
			for i := 0; i < to-channels+1; i += channels {
				if channels == 2 {
					s = (float64(samples[i]) + float64(samples[i+1])) / (2 * MaxInt32)
				} else {
					s = float64(samples[i]) / MaxInt32
				}
				wav.Data = append(wav.Data, s)
			}
		case 32:
			samples := make([]int32, to)
			rr := binary.Read(rb, binary.LittleEndian, &samples)
			if e(rr) {
				msg("error decoding: %s %s", file, rr)
				continue
			}
			// convert to syntə format
			s := 0.0
			wav.Data = make([]float64, 0, to)
			for i := 0; i < to-channels+1; i += channels {
				if channels == 2 {
					s = (float64(samples[i]) + float64(samples[i+1])) / (2 * MaxInt32)
				} else {
					s = float64(samples[i]) / MaxInt32
				}
				wav.Data = append(wav.Data, s)
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
		msg("%s\t%s  SR: %5d  bits: %2d  %.3gs", file, c, SR, bits, t)
	}
	if len(w) == 0 {
		return nil, false
	}
	return w, true
}

// quick and basic decode of mouse bytes
func mouseRead() {
	file := ""
	switch runtime.GOOS {
	case "freebsd":
		file = "/dev/bpsm0"
	case "linux":
		file = "/dev/input/mice"
	default:
		msg("mouse not supported")
		return
	}
	mf, rr := os.Open(file)
	if e(rr) {
		p("error opening '"+file+"':", rr)
		msg("mouse unavailable")
		return
	}
	defer mf.Close()
	m := bufio.NewReader(mf)
	bytes := make([]byte, 3)
	mx, my := 0.0, 0.0
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
				mx += float64(int8(bytes[1]-255)) / 255
			} else {
				mx += float64(int8(bytes[1])) / 255
			}
		}
		if bytes[2] != 0 {
			if bytes[0]>>5&1 == 1 {
				my += float64(int8(bytes[2]-255)) / 255
			} else {
				my += float64(int8(bytes[2])) / 255
			}
		}
		if e(rr) {
			pf("%serror reading %s: %v\r", reset, file, rr)
			msg("error reading mouse data")
			return
		}
		if exit { // necessary?
			break
		}
		if mc {
			mouse.X = Pow(10, mx/10)
			mouse.Y = Pow(10, my/10)
		} else {
			mouse.X = mx / 5
			mouse.Y = mx / 5
		}
		display.MouseX = mouse.X
		display.MouseY = mouse.Y
		time.Sleep(42 * time.Microsecond) // coarse loop timing
	}
}

// infodisplay won't run during fadeout
func infodisplay() {
	file := "infodisplay.json"
	n := 0
loop:
	for {
		display.Protect = protected

		select {
		case infoString := <-info:
			display.Info = infoString
		case carryOn <- true: // semaphore: received
			// continue
		case <-infoff:
			display.Info = sf("clear")
			time.Sleep(20 * time.Millisecond) // coarse loop timing
			display.Info = sf("%sSyntə closed%s", italic, reset)
			save(display, file)
			break loop
		default:
			// passthrough
		}
		if !save(display, file) {
			pf("%sinfo display not updated, check file %s%s%s exists%s\n",
				italic, reset, file, italic, reset)
			time.Sleep(2 * time.Second)
		}
		time.Sleep(20 * time.Millisecond) // coarse loop timing
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
// except where the calculations don't complete in time under heavy load and the soundcard driver buffer underruns.
// If the loop time exceeds the sample rate over number of samples given by RATE the Sound Engine will panic
func SoundEngine(w *bufio.Writer, bits int) {
	defer close(stop)
	defer w.Flush()
	output := func(w *bufio.Writer, f float64) {
		if rr := binary.Write(w, binary.LittleEndian, int16(f)); e(rr) {
			panic("writing to soundcard failed!")
		}
	}
	switch bits {
	case 8:
		output = func(w *bufio.Writer, f float64) {
			binary.Write(w, binary.LittleEndian, int8(f))
		}
	case 16:
		// already assigned
	case 32:
		output = func(w *bufio.Writer, f float64) {
			binary.Write(w, binary.LittleEndian, int32(f))
		}
	default:
		msg("unable to write to soundcard!")
		return
	}

	const (
		Tau  = 2 * Pi
		RATE = 2 << 14
	)

	var (
		no     noise   = noise(time.Now().UnixNano())
		r      float64                                   // result
		l, h   float64 = 1, 2                            // limiter, hold
		dac    float64                                   // output
		dac0   float64                                   // formatted output
		env    float64 = 1                               // for exit envelope
		penv   float64 = Pow(1e-4, 1/(SampleRate*50e-3)) // approx -80dB in 50ms
		peak   float64                                   // vu meter
		dither float64
		n      int // loop counter

		rate     time.Duration = time.Duration(7292) // loop timer, initialised to approximate resting rate
		lastTime time.Time     = time.Now()
		rates    [RATE]time.Duration
		t        time.Duration
		//DS       float64 = 1 // down-sample, integer as float type
		s  float64 = 1 // sync=0
		p  bool        // play/pause, shadows
		ii int         // sync intermediate

		mx, my float64 = 1, 1 // mouse smooth intermediates
		hpf, x float64        // DC-blocking high pass filter
		hpf2560, x2560,
		hpf160, x160,
		det float64 // limiter detection
		lpf50, lpf1522,
		deemph float64 // de-emphasis
		smR8             = 40.0 / SampleRate
		hroom            = (convFactor - 1.0) / convFactor // headroom for positive dither
		c                float64                           // mix factor
		rms, rr, prevRms float64
		RMS              [RMS_INT]float64
		pd               int
		nyf              float64 // nyquist filtering
	)
	no *= 77777777777 // force overflow
	defer func() {    // fail gracefully
		if p := recover(); p != nil { // p is shadowed
			fade := Pow(1e-4, 1/(SampleRate*50e-3)) // approx -80dB in 50ms
			for i := 2400; i >= 0; i-- {
				dac0 *= fade
				output(w, dac0) // left
				output(w, dac0) // right
			}
			msg("%v", p)
		}
	}()
	_ = dac0

	<-transmit // load first listing(s) and start SoundEngine
	// excess capacities unnecessary?
	listings := make([]listing, len(transfer.Listing), len(transfer.Listing)+24)
	sigs := make([][]float64, len(transfer.Signals), len(transfer.Signals)+23)
	stacks := make([][]float64, len(transfer.Listing), len(transfer.Listing)+21)
	wavs := make([][]float64, len(transfer.Wavs), MAX_WAVS)
	tapes := make([][]float64, 0, 26)
	copy(listings, transfer.Listing) // is this pointless as refers to same underlying array anyway?
	copy(sigs, transfer.Signals)
	copy(wavs, transfer.Wavs)
	tapes = make([][]float64, len(transfer.Listing))
	for i := range tapes { // i is shadowed
		tapes[i] = make([]float64, TLlen)
	}
	tf := make([]float64, 1)
	accepted <- true
	sync := make([]float64, len(transfer.Listing))
	syncInhibit := make([]bool, len(transfer.Listing), len(transfer.Listing)+27) // inhibitions
	peakfreq := make([]float64, len(transfer.Listing), len(transfer.Listing)+28) // peak frequency for setlevel
	for i := range peakfreq {
		peakfreq[i] = 20 / SampleRate
	}
	m := make([]float64, len(transfer.Listing), len(transfer.Listing)+29)  // filter intermediate for mute
	lv := make([]float64, len(transfer.Listing), len(transfer.Listing)+30) // filter intermediate for level

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
			// add additional sync inhibit and tape. Assumes at most one listing addeded per transfer.
			if len(transfer.Listing) > len(m) {
				tapes = append(tapes, make([]float64, TLlen))
				tf = append(tf, 0)
				sync = append(sync, 0)
				syncInhibit = append(syncInhibit, false)
				peakfreq = append(peakfreq, 20/SampleRate)
				m = append(m, 0.0)
				lv = append(lv, 0.0)
			}
		default:
			// play
		}

		if n%15127 == 0 { // arbitrary interval all-zeros protection for lfsr
			no ^= 1 << 27
		}
		mx = (mx*765 + mouse.X) / 766 // lpf @ ~10Hz
		my = (my*765 + mouse.Y) / 766

		for i, list := range listings {
			if muteSkip && mute[i] == 0 && m[i] < 1e-6 { // skip muted listings
				continue
			}
			r = 0
			// daisy-chains
			for _, ii := range Sigs {
				sigs[i][ii] = sigs[(i+len(sigs)-1)%len(sigs)][ii]
			}
			// mouse values
			sigs[i][4] = mx
			sigs[i][5] = my
			sigs[i][6] = mouse.Left
			sigs[i][7] = mouse.Right
			sigs[i][8] = mouse.Middle
			for op, o := range list {
				switch o.Opn {
				case 0:
					// nop
				case 1: // "+"
					//r *= DS // temporary hack
					r += sigs[i][o.N]
				case 2: // "out"
					sigs[i][o.N] = r
				case 3: // "out+"
					sigs[i][o.N] += r
				case 4: // "in"
					r = sigs[i][o.N]
				case 5: // "sine"
					r = Sin(Tau * r)
				case 6: // "mod"
					r = Mod(r, sigs[i][o.N])
				case 7: // "gt"
					if r >= sigs[i][o.N] {
						r = 1
					} else {
						r = 0
					}
				case 8: // "lt"
					if r <= sigs[i][o.N] {
						r = 1
					} else {
						r = 0
					}
				case 9: // "mul", "x", "*":
					r *= sigs[i][o.N]
				case 10: // "abs"
					r = Abs(r)
				case 11: // "tanh"
					r = Tanh(r)
				case 12: // "pow"
					if r == 0 && Signbit(sigs[i][o.N]) {
						r = Copysign(1e-308, r) // inverse is within upper range of float
					}
					r = Pow(r, sigs[i][o.N])
				case 13: // "base"
					r = Pow(sigs[i][o.N], r)
					if IsInf(r, 0) { // infinity to '93
						r = Nextafter(r, 0)
					}
				case 14: // "clip"
					switch {
					case sigs[i][o.N] == 0:
						r = Max(0, Min(1, r))
					case sigs[i][o.N] > 0:
						r = Max(-sigs[i][o.N], Min(sigs[i][o.N], r))
					case sigs[i][o.N] < 0:
						r = Min(-sigs[i][o.N], Max(sigs[i][o.N], r))
					}
				case 15: // "noise"
					no.ise() // roll a fresh one
					r *= (2*(float64(no)/MaxUint) - 1)
					//if r > 0.9999 { panic("test") }
					//time.Sleep(5*time.Microsecond) // for testing
				case 16: // "push"
					stacks[i] = append(stacks[i], r)
				case 17: // "pop"
					r = stacks[i][len(stacks[i])-1]
					stacks[i] = stacks[i][:len(stacks[i])-1]
				case 18: // "tape"
					r = Max(-1, Min(1, r))  // hard clip for cleaner reverbs
					tf[i] = (tf[i] + r) / 2 // roll off the top end
					r = tf[i]
					tapes[i][n%TLlen] = r
					{
						t := Min(1/sigs[i][o.N], SampleRate*TAPE_LENGTH)
						r = tapes[i][(n+TLlen-int(t)+1)%TLlen]
					}
				case 20: // "+tap", "tap"
					r += tapes[i][(n+1+int(Abs((TAPE_LENGTH*SampleRate)-1/sigs[i][o.N])))%TLlen]
				case 21: // "f2c"
					r = Abs(r)
					//r = 1 / (1 + 1/(Tau*r))
					r *= Tau
					r /= (r + 1)
				case 22: // "wav"
					r = Abs(r)
					r *= WAV_LENGTH // needs to adapt to shorter samples
					r = wavs[int(sigs[i][o.N])][int(r)%len(wavs[int(sigs[i][o.N])])]
				case 23: // "8bit"
					r = float64(int8(r*sigs[i][o.N])) / sigs[i][o.N]
				case 24: // "index"
					r = float64(i) // * sigs[i][o.N]
				case 25: // "<sync"
					r *= s                      //* (1 - sync[i])
					r += (1 - s) * sigs[i][o.N] //* sync[i]  phase offset
				case 26: // ">sync", ".>sync"
					switch {
					case r <= 0 && s == 1 && !syncInhibit[i]:
						s = 0
						syncInhibit[i] = true
					case s == 0 && syncInhibit[i]: // single sample pulse
						s = 1
						//fallthrough
					case r > 0:
						syncInhibit[i] = false
					}
					r = 0
				case 28: // "level", ".level"
					level[int(sigs[i][o.N])] = r
					//r = 0
				case 29: // "from"
					r = sigs[int(sigs[i][o.N])%len(sigs)][0]
				case 30: // "sgn"
					r = float64(Float64bits(r)>>62) - 1
				case 31: // "deleted"
					sigs[i][0] = 0
				case 32: // "/"
					if sigs[i][o.N] == 0 {
						sigs[i][o.N] = Copysign(1e-308, sigs[i][o.N])
					}
					r /= sigs[i][o.N]
				case 33: // "sub"
					r -= sigs[i][o.N]
				case 34: // "setmix"
					{ // lexical scope
						a := Abs(sigs[i][o.N]) + 1e-6
						d := a/peakfreq[i] - 1
						d = Max(-1, Min(1, d))
						peakfreq[i] += a * (d * smR8)
						if Abs(d) < 0.01 {
							peakfreq[i] = a
						}
					}
					r *= Min(1, 80/(peakfreq[i]*SampleRate+20)) // ignoring density
					//r *= Min(1, Sqrt(20/(peakfreq[i]*SampleRate+20)))
				case 35: // "print"
					pd++ // unnecessary?
					if (pd)%32768 == 0 && !exit {
						info <- sf("listing %d, op %d: %.3g", i, op, r)
						pd += int(no >> 50)
					}
				case 36: // "\\"
					if r == 0 {
						r = Copysign(1e-308, r)
					}
					r = sigs[i][o.N] / r
				case 37: // "degrade" // needs more work
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
				case 39: // reel // deprecated
				case 40: // all
					c := -3.0                   // to avoid being mixed twice
					for ii := 0; ii < i; ii++ { // only read from prior listings
						if sigs[ii][0] == 0 { // avoid silent listings, hacky
							continue
						}
						r += sigs[ii][0]
						c++
					}
					if c < 1 {
						c = 1
					}
					r /= c
				case 41: // rms
					r *= r
					rms += r
					RMS[n%RMS_INT] = r
					rms -= RMS[(n+1)%RMS_INT]
					rr = Sqrt(rms / RMS_INT)
					if rr > sigs[i][o.N] {
						r = rr
						prevRms = rr
					} else {
						r = prevRms
					}
				default:
					// nop, r = r
				}
			}
			//info <- sf("%v\r", sigs[i][0]) // slo-mo debug, will cause long exit! Use ctrl-c
			if sigs[i][0] != sigs[i][0] { // test for NaN
				sigs[i][0] = 0
				panic(sf("listing: %d - NaN", i))
			}
			if IsInf(sigs[i][0], 0) { // infinity to '93
				sigs[i][0] = 0
				panic(sf("listing: %d - overflow", i))
			}
			if sigs[i][0] > ct { // soft clip
				sigs[i][0] = ct + Tanh(sigs[i][0]-ct)
			}
			if sigs[i][0] < -ct {
				sigs[i][0] = Tanh(sigs[i][0]+ct) - ct
			}
			m[i] = (m[i]*152 + mute[i]) / 153 // anti-click filter @ ~20hz
			lv[i] = (lv[i]*7 + level[i]) / 8  // @ 1273Hz
			sigs[i][0] *= lv[i]
			dac += sigs[i][0] * m[i]
			c += m[i] // add mute to mix factor
		}
		if c > 4 {
			dac /= c
		} else {
			dac /= 4
		}
		c = 0
		hpf = (hpf + dac - x) * 0.9994 // hpf = 4.6Hz @ 48kHz SR
		x = dac
		dac = hpf
		if protected { // limiter
			// apply premphasis to detection
			hpf2560 = (hpf2560 + dac - x2560) * 0.749
			x2560 = dac
			hpf160 = (hpf160 + dac - x160) * 0.97948
			x160 = dac
			{
				d := Max(-1, Min(1, dac))
				lpf50 = (lpf50*152.8 + d) / 153.8
				lpf1522 = (lpf1522*5 + d) / 6
				deemph = lpf50 + lpf1522/5.657
			}
			det = Abs(32*hpf2560+5.657*hpf160+dac) / 3
			if det > l {
				l = det // MC
				h = release
			}
			dac /= l
			dac += deemph
			h /= release
			l = (l-1)*(1/(h+1/(1-release))+release) + 1 // snubbed decay curve
			display.GR = l > 1+3e-4
		} else {
			display.GR = false
		}
		if exit {
			dac *= env // fade out
			env *= fade
			if env < 1e-4 {
				save([]listing{listing{{Op: advisory}}}, "displaylisting.json")
				break
			}
		}
		if p {
			dac *= env // fade out
			env *= penv
			if env < 1e-4 {
				pause <- false // blocks until `: play`
				lastTime = time.Now()
				env = 1 // zero attack...
				p = false
			}
		}
		no.ise()
		dither = float64(no) / MaxUint64
		no.ise()
		dither += float64(no)/MaxUint64 - 1
		dac *= hroom
		dac += dither / convFactor // dither dac value ±1 from xorshift lfsr
		if dac > 1 {               // hard clip
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
		dac *= convFactor // convert
		t = time.Since(lastTime)

		for i := 0; i < int(DS); i++ {
			nyf = nyf + nyfC*(dac-nyf)
			//_,_ = nyf, nyfC
			output(w, nyf) // left
			output(w, nyf) // right
		}
		lastTime = time.Now()
		rate += t
		rates[n%RATE] = t // rolling average buffer
		rate -= rates[(n+1)%RATE]
		if n%RATE == 0 {
			display.Load = rate / RATE
			if float64(display.Load)/DS > 1e9/initialR8 {
				//DS++
				//if DS > 3 {
				panic("Sound Engine overloaded")
				/*}
				nyfC = 1 / (1 + (DS / Pi))
				SampleRate = initialR8 / DS
				display.SR = SampleRate
				info <- sf("sample rate divided by %.fx to %.fHz", DS, initialR8/DS)*/
			}
			/*else if DS > 1 && float64(display.Load)/DS < 5e8/initialR8 {
				DS--
				nyfC = 1 / (1 + (DS / Pi))
				SampleRate = initialR8 / DS
				display.SR = SampleRate
				info <- sf("sample rate increased to %.fHz", initialR8/DS)
			}*/
		}
		dac0 = dac // dac0 holds output value for use when restarting
		dac = 0
		n++
	}
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
// var p func(...interface{}) (int, error) = fmt.Println
func p(i ...any) { // mega hacky nullify output on re/load
	if ext {
		return
	}
	fmt.Println(i...)
}

var sf func(string, ...interface{}) string = fmt.Sprintf

// var pf func(string, ...interface{}) (int, error) = fmt.Printf
func pf(s string, i ...interface{}) { // mega hacky nullify output on re/load
	if ext {
		return
	}
	fmt.Printf(s, i...)
}

// msg sends a formatted string to info display
func msg(s string, i ...interface{}) {
	info <- fmt.Sprintf(s, i...)
	<-carryOn
}

// error handling
func e(rr error) bool {
	return rr != nil
}
