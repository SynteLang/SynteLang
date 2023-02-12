//go:build (freebsd || linux) && amd64

/*
	Syntə is an audio live coding environment

	The name is pronounced 'sinter', which means to create something by
	fusing many tiny elements together under intense heat

	The input syntax is in EBNF = operator " " [ [","] operand [","] " " ] .
	Where an operand can be a ( name = letter { letter | digit } ) | ( number = float  ["/" float ] [type] ) .
	A letter is defined as any UTF-8 character excluding + - . 0 1 2 3 4 5 6 7 8 9
	A float matches the floating point literal in the Go language specification.
	A type can be one of the following tokens: "hz", "s", "ms", "bpm", "db", "!" .
	A list of operators is given below.
	Lists of operations may be composed into functions with multiple arguments.
	The function syntax is = function [ " " operand [ "," operand ] [ "," operand ] ].

	Protect your hearing when listening to any audio on a system capable of more than 85dB SPL

	Motivation:
		Fun

	Features:
		Audio synthesis √
		Wav playback √
		Mouse control √
		Telemetry / code display √
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
// This is a prototype

// There are 6 goroutines (aside from main), they are:
// go SoundEngine(), blocks on write to soundcard input buffer, shutdown with ": exit"
// go infoDisplay(), timed slowly at > 20ms, explicitly returned from on exit
// go mouseRead(), blocks on mouse input, rechecks approx 20 samples later (at 48kHz)
// go func(), anonymous restart watchdog, waits on close of stop channel
// go func(), anonymous input from stdin, waits on user input
// go func(), anonymous polling of 'temp/', timed slowly at > 84ms

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
// these values are from '/sys/sys/soundcard.h' on freebsd13.0
// currently using `sudo sysctl dev.pcm.X.bitperfect=1`
// where X is the output found in `cat /dev/sndstat`
const (
	// set output only
	IOC_INOUT = 0xC0000000
	// set bit width to 32bit
	SNDCTL_DSP_SETFMT = IOC_INOUT | (0x04&((1<<13)-1))<<16 | 0x50<<8 | 0x05
	//	SNDCTL_DSP_SETFMT	= 0xC0045005
	// Format in Little Endian, see BYTE_ORDER below
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
	SNDCTL_DSP_SPEED       = 0xC0045002
	SAMPLE_RATE            = 48000 //hertz
	SNDCTL_DSP_SETFRAGMENT = IOC_INOUT | (0x04&((1<<13)-1))<<16 | 0x50<<8 | 0x0A
	BUFFER_SIZE            = 10 // not used

	WAV_TIME       = 4 //seconds
	WAV_LENGTH     = WAV_TIME * SAMPLE_RATE
	TAPE_LENGTH    = 1 //seconds
	MAX_WAVS       = 12
	EXPORTED_LIMIT = 12
	NOISE_FREQ     = 0.033 // 2½ times the geometric mean of audible spectrum (-8dB)
	RUN_TIME_OUT   = 100   // seconds
)

var convFactor = float64(MaxInt16) // checked below

// terminal colours, eg. sf("%stest%s test", yellow, reset)
const (
	reset   = "\x1b[0m"
	italic  = "\x1b[3m"
	red     = "\x1b[31m"
	yellow  = "\x1b[33m"
	magenta = "\x1b[35m"
	cyan    = "\x1b[36m"
)

const (
	yes = true
	not = false
)

type ops struct {
	Opd bool
	N   int
}

var operators = map[string]ops{ // would be nice if switch indexes could be generated from a common root
	// bool indicates if operand used
	"+":      ops{yes, 1},
	"out":    ops{yes, 2},
	".out":   ops{yes, 2}, // alias of out
	"out+":   ops{yes, 3},
	"in":     ops{yes, 4},
	"sine":   ops{not, 5},
	"mod":    ops{yes, 6},
	"gt":     ops{yes, 7},
	"lt":     ops{yes, 8},
	"mul":    ops{yes, 9},
	"*":      ops{yes, 9}, // alias of mul
	"x":      ops{yes, 9}, // alias of mul
	"abs":    ops{not, 10},
	"tanh":   ops{not, 11},
	"pow":    ops{yes, 12},
	"base":   ops{yes, 13},
	"clip":   ops{yes, 14},
	"noise":  ops{not, 15},
	"push":   ops{not, 16},
	"pop":    ops{not, 17},
	"tape":   ops{yes, 18},
	"--":     ops{yes, 19},
	"tap":    ops{yes, 20},
	"f2c":    ops{not, 21},
	"wav":    ops{yes, 22},
	"8bit":   ops{yes, 23},
	"index":  ops{not, 24}, // change to signal?
	"<sync":  ops{yes, 25},
	">sync":  ops{not, 26},
	".>sync": ops{not, 26},
	"jl0":    ops{yes, 27}, // jump if less than zero
	"level":  ops{yes, 28},
	".level": ops{yes, 28},
	"from":   ops{yes, 29},
	"sgn":    ops{not, 30},
	//	"deleted":      ops{yes, 31}, // specified below
	"/":      ops{yes, 32},
	"sub":    ops{yes, 33},
	"-":      ops{yes, 33}, // alias of sub
	"setmix": ops{yes, 34},
	"print":  ops{not, 35},
	"\\":     ops{yes, 36}, // "\"
	//	"degrade": ops{yes, 37}, // deprecated
	"pan":    ops{yes, 38},
	".pan":   ops{yes, 38},
	"all":    ops{not, 39},
	"fft":    ops{not, 40},
	"ifft":   ops{not, 41},
	"fftrnc": ops{yes, 42},
	"shfft":  ops{yes, 43},
	"ffrz":   ops{yes, 44},
	"gafft":  ops{yes, 45},

	// specials
	"]":       ops{not, 0},
	":":       ops{yes, 0},
	"fade":    ops{yes, 0},
	"del":     ops{yes, 0},
	"erase":   ops{yes, 0},
	"mute":    ops{yes, 0},
	"m":       ops{yes, 0}, // alias of mute
	"solo":    ops{yes, 0},
	"release": ops{yes, 0},
	"unmute":  ops{not, 0},
	".mute":   ops{yes, 0},
	".del":    ops{yes, 0},
	".solo":   ops{yes, 0},
	"//":      ops{yes, 0}, // comments
	"load":    ops{yes, 0},
	"ld":      ops{yes, 0}, // alias of load
	"[":       ops{yes, 0},
	"save":    ops{yes, 0},
	"ls":      ops{yes, 0},
	"ct":      ops{yes, 0}, // individual clip threshold
	"rld":     ops{yes, 0},
	"r":       ops{yes, 0}, // alias of rld
	"rpl":     ops{yes, 0},
	".rpl":    ops{yes, 0},
	"s":       ops{yes, 0}, // alias of solo
	"e":       ops{yes, 0}, // alias of erase
	"apd":     ops{yes, 0},
	"do":      ops{yes, 0},
	"d":       ops{yes, 0},
	"deleted": ops{not, 0}, // for internal use
	"extyes":  ops{not, 0}, // for internal use
	"extnot":  ops{not, 0}, // for internal use
}

// listing is a slice of { operator, operand; signal and operator numbers }
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
	Wavs    [][]float64 // sample
}

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
	infoff   = make(chan struct{}) // shut-off info display (and external input)
	mute     []float64             // should really be in transfer struct?
	level    []float64
	muteSkip bool
	ds       bool
	restart  bool
	reload   = -1
	ext      = not // loading external listing state

	daisyChains []int // list of exported signals to be daisy-chained
)

var ( // misc
	SampleRate float64 = SAMPLE_RATE
	BYTE_ORDER         = binary.LittleEndian // not allowed in constants
	TLlen      int
	fade       float64 = Pow(1e-4, 1/(100e-3*SAMPLE_RATE)) // 100ms
	protected          = yes
	release    float64 = Pow(8000, -1.0/(0.5*SAMPLE_RATE)) // 500ms
	DS                 = 1                                 // down-sample, integer as float type
	ct                 = 1.0                               // individual listing clip threshold
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
var mc = yes

type disp struct {
	On      bool
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
	Sync    bool
	Verbose bool
}

var display = disp{
	Mode:    "off",
	MouseX:  1,
	MouseY:  1,
	Protect: yes,
	Info:    "clear",
}

type wavs []struct {
	Name string
	Data []float64
}

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
		info <- "Bit format not available! Change requested format in file"
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
		p("\n--requested channels not accepted--")
		os.Exit(1)
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
	SampleRate = float64(data)
	if data != SAMPLE_RATE {
		info <- "\n--requested sample rate not accepted--"
		info <- sf("new sample rate: %vHz\n\n", SampleRate)
		time.Sleep(time.Second)
	}
	display.SR = SampleRate // fixed at initial rate

	go SoundEngine(w, format)
	go infoDisplay()
	go mouseRead()

	// process wavs
	wavSlice := decodeWavs()
	msg("")

	transfer.Wavs = make([][]float64, 0, len(wavSlice))
	wmap := map[string]bool{}
	wavNames := ""
	for _, w := range wavSlice {
		wavNames += w.Name + " "
		wmap[w.Name] = yes
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
	daisyChains = []int{2, 3, 9} // pitch,tempo,grid
	for i := 0; i < EXPORTED_LIMIT; i++ {
		reserved = append(reserved, sf("***%d", i+lenReserved)) // placeholder
	}
	lenExported := 0
	sg := map[string]float64{} // signals map
	var sig []float64          // local signals
	funcs := make(map[string]listing)
	// load functions from files and assign to funcs
	load(&funcs, "functions.json")
	for k, f := range funcs { // add funcs to operators map
		hasOpd := not
		for _, o := range f {
			if o.Opd == "@" { // set but don't reset
				hasOpd = yes
			}
		}
		operators[k] = ops{Opd: hasOpd}
	}
	var funcsave bool
	dispListings := []listing{}
	code := &dispListings
	priorMutes := []float64{}
	solo := -1
	unsolo := []float64{}
	lockLoad := make(chan struct{}, 1)
	tokens := make(chan string, 2<<12) // arbitrary capacity, will block input in extreme circumstances

	go func() { // watchdog, anonymous to use variables in scope
		// This function will restart the sound engine and reload listings using new sample rate
		for {
			<-stop // wait until stop channel closed
			if exit {
				return
			}
			stop = make(chan struct{})
			go SoundEngine(w, format)
			sg["wavR"] = 1.0 / (WAV_TIME * SampleRate) // hack to update wav rate
			for _, w := range wavSlice {
				sg["l."+w.Name] = float64(len(w.Data)-1) / (WAV_TIME * SampleRate)
			}
			TLlen = int(SampleRate * TAPE_LENGTH)
			lockLoad <- struct{}{}
			for len(tokens) > 0 { // empty incoming tokens
				<-tokens
			}
			for i := 0; i < len(transfer.Listing); i++ { // preload listings into tokens buffer
				f := sf(".temp/%d.syt", i)
				inputF, rr := os.Open(f)
				if e(rr) {
					msg("%v", rr)
					break
				}
				s := bufio.NewScanner(inputF)
				s.Split(bufio.ScanWords)
				if transfer.Listing[i][0].Op == "deleted" { // hacky, to avoid undeleting listings
					tokens <- "deleted"
					tokens <- "out"
					tokens <- "dac"
					continue
				}
				tokens <- "extyes"
				for s.Scan() {
					tokens <- s.Text()
				}
				tokens <- "extnot"
				inputF.Close()
			}
			transfer.Listing = nil
			transfer.Signals = nil
			dispListings = nil
			transmit <- yes
			<-accepted
			restart = yes
			<-lockLoad
			msg("%s>>> Sound Engine restarted%s", italic, reset)
		}
	}()

	go func() { // scan stdin from goroutine to allow external concurrent input
		s := bufio.NewScanner(os.Stdin)
		s.Split(bufio.ScanWords)
		for {
			s.Scan() // blocks on stdin
			t := s.Text()
			tokens <- t
		}
	}()

	go func() { // poll '.temp/%d.syt' modified time and reload if changed
		for {
			lockLoad <- struct{}{}
			l := len(transfer.Listing)
			stat := make([]time.Time, len(transfer.Listing))
			prevStat := make([]time.Time, len(transfer.Listing))
			for i := 0; i < len(transfer.Listing); i++ {
				f := sf(".temp/%d.syt", i)
				st, rr := os.Stat(f)
				if e(rr) {
					msg("reload: unable to locate %d.syt", i)
					return
				}
				stat[i] = st.ModTime()
				prevStat[i] = stat[i]
			}
			<-lockLoad
			for {
				time.Sleep(84721 * time.Microsecond) // coarse loop timing
				if !started {
					continue
				}
				lockLoad <- struct{}{}
				if len(transfer.Listing) != l {
					<-lockLoad
					break // to remake stat slices
				}
				l = len(transfer.Listing)
				for i := 0; i < len(transfer.Listing); i++ {
					f := sf(".temp/%d.syt", i)
					st, rr := os.Stat(f)
					if e(rr) {
						msg("unable to locate %d.syt", i)
						break
					}
					stat[i] = st.ModTime()
					if prevStat[i] != stat[i] {
						inputF, rr := os.Open(f)
						if e(rr) {
							msg("%v", rr)
							break
						}
						reload = i
						mute[reload] = 0
						time.Sleep(25 * time.Millisecond)
						s := bufio.NewScanner(inputF)
						s.Split(bufio.ScanWords)
						tokens <- "extyes"
						for s.Scan() {
							tokens <- s.Text()
						}
						tokens <- "extnot"
						msg("%slisting reloaded:%s %d", italic, reset, i)
						inputF.Close()
						prevStat[i] = stat[i]
						break
					}
					prevStat[i] = stat[i]
				}
				<-lockLoad
			}
		}
	}()

start:
	for { // main loop
		newListing := listing{}
		dispListing := listing{}
		sig = make([]float64, len(reserved), 30) // capacity is nominal
		// signals map with predefined constants, mutable
		sg = map[string]float64{ // reset sg map
			"ln2":      Ln2,
			"ln3":      Log(3),
			"ln5":      Log(5),
			"E":        E,   // e
			"Pi":       Pi,  // π
			"Phi":      Phi, // φ
			"invSR":    1 / SampleRate,
			"SR":       SampleRate,
			"Epsilon":  SmallestNonzeroFloat64, // ε, epsilon
			"wavR":     1.0 / (WAV_TIME * SampleRate),
			"semitone": Pow(2, 1.0/12),
			"Tau":      2 * Pi, // 2π
			"ln7":      Log(7),
			"^freq":    NOISE_FREQ, // default frequency for setmix, suitable for noise
			"null":     0,
		}
		for i, w := range wavSlice {
			sg[w.Name] = float64(i)
			sg["l."+w.Name] = float64(len(w.Data)-1) / (WAV_TIME * SampleRate)
		}
		out := map[string]struct{}{}
		for _, v := range reserved {
			switch v {
			case "tempo", "pitch", "grid":
				continue
			}
			out[v] = struct{}{}
		}
		fIn := not // yes = inside function definition
		st := 0    // func def start
		fun := 0   // don't worry the fun will increase!
		reload = -1
		do := 0

	input:
		for { // input loop
			if len(tokens) == 0 {
				// not strictly correct. restart only becomes true on an empty token channel,
				// no files saved to temp while restart is true,
				// tokens will be empty at completion of restart unless received from stdin within
				// time taken to compile and launch. This is not critical as restart only controls file save.
				restart = not
			}
			pf("%s\033[H\033[2J", reset) // this clears prior error messages!
			pf(">  %dbit %2gkHz %s\n", format, SampleRate/1000, channels)
			pf("%sSyntə%s running...\n", cyan, reset)
			pf("Always protect your ears above +85dB SPL\n\n")
			if len(wavNames) > 0 {
				pf(" %swavs:%s %s\n\n", italic, reset, wavNames)
			}
			pf("\n%s%d%s:", cyan, len(dispListings), reset)
			for i, o := range dispListing {
				switch dispListing[i].Op {
				case "in", "pop", "tap", "index", "[", "]", "from", "all":
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
			var num struct {
				Ber float64
				Is  bool
			}
			var op, opd string
			pf("\t  %s", yellow)
			op = <-tokens
			for e := yes; e; { // deal with all ext signals until token received
				switch op {
				case "extnot":
					ext = not
					op = <-tokens
				case "extyes":
					ext = yes
					op = <-tokens
				default:
					e = not
				}
			}
			pf("%s", reset)
			if (len(op) > 2 && byte(op[1]) == 91) || op == "_" { // hack to escape terminal characters
				continue
			}
			op = strings.TrimSuffix(op, ",") // to allow comma separation of tokens
			op2, in := operators[op]
			if !in {
				msg("%soperator or function doesn't exist:%s %s", italic, reset, op)
				for len(tokens) > 0 { // empty remainder of incoming tokens and abandon reload
					<-tokens
				}
				continue
			}
			_, f := funcs[op]
			var operands = []string{}
			if op2.Opd { // parse second token
				pf("%s", yellow)
				opd = <-tokens
				opd = strings.TrimSuffix(opd, ",") // to allow comma separation of tokens
				pf("%s", reset)
				if opd == "_" {
					continue
				}
				operands = strings.Split(opd, ",")
				if !f && len(operands) > 1 {
					msg("only functions can have multiple operands")
					continue
				}
				wav := wmap[opd] && op == "wav" // wavs can start with a number
				if strings.ContainsAny(opd[:1], "+-.0123456789") && !wav && !f {
					if num.Ber, num.Is = parseType(opd, op); !num.Is {
						continue input // parseType will report error
					}
				}
			}
			for do > 1 { // one already done
				tokens <- op
				if t2 := operators[op]; t2.Opd { // to avoid weird blank opds being sent
					tokens <- opd
				}
				do--
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
						M.at = yes
					case "@1":
						M.at1 = yes
					case "@2":
						M.at2 = yes
					}
					if _, r := sg[o.Opd]; r {
						continue
					}
					switch o.Opd[:1] {
					case "^", "@":
						continue
					}
					if strings.ContainsAny(o.Opd[:1], "+-.0123456789") {
						if _, ok := parseType(o.Opd, o.Op); ok {
							continue
						}
					}
					function[i].Opd += s // rename signal
					if o.Op == "out" {
						out[function[i].Opd] = struct{}{}
					}

				}
				m := 0
				switch M {
				case mm{not, not, not}:
					// nop
				case mm{yes, not, not}:
					m = 1
				case mm{yes, yes, not}:
					m = 2
				case mm{yes, yes, yes}:
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
						msg("%sthe function requires an operand%s", italic, reset)
						continue
					case m > 1:
						msg("%sthe function requires %d operands%s", italic, m, reset)
						continue
					}
				}
				for i, opd := range operands { // opd shadowed
					if operands[i] == "" {
						msg("empty argument %d", i+1)
						continue input
					}
					if strings.ContainsAny(opd[:1], "+-.0123456789") {
						if _, ok := parseType(opd, ""); !ok {
							continue input // parseType will report error
						}
					}
				}
				for i, o := range function {
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
				fun++
				dispListing = append(dispListing, listing{{Op: op, Opd: opd}}...) // only display name
				newListing = append(newListing, function...)
				if fIn {
					continue
				}
				switch o := newListing[len(newListing)-1]; o.Op {
				case "out":
					if o.Opd == "dac" {
						break input
					}
				case ".out", ".>sync", ".level", "//":
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
				case "exit", "q":
					p("\nexiting...")
					if display.Paused {
						<-pause
					}
					exit = yes
					if started {
						<-stop
					}
					p("Stopped")
					close(infoff)
					if funcsave {
						if !save(funcs, "functions.json") {
							msg("functions not saved!")
						}
					}
					time.Sleep(30 * time.Millisecond) // wait for infoDisplay to finish
					break start
				case "erase", "e":
					continue start
				case "foff":
					funcsave = not
					display.Mode = "off"
					continue
				case "fon":
					funcsave = yes
					display.Mode = "on"
					if !save(funcs, "functions.json") {
						msg("functions not saved!")
					}
					msg("%sfunctions saved%s", italic, reset)
					continue
				case "pause":
					if started && !display.Paused {
						for i := range mute { // save, and mute all
							priorMutes[i] = mute[i]
							mute[i] = 0
						}
						time.Sleep(50 * time.Millisecond)
						pause <- yes
						display.Paused = yes
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
					<-pause
					display.Paused = not
					continue
				case "unprotected":
					msg("unavailable")
					//protected = !protected
					continue
				case "clear", "c":
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
					continue
				case "mc": // mouse curve, exp or lin
					mc = !mc
					continue
				case "muff": // Mute Off
					muteSkip = !muteSkip
					s := "no"
					if muteSkip {
						s = "yes"
					}
					msg("%smute skip:%s %v", italic, reset, s)
					continue
				case "ds":
					ds = yes // not intended to be invoked while paused
					continue
				default:
					msg("%sunrecognised mode%s", italic, reset)
					continue
				}
			case "load", "ld", "rld", "r", "apd":
				switch op {
				case "rld", "r":
					reload, _ = strconv.Atoi(opd) // no checks
					opd = ".temp/" + opd
				case "apd":
					reload = -1
					opd = ".temp/" + opd
				}
				inputF, rr := os.Open(opd + ".syt")
				if e(rr) {
					msg("%v", rr)
					continue
				}
				s := bufio.NewScanner(inputF)
				s.Split(bufio.ScanWords)
				tokens <- "extyes"
				for s.Scan() {
					tokens <- s.Text()
				}
				tokens <- "extnot"
				inputF.Close()
				continue
			case "save":
				n, rr := strconv.Atoi(opd)
				if e(rr) || n < 0 || n > len(transfer.Listing)-1 {
					msg("%soperand not valid%s", italic, reset)
					continue
				}
				pf("\tName: ")
				f := <-tokens
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
				msg("%slisting%s %d %ssaved to%s %s", italic, reset, n, italic, reset, f)
				continue
			case "in":
				// nop
			case "out", "out+", ".out":
				_, in := out[opd]
				ExpSig := not
				for i := lenReserved; i < lenReserved+lenExported; i++ {
					if reserved[i] == opd {
						ExpSig = yes
					}
				}
				switch {
				case num.Is:
					msg("%soutput to number not permitted%s", italic, reset)
					continue
				case in && opd[:1] != "^" && opd != "dac" && !ExpSig && op != "out+":
					msg("%sduplicate output to signal, c'est interdit%s", italic, reset)
					continue
				case opd == "@":
					msg("%scan't send to @, represents function operand%s", italic, reset)
					continue
				}
				out[opd] = struct{}{}
			case "del", ".del", "d":
				n, rr := strconv.Atoi(opd)
				if e(rr) {
					msg("%soperand not an integer%s", italic, reset)
					continue
				}
				if n > len(transfer.Listing)-1 || n < 0 {
					msg("%sindex out of range%s", italic, reset)
					continue
				}
				mute[n] = 0 // wintermute
				display.Mute[n] = yes
				if display.Paused {
					for i := range mute { // restore mutes
						if i == n {
							continue
						}
						mute[i] = priorMutes[i]
					}
					<-pause
					display.Paused = not
				}
				time.Sleep(50 * time.Millisecond) // wait for envelope to complete
				transfer.Listing[n] = listing{{Op: "deleted", Opn: 31}}
				transfer.Signals[n][0] = 0 // silence listing
				dispListings[n] = listing{{Op: "deleted"}}
				transmit <- yes
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
					msg("%sno function definition%s", italic, reset)
					continue
				}
				hasOpd := not
				for _, o := range newListing[st+1:] {
					if o.Opd == "@" { // set but don't reset
						hasOpd = yes
					}
				}
				o := operators[newListing[st].Opd]
				o.Opd = hasOpd
				operators[newListing[st].Opd] = o
				funcs[newListing[st].Opd] = newListing[st+1:]
				msg("%sfunction assigned to:%s %s", italic, reset, newListing[st].Opd)
				fIn = not
				if funcsave {
					if !save(funcs, "functions.json") {
						msg("functions not saved!")
					}
				}
				continue start
			case "fade":
				if !num.Is {
					msg("%snot a valid number%s", italic, reset)
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
					msg("%spop before push%s", italic, reset)
					continue
				}
			case "tape":
				for _, o := range newListing {
					if o.Op == "tape" {
						msg("%sonly one tape per listing%s", italic, reset)
						continue input
					}
				}
			case "degrade":
				if len(transfer.Listing) == 0 {
					msg("%scan't use degrade in first listing%s", italic, reset)
					continue
				}
				msg("%sno register is safe...%s", italic, reset)
			case "erase", "e":
				n, rr := strconv.Atoi(opd)
				if e(rr) {
					msg("%soperand not an integer%s", italic, reset)
					continue
				}
				if n < 0 {
					continue
				}
				if n > len(dispListing) {
					msg("%snumber greater than length of necklace%s", italic, reset)
					continue
				}
				for i := 0; i < len(dispListing)-n; i++ { // recompile
					tokens <- dispListing[i].Op
					if len(dispListing[i].Opd) > 0 {
						tokens <- dispListing[i].Opd
					}
				}
				continue start
			case "wav":
				if !wmap[opd] && opd != "@" {
					msg("%sname isn't in wav list%s", italic, reset)
					continue
				}
			case "mute", ".mute", "m":
				i, rr := strconv.Atoi(opd)
				if e(rr) {
					msg("%soperand not an integer%s", italic, reset)
					continue
				}
				if i < 0 || i > len(transfer.Listing)-1 {
					msg("listing index does not exist")
					continue
				}
				if display.Paused && i < len(transfer.Listing) { // exclude present listing
					priorMutes[i] = 1 - priorMutes[i]
					display.Mute[i] = priorMutes[i] == 0 // convert binary to boolean
				} else {
					mute[i] = 1 - mute[i]
					unsolo[i] = mute[i]
					display.Mute[i] = mute[i] == 0 // convert binary to boolean
				}
				if op[:1] == "." && len(newListing) > 0 {
					dispListing = append(dispListing, listing{{Op: "mix"}}...)
					newListing = append(newListing, listing{{Op: "setmix", Opd: "^freq"}}...) // hacky
					op, opd = "out", "dac"
					break
				}
				continue
			case "level", ".level", "pan", ".pan":
				if len(transfer.Listing) == 0 {
					msg("no running listings")
					continue
				}
				i, rr := strconv.Atoi(opd)
				if e(rr) {
					msg("operand not an integer")
					continue
				}
				if i < 0 || i > len(transfer.Listing) { // includes current listing to be launched
					msg("index doesn't exist")
					continue
				}
			case "solo", ".solo", "s":
				if len(transfer.Listing) == 0 {
					msg("no running listings")
					continue
				}
				i, rr := strconv.Atoi(opd)
				if e(rr) {
					msg("operand not an integer")
					continue
				}
				if i < 0 || i > len(transfer.Listing) || (i == len(transfer.Listing) && op[:1] != ".") {
					msg("operand out of range")
					continue
				}
				if solo == i {
					for i := range mute { // i is shadowed
						mute[i] = unsolo[i]
						display.Mute[i] = mute[i] == 0
						solo = -1
					}
					mute[i] = 1
					display.Mute[i] = mute[i] == 0
				} else {
					for i := range mute { // i is shadowed
						unsolo[i] = mute[i]
						mute[i] = 0
						display.Mute[i] = yes
					}
					if i < len(transfer.Listing) { // only solo extant listings, new will be unmuted
						mute[i] = 1
						display.Mute[i] = not
					}
					solo = i
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
					display.Mute[i] = not
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
					msg("%swill overwrite existing function!%s", red, reset)
				} else if _, ok := operators[opd]; ok {
					msg("%sduplicate of extant operator, use another name%s", italic, reset)
					continue
				}
				st = len(newListing) // because current input hasn't been added yet
				fIn = yes
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
				extn := ""
				switch dir {
				case "./wavs":
					extn = ".wav"
				default:
					extn = ".syt"
				}
				ls := ""
				for _, file := range files {
					f := file.Name()
					if f[len(f)-4:] != extn {
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
				if n, ok := parseType(opd, op); ok {
					ct = n
					msg("%sclip threshold set to %.2g%s", italic, ct, reset)
				}
				continue
			case "rpl", ".rpl":
				n, rr := strconv.Atoi(opd)
				if e(rr) {
					msg("%soperand not an integer%s", italic, reset)
					continue
				}
				if n >= len(transfer.Listing) { // not really necessary
					msg("listing doesn't exist")
					continue
				}
				reload = n
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
			case "do":
				do, rr = strconv.Atoi(opd)
				if e(rr) { // returns do as zero
					msg("%soperand not an integer%s", italic, reset)
					continue
				}
				msg("%snext operation repeated%s %dx", italic, reset, do)
				continue
			case "extyes", "extnot": // remove this case after testing, should be unreachable //
				msg("external signal rejected from token queue")
				continue
			default:
				// nop
			}
			// end of switch

			// process exported signals
			alreadyIn := not
			for _, v := range reserved {
				if v == opd {
					alreadyIn = yes // signal already exported
				}
			}
			_, inSg := sg[opd]
			if !inSg && !alreadyIn && !num.Is && unicode.IsUpper([]rune(opd)[0]) {
				if lenExported > EXPORTED_LIMIT {
					msg("we've ran out of exported signals :(")
					continue
				}
				reserved[lenReserved+lenExported] = opd
				daisyChains = append(daisyChains, lenReserved+lenExported)
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
			case ".out", ".>sync", ".level", ".pan", "//":
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
			<-pause
			display.Paused = not
		}
		lockLoad <- struct{}{}
		//transfer to sound engine, or if reload, replace existing at that index
		if reload < 0 || reload > len(transfer.Listing)-1 {
			dispListings = append(dispListings, dispListing)
			transfer.Listing = append(transfer.Listing, newListing)
			transfer.Signals = append(transfer.Signals, sig)
			if len(mute) < len(transfer.Listing) { // not if restarting
				mute = append(mute, 1)
				priorMutes = append(priorMutes, 1)
				unsolo = append(unsolo, 1)
				display.Mute = append(display.Mute, not)
				level = append(level, 1)
			}
			transmit <- yes
			<-accepted
			if !restart { // hacky conditional
				// save listing as <n>.syt for the reload
				f := sf(".temp/%d.syt", len(transfer.Listing)-1)
				content := ""
				for _, d := range dispListing {
					content += d.Op + " " + d.Opd + "\n"
				}
				if rr := os.WriteFile(f, []byte(content), 0666); e(rr) {
					msg("%v", rr)
				}
			}
		} else { // reloaded listing isn't saved to '.temp/'
			mute[reload] = 1
			dispListings[reload] = dispListing
			transfer.Listing[reload] = newListing
			transfer.Signals[reload] = sig
			priorMutes[reload] = 1
			unsolo[reload] = 1
			display.Mute[reload] = not
			level[reload] = 1
			transmit <- yes
			<-accepted
		}
		<-lockLoad
		if !started {
			display.On = yes
			started = yes
		}

		timestamp := time.Now().Format("02-01-06.15:04")
		f := "recordings/listing." + timestamp + ".json" // shadowed
		if !save(newListing, f) {                        // save as plain text instead?
			msg("%slisting not recorded, check 'recordings/' directory exists%s", italic, reset)
		}
		if !save(*code, "displaylisting.json") {
			msg("%slisting display not updated, check file %s'displaylisting.json'%s exists%s",
				italic, reset, italic, reset)
		}
	}
}

// parseType() evaluates conversion of types
func parseType(expr, op string) (n float64, b bool) {
	switch op { // ignore for following commands
	case "mute", ".mute", "del", ".del", "d", "solo", ".solo", "load", "save", "m", "rld", "r", "rpl", "s", "ld", "ls", "[", "do", "apd": // this is a bit messy, we don't care what value is - commands handled in input loop
		return 0, true
	default:
		// process expression below
	}
	switch {
	case len(expr) > 1 && expr[len(expr)-1:] == "!":
		if n, b = evaluateExpr(expr[:len(expr)-1]); !b {
			return 0, false
		}
	case len(expr) > 2 && expr[len(expr)-2:] == "ms":
		if n, b = evaluateExpr(expr[:len(expr)-2]); !b {
			msg("erm s")
			return 0, false
		}
		n = 1 / ((n / 1000) * SampleRate)
		if !nyquist(n, expr) {
			return 0, false
		}
	case len(expr) > 1 && expr[len(expr)-1:] == "s":
		if n, b = evaluateExpr(expr[:len(expr)-1]); !b {
			return 0, false
		}
		n = 1 / (n * SampleRate)
		if !nyquist(n, expr) {
			return 0, false
		}
	case len(expr) > 2 && expr[len(expr)-2:] == "hz":
		if n, b = evaluateExpr(expr[:len(expr)-2]); !b {
			return 0, false
		}
		n /= SampleRate
		if !nyquist(n, expr) {
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
		switch op {
		case "from", "level", ".level", "count", "/":
			// allow high values for these operators
		default:
			if Abs(n) > 20 {
				msg("exceeds sensible values, use a type")
				return 0, false
			}
		}
	}
	if IsInf(n, 0) || n != n { // ideally check for zero in specific cases
		msg("number not useful")
		return 0, false
	}
	return n, true
}
func nyquist(n float64, e string) bool {
	ny := 2e4 / SampleRate
	if bounds(n, ny) {
		msg("'%s' is an %sinaudible frequency >20kHz%s", e, italic, reset)
		if bounds(n, 1) {
			msg(" and frequency out of range, not accepted")
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
		msg("%s third operand in expression ignored%s", italic, reset)
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
// Differing sample rates are not currently pitch converted. Header is assumed to be 44 bytes.
func decodeWavs() wavs {
	var filelist []string
	var w wavs
	var wav struct {
		Name string
		Data []float64
	}
	files, rr := os.ReadDir("./wavs")
	if e(rr) {
		msg("%sno wavs:%s %v", italic, reset, rr)
		return nil
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
		return nil
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
		r.Close()
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
		rb := bytes.NewReader(data[44:])
		switch bits {
		case 16:
			wav.Data = decode(rb, file, make([]int16, to), float64(MaxInt16), to, channels)
		case 24:
			d := make([]byte, 0, len(data)*2)
			for i := 44; i < len(data)-3; i += 3 { // byte stuffing
				word := append(data[i:i+3], byte(0))
				d = append(d, word...)
			}
			rb = bytes.NewReader(d)
			wav.Data = decode(rb, file, make([]int32, to), float64(MaxInt32), to, channels)
		case 32:
			wav.Data = decode(rb, file, make([]int32, to), float64(MaxInt32), to, channels)
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
		msg("%s\t%2dbit  %3gkHz  %s  %.3gs", file, bits, float64(SR)/1000, c, t)
	}
	if len(w) == 0 {
		return nil
	}
	return w
}

func decode[S int16 | int32](rb *bytes.Reader, file string, samples []S, factor float64, to, channels int) []float64 {
	rr := binary.Read(rb, binary.LittleEndian, &samples)
	if e(rr) && rr != io.ErrUnexpectedEOF {
		msg("error decoding: %s %s", file, rr)
		return nil
	}
	// convert to syntə format
	wav := make([]float64, 0, to)
	for i := 0; i < to-channels+1; i += channels {
		s := 0.0
		if channels == 2 {
			s = (float64(samples[i]) + float64(samples[i+1])) / (2 * factor)
		} else {
			s = float64(samples[i]) / factor
		}
		wav = append(wav, s)
	}
	return wav
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
		msg("mouse unavailable: %v", rr)
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
			msg("error reading mouse data: %v", rr)
			return
		}
		if mc {
			mouse.X = Pow(10, mx/10)
			mouse.Y = Pow(10, my/10)
		} else {
			mouse.X = mx / 5
			mouse.Y = my / 5
		}
		display.MouseX = mouse.X
		display.MouseY = mouse.Y
		time.Sleep(416 * time.Microsecond) // coarse loop timing
	}
}

func infoDisplay() {
	file := "infodisplay.json"
	n := 1
	s := 1
	for {
		select {
		case display.Info = <-info:
		case carryOn <- yes: // semaphore: received, continue
		case <-infoff:
			display.Info = sf("%sSyntə closed%s", italic, reset)
			display.On = not // stops timer in info display
			save(display, file)
			return
		default: // passthrough
		}
		if !save(display, file) {
			pf("%sinfo display not updated, check file %s%s%s exists%s\n",
				italic, reset, file, italic, reset)
			time.Sleep(2 * time.Second)
		}
		time.Sleep(20 * time.Millisecond) // coarse loop timing
		n++
		if n > 10 { // clip timeout
			display.Clip = not
			n = 0
		}
		s++
		if s > 20 { // sync timeout
			display.Sync = not
			s = 0
		}
	}
}

// The Sound Engine does the bare minimum to generate audio
// Some work has been done on profiling, beyond design choices such as using slices instead of maps
// Using floats is probably somewhat profligate, later on this may be converted to int type which would provide ample dynamic range
// It is also freewheeling, it won't block on the action of any other goroutine, only on IO, namely writing to soundcard
// The latency and jitter of the audio output is entirely dependent on the soundcard and its OS driver,
// except where the calculations don't complete in time under heavy load and the soundcard driver buffer underruns. Frequency accuracy is determined by the soundcard clock and precision of float64 type
// If the loop time exceeds the sample rate over number of samples given by RATE the Sound Engine will panic
// The data transfer structures need a good clean up
func SoundEngine(w *bufio.Writer, bits int) {
	defer close(stop)
	defer w.Flush()
	output := func(w *bufio.Writer, f float64) {
		binary.Write(w, BYTE_ORDER, int16(f))
	}
	switch bits {
	case 8:
		output = func(w *bufio.Writer, f float64) {
			binary.Write(w, BYTE_ORDER, int8(f))
		}
	case 16:
		// already assigned
	case 32:
		output = func(w *bufio.Writer, f float64) {
			binary.Write(w, BYTE_ORDER, int32(f))
		}
	default:
		msg("unable to write to soundcard!")
		return
	}

	const (
		Tau        = 2 * Pi
		RATE       = 2 << 11
		overload   = "Sound Engine overloaded"
		recovering = "Sound Engine recovering"
		rateLimit  = "At sample rate limit"
	)

	var (
		no     noise   = noise(time.Now().UnixNano())
		l, h   float64 = 1, 2 // limiter, hold
		dac    float64        // output
		dac0   float64        // formatted output
		env    float64 = 1    // for exit envelope
		peak   float64        // vu meter
		dither float64
		n      int // loop counter

		rate     time.Duration = time.Duration(7292) // loop timer, initialised to approximate resting rate
		lastTime time.Time     = time.Now()
		rates    [RATE]time.Duration
		t        time.Duration
		s        float64 = 1 // sync=0

		mx, my float64 = 1, 1 // mouse smooth intermediates
		hpf, x float64        // DC-blocking high pass filter
		hpf2560, x2560,
		hpf160, x160,
		det float64 // limiter detection
		lpf50, lpf510,
		deemph float64 // de-emphasis
		smR8        = 40.0 / SampleRate
		hroom       = (convFactor - 1.0) / convFactor // headroom for positive dither
		c           float64                           // mix factor
		pd          int
		nyfL, nyfR  float64                                      // nyquist filtering
		nyfC        float64 = 1 / (1 + 1/(2*Pi*2e4/SAMPLE_RATE)) // coefficient
		L, R, sides float64
	)
	no *= 77777777777 // force overflow
	defer func() {    // fail gracefully
		switch p := recover(); p { // p is shadowed
		case nil:
			return // exit normally
		case overload, recovering:
			msg("%v", p)
			msg("%ssample rate is now:%s %3gkHz", italic, reset, SampleRate/1000)
		default:
			msg("%v", p) // report runtime error to infoDisplay
			/*var buf [4096]byte
			n := runtime.Stack(buf[:], false)
			msg("%s", buf[:n]) // print stack trace to infoDisplay*/
			if reload == -1 {
				reload = len(transfer.Listing) - 1
			}
			for reload >= 0 && transfer.Listing[reload][0].Op == "deleted" {
				reload--
			}
			if reload < 0 {
				break
			}
			transfer.Listing[reload] = listing{{Op: "deleted", Opn: 31}} // delete listing
			transfer.Signals[reload][0] = 0                              // silence listing
			msg("previous listing deleted: %d", reload)
		}
		fade := Pow(1e-4, 1/(SampleRate*100e-3)) // approx -80dB in 100ms
		for i := 4800; i >= 0; i-- {
			dac0 *= fade
			output(w, dac0) // left
			output(w, dac0) // right
		}
	}()
	stack := make([]float64, 0, 4)

	<-transmit // load first listing(s) and start SoundEngine, multiple listings loaded on recover from panic
	listings := make([]listing, len(transfer.Listing), len(transfer.Listing)+24)
	sigs := make([][]float64, len(transfer.Signals), len(transfer.Signals)+23)
	stacks := make([][]float64, len(transfer.Listing), len(transfer.Listing)+21)
	for i := range stacks {
		stacks[i] = stack
	}
	wavs := make([][]float64, len(transfer.Wavs), MAX_WAVS)
	tapes := make([][]float64, 0, 26)
	copy(listings, transfer.Listing)
	copy(sigs, transfer.Signals)
	copy(wavs, transfer.Wavs)
	tapes = make([][]float64, len(transfer.Listing))
	for i := range tapes { // i is shadowed
		tapes[i] = make([]float64, TLlen)
	}
	tf := make([]float64, len(transfer.Listing)+31)
	th := make([]float64, len(transfer.Listing)+31)
	tx := make([]float64, len(transfer.Listing)+31)
	pan := make([]float64, len(transfer.Listing)+31)
	accepted <- yes
	syncInhibit := make([]bool, len(transfer.Listing), len(transfer.Listing)+27) // inhibitions
	peakfreq := make([]float64, len(transfer.Listing), len(transfer.Listing)+28) // peak frequency for setlevel
	for i := range peakfreq {
		peakfreq[i] = 20 / SampleRate
	}
	m := make([]float64, len(transfer.Listing), len(transfer.Listing)+29)  // filter intermediate for mute
	lv := make([]float64, len(transfer.Listing), len(transfer.Listing)+30) // filter intermediate for level
	fftArray := make([][N]float64, len(transfer.Listing))
	ifftArray := make([][N]float64, len(transfer.Listing))
	ifft2 := make([][N]float64, len(transfer.Listing))
	z := make([][N]complex128, len(transfer.Listing))
	ffrz := make([]bool, len(transfer.Listing))

	lastTime = time.Now()
	for {
		select {
		case <-pause:
			pause <- not          // blocks until `: play`
			lastTime = time.Now() // restart loop timer
		case <-transmit:
			listings = make([]listing, len(transfer.Listing))
			copy(listings, transfer.Listing)
			sigs = make([][]float64, len(transfer.Signals))
			copy(sigs, transfer.Signals)
			accepted <- yes
			ffrz = make([]bool, len(transfer.Listing))
			if len(transfer.Listing) > len(m) { // preserve extant tapes and mutes, etc
				tapes = append(tapes, make([]float64, TLlen))
				m = append(m, 0) // m ramps to mute value on launch
				lv = append(lv, 1)
				tf = append(tf, 0)
				th = append(th, 0)
				tx = append(tx, 0)
				pan = append(pan, 0)
				syncInhibit = append(syncInhibit, not)
				peakfreq = append(peakfreq, 20/SampleRate)
				stacks = append(stacks, stack)
				fftArray = append(fftArray, [N]float64{})
				ifftArray = append(ifftArray, [N]float64{})
				ifft2 = append(ifft2, [N]float64{})
				z = append(z, [N]complex128{})
			} else if reload > -1 {
				m[reload] = 0 // m ramps to mute value on reload
				syncInhibit[reload] = not
			}
		default:
			// play
		}

		if n%15127 == 0 { // arbitrary interval all-zeros protection for noise lfsr
			no ^= 1 << 27
		}

		mo := mouse
		mx = (mx*764 + mo.X) / 765 // lpf @ ~10Hz
		my = (my*764 + mo.Y) / 765

		for i, list := range listings {
			for _, ii := range daisyChains {
				sigs[i][ii] = sigs[(i+len(sigs)-1)%len(sigs)][ii]
			}
			// skip muted/deleted listing
			if (muteSkip && mute[i] == 0 && m[i] < 1e-6) || list[0].Opn == 31 {
				continue
			}
			// mouse values
			sigs[i][4] = mx
			sigs[i][5] = my
			sigs[i][6] = mo.Left
			sigs[i][7] = mo.Right
			sigs[i][8] = mo.Middle
			r := 0.0
			op := 0
			for _, o := range list {
				switch o.Opn {
				case 0:
					// nop
				case 1: // "+"
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
					if Signbit(sigs[i][o.N]) && r == 0 {
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
					//if r > 0.9999 { panic("test") } // for testing
				case 16: // "push"
					stacks[i] = append(stacks[i], r)
				case 17: // "pop"
					r = stacks[i][len(stacks[i])-1]
					stacks[i] = stacks[i][:len(stacks[i])-1]
				case 18: // "tape"
					r = Max(-1, Min(1, r)) // hard clip for cleaner reverbs
					th[i] = (th[i] + r - tx[i]) * 0.9994
					tx[i] = r
					tapes[i][n%TLlen] = th[i]
					t := Min(1/sigs[i][o.N], SampleRate*TAPE_LENGTH)
					r = tapes[i][(n+TLlen-int(t)+1)%TLlen]
					tf[i] = (tf[i] + r) / 2 // roll off the top end @ 7640Hz
					r = tf[i]
				case 19:
					r = sigs[i][o.N] - r
				case 20: // "tap"
					t := Min(1/sigs[i][o.N], SampleRate*TAPE_LENGTH)
					r = tapes[i][(n+TLlen-int(t)+1)%TLlen]
				case 21: // "f2c"
					r = Abs(r)
					//r = 1 / (1 + 1/(Tau*r))
					r *= Tau
					r /= (r + 1)
				case 22: // "wav"
					r += 1 // to allow negative input to reverse playback
					r = Abs(r)
					r *= float64(len(wavs[int(sigs[i][o.N])]))
					r = wavs[int(sigs[i][o.N])][int(r)%len(wavs[int(sigs[i][o.N])])]
				case 23: // "8bit"
					r = float64(int8(r*sigs[i][o.N])) / sigs[i][o.N]
				case 24: // "index"
					r = float64(i)
				case 25: // "<sync"
					r *= s
					r += (1 - s) * sigs[i][o.N] // phase offset
				case 26: // ">sync", ".>sync"
					switch { // syncInhibit is a slice to make multiple >sync operations independent
					case r <= 0 && s == 1 && !syncInhibit[i]:
						s = 0
						syncInhibit[i] = yes
					case s == 0 && syncInhibit[i]: // single sample pulse
						s = 1
						display.Sync = yes
					case r > 0:
						syncInhibit[i] = not
					}
				case 27: // "jl0"
					if r <= 0 {
						op += int(sigs[i][o.N])
					}
					if op > len(list)-2 {
						op = len(list) - 2
					}
				case 28: // "level", ".level"
					level[int(sigs[i][o.N])] = r
				case 29: // "from"
					r = sigs[int(sigs[i][o.N])%len(sigs)][0]
				case 30: // "sgn"
					r = 1 - float64(Float64bits(r)>>62)
				case 32: // "/"
					if sigs[i][o.N] == 0 {
						sigs[i][o.N] = Copysign(1e-308, sigs[i][o.N])
					}
					//r /= Max(0.1, Min(-0.1, sigs[i][o.N])) // alternative
					r /= sigs[i][o.N]
				case 33: // "sub"
					r -= sigs[i][o.N]
				case 34: // "setmix"
					a := Abs(sigs[i][o.N]) + 1e-6
					d := a/peakfreq[i] - 1
					d = Max(-1, Min(1, d))
					peakfreq[i] += a * (d * smR8)
					if Abs(d) < 0.01 {
						peakfreq[i] = a
					}
					r *= Min(1, 80/(peakfreq[i]*SampleRate+20)) // ignoring density
					//r *= Min(1, Sqrt(80/(peakfreq[i]*SampleRate+20)))
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
				case 38: // "pan", ".pan"
					pan[int(sigs[i][o.N])] = Max(-1, Min(1, r))
				case 39: // "all"
					c := -3.0                   // to avoid being mixed twice
					for ii := 0; ii < i; ii++ { // only read from prior listings
						if sigs[ii][0] == 0 { // avoid silent listings, hacky
							continue
						}
						r += sigs[ii][0]
						c++
					}
					c = Max(c, 1)
					r /= c
				case 40: // "fft"
					fftArray[i][n%N] = r
					if n%N2 == 0 && n >= N && !ffrz[i] {
						nn := n % N
						var zz [N]complex128
						for n := range fftArray[i] { // n is shadowed
							ww := float64(n) / float64(N-1)
							w := Pow(1-ww*ww, 1.25) // modified Welch
							zz[n] = complex(w*fftArray[i][(n+nn)%N], 0)
						}
						z[i] = fft(zz, 1)
					}
				case 41: // "ifft"
					if n%N == 1 && n >= N {
						zz := fft(z[i], -1)
						for n, z := range zz { // n, z are shadowed
							w := (1 - Cos(Tau*float64(n)/float64(N-1))) / 2 // Hann
							ifftArray[i][n] = w * real(z) / N

						}
					}
					if n%N == N2+1 && n >= N {
						zz := fft(z[i], -1)
						for n, z := range zz { // n, z are shadowed
							w := (1 - Cos(Tau*float64(n)/float64(N-1))) / 2 // Hann
							ifft2[i][n] = w * real(z) / N

						}
					}
					if !ffrz[i] {
						r = ifftArray[i][n%N] + ifft2[i][(n+N2)%N]
					} else {
						r = (ifftArray[i][n%N] + ifftArray[i][(n+N2)%N])
					}
				case 42: // "fftrnc"
					if n%N2 == 0 && n >= N && !ffrz[i] {
						switch {
						case sigs[i][o.N] > 0:
							l := int(N * sigs[i][o.N])
							for n := l; n < N; n++ {
								z[i][n] = complex(0, 0)
							}
						case sigs[i][o.N] < 0:
							l := -int(N * sigs[i][o.N])
							for n := range z[i] {
								if n > l || n < N-l {
									z[i][n] = complex(0, 0)
								}
							}
						}
					}
				case 43: // "shfft"
					s := sigs[i][o.N]
					if n%N2 == 0 && n >= N && !ffrz[i] {
						l := int(Mod(s, 1) * N)
						for n := range z[i] {
							nn := (N + n + l) % N
							z[i][n] = z[i][nn]
						}
					}
				case 44: // "ffrz"
					ffrz[i] = sigs[i][o.N] == 0
				case 45: // "gafft"
					if n%N2 == 0 && n >= N && !ffrz[i] {
						s := sigs[i][o.N] * 50
						gt := yes
						if s < 0 {
							s = -s
							gt = not
						}
						for n, zz := range z[i] {
							if gt && Abs(real(zz)) < s {
								z[i][n] = 0
							} else if !gt && Abs(real(zz)) > s {
								z[i][n] = 0
							}
						}
					}
				default:
					// nop, r = r
				}
				op++
			}
			m[i] = (m[i]*764 + mute[i]) / 765 // anti-click filter @ ~10hz
			lv[i] = (lv[i]*7 + level[i]) / 8  // @ 1091hz
			c += m[i]                         // add mute to mix factor
			if sigs[i][0] == 0 {
				continue
			}
			if sigs[i][0] != sigs[i][0] { // test for NaN
				sigs[i][0] = 0
				panic(sf("listing: %d - NaN", i))
			}
			if IsInf(sigs[i][0], 0) { // infinity to '93
				sigs[i][0] = 0
				panic(sf("listing: %d - overflow", i))
			}
			sigs[i][0] *= lv[i]
			sides += pan[i] * (sigs[i][0] / 2) * m[i] * lv[i]
			sigs[i][0] *= 1 - Abs(pan[i]/2)
			mm := sigs[i][0] * m[i]
			if mm > ct && protected { // soft clip
				mm = ct + Tanh(mm-ct)
				display.Clip = yes
			} else if mm < -ct && protected {
				mm = Tanh(mm+ct) - ct
				display.Clip = yes
			}
			dac += mm
		}
		c += 16 / (c*c + 4)
		dac /= c
		sides /= c
		c = 0
		hpf = (hpf + dac - x) * 0.9994 // hpf ≈ 4.6Hz
		x = dac
		dac = hpf
		if protected { // limiter
			// apply premphasis to detection
			hpf2560 = (hpf2560 + dac - x2560) * 0.749
			x2560 = dac
			hpf160 = (hpf160 + dac - x160) * 0.97948
			x160 = dac
			{
				d := 4 * dac / (1 + Abs(dac*4))
				lpf50 = (lpf50*152 + d) / 153
				lpf510 = (lpf510*152 + lpf50) / 153
				deemph = lpf510 / 1.5
			}
			det = Abs(32*hpf2560 + 5.657*hpf160 + dac)
			if det > l {
				l = det // MC
				h = release
			}
			dac /= l
			sides /= l
			dac += deemph
			h /= release
			l = (l-1)*(1/(h+1/(1-release))+release) + 1 // snubbed decay curve
			display.GR = l > 1+3e-4
		}
		if exit {
			dac *= env // fade out
			sides *= env
			env *= fade
			if env < 1e-4 {
				save([]listing{listing{{Op: advisory}}}, "displaylisting.json")
				break
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
			display.Clip = yes
		} else if dac < -1 {
			dac = -1
			display.Clip = yes
		}
		if abs := Abs(dac); abs > peak { // peak detect
			peak = abs
		}
		peak -= 8e-5 // meter ballistics
		if peak < 0 {
			peak = 0
		}
		sides = Max(-0.5, Min(0.5, sides))
		display.Vu = peak
		L = (dac + sides) * convFactor
		R = (dac - sides) * convFactor
		t = time.Since(lastTime)
		for i := 0; i < DS; i++ { // write sample(s) to soundcard
			nyfL = nyfL + nyfC*(L-nyfL)
			nyfR = nyfR + nyfC*(R-nyfR)
			output(w, nyfL) // left
			output(w, nyfR) // right, remove if stereo not available
		}
		lastTime = time.Now()
		rate += t
		rates[n%RATE] = t // rolling average buffer
		rate -= rates[(n+1)%RATE]
		if n%RATE == 0 && n > RATE<<2 { // don't restart in first four time frames
			display.Load = rate / RATE
			if (float64(display.Load) > 1e9/SampleRate || ds) && SampleRate > 22050 {
				ds = not
				DS <<= 1
				nyfC = 1 / (1 + (float64(DS*DS) / Pi)) // coefficient is non-linear
				SampleRate /= 2
				display.SR = SampleRate
				fade = Pow(1e-4, 1/(100e-3*SampleRate))    // 100ms
				release = Pow(8000, -1.0/(0.5*SampleRate)) // 500ms
				panic(overload)
			} else if float64(display.Load) > 1e9/SampleRate {
				panic(rateLimit)
			} else if DS > 1 && float64(display.Load) < 33e7/SampleRate && n > 100000 { // holdoff for ~4secs x DS
				DS >>= 1
				nyfC = 1 / (1 + (float64(DS*DS) / Pi)) // coefficient is non-linear
				SampleRate *= 2
				display.SR = SampleRate
				fade = Pow(1e-4, 1/(100e-3*SampleRate))    // 100ms
				release = Pow(8000, -1.0/(0.5*SampleRate)) // 500ms
				panic(recovering)
			}
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

const (
	N  = 2 << 12 // fft window size
	N2 = N >> 1
)

func fft(y [N]complex128, s float64) [N]complex128 {
	var x [N]complex128
	for r, l := N2, 1; r > 0; r /= 2 {
		y, x = x, y
		ωi, ωr := Sincos(-s * Pi / float64(l))
		//ω := complex(Cos(-s * Pi / float64(l)), Sin(-s * Pi / float64(l)))
		for j, ωj := 0, complex(1, 0); j < l; j++ {
			jr := j * r * 2
			for k, m := jr, jr/2; k < jr+r; k++ {
				t := ωj * x[k+r]
				y[m] = x[k] + t
				y[m+N2] = x[k] - t
				m++
			}
			ωj *= complex(ωr, ωi)
			//ωj *= ω
		}
		l *= 2
	}
	return y
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

// shorthand
func p(i ...any) {
	fmt.Println(i...)
}
func pf(s string, i ...interface{}) { // mega hacky nullify output on re/load
	if ext {
		return
	}
	fmt.Printf(s, i...)
}

var sf func(string, ...interface{}) string = fmt.Sprintf

// msg sends a formatted string to info display
func msg(s string, i ...interface{}) {
	info <- fmt.Sprintf(s, i...)
	<-carryOn
}

// error handling
func e(rr error) bool {
	return rr != nil
}
