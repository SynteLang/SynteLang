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
		Mouse control √ (only available on bsd and linux)
		Telemetry / code display √
		Anything can be connected to anything else within a listing √
		Feedback permitted (see above) √
		Groups of operators can be defined, named and instantiated as new functions √
		Predefined functions and operators √
		Flexible synchronisation of co-running listings √

	Author: Dan Arves
	Available for workshops, talks and performances: synte@proton.me

	See licence file in this repository
	© 2022
*/

// Go code in this file not suitable for reference or didactic purposes
// This is a prototype

// There are 7 goroutines (aside from main), they are:
// go SoundEngine(), blocks on write to soundcard input buffer, shutdown with ": exit"
// go infoDisplay(), timed slowly at > 20ms, explicitly returned from on exit
// go mouseRead(), blocks on mouse input, rechecks approx 20 samples later (at 48kHz)
// go func(), anonymous restart watchdog, waits on close of stop channel
// go readInput(), scan stdin from goroutine to allow external concurrent input, blocks on stdin
// go reloadListing(), poll '.temp/*.syt' modified time and reload if changed, timed slowly at > 84ms
// go sc.output(), handles writing to soundcard within SoundEngine(), blocks on write to soundcard if no overload

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/cmplx"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	CHANNELS      = 2 // will halve pitches/frequencies/tempos if mono!
	SAMPLE_RATE   = 48000 //hertz

	WAV_TIME      = 4 //seconds
	TAPE_LENGTH   = 1 //seconds
	MAX_WAVS      = 12
	lenReserved   = 11
	maxExports    = 12
	DEFAULT_FREQ  = 0.0625 // 3kHz @ 48kHz Sample rate
	MIN_FADE      = 125e-3 // 125ms
	MAX_FADE      = 120   // seconds
	defaultRelease = 0.25  // seconds
	MIN_RELEASE   = 25e-3 // 25ms
	MAX_RELEASE   = 1     // seconds
	twoInvMaxUint = 2.0 / math.MaxUint64
	alpLen        = 2400
	baseGain      = 0.27
	writeBufferLen = 2 << 12
	OutputFilter  = 12000 // Hz
	OutputSmooth  = 15 // Hz
	LoadThresh	  = 85 // percent
)

const (
	infoFile     = "infodisplay.json"
	logFile      = "info.txt"
	listingsFile = "displaylisting.json"
	funcsFile    = "functions.json"
)

var SampleRate float64 = SAMPLE_RATE // should be 'de-globalised'

const ( // terminal colours, eg. sf("%stest%s test", yellow, reset)
	reset   = "\x1b[0m"
	italic  = "\x1b[3m"
	bold    = "\x1b[1m"
	red     = "\x1b[31m"
	green   = "\x1b[32m"
	blue    = "\x1b[34m"
	yellow  = "\x1b[33m"
	magenta = "\x1b[35m"
	cyan    = "\x1b[36m"
)

const ( // aliases
	yes = true
	not = false
)

var assigned = struct{}{}

type operation struct {
	Op  string // operator
	Opd string // operand
	N   int    `json:"-"` // signal number
	Opn int    `json:"-"` // operation switch index
	P	bool   `json:"-"` // persist = true
	i   int // index of persisted signal
	num	bool
	ber	float64
}
type listing []operation

type createListing struct {
	newListing  listing
	dispListing listing
	newSignals  []float64
	signals     map[string]int
}

type number struct {
	Ber float64
	Is  bool
}

type newOperation struct {
	operator, operand string
	operands          []string
	num               number
	isFunction        bool
}

type listingState struct {
	createListing
	reload int
	out map[string]struct{} // to check for multiple outs to same signal name
	clr clear
	newOperation
	fIn bool // yes = inside function definition
	st, // func def start
	funCount,
	do, to int
	muteGroup []int // new mute group
}

type fn struct {
	Comment string
	Body    listing
}

type systemState struct {
	dispListings []listing // these are relied on to track len of SE listings, checked after transfer
	verbose         []listing // for tools/listings.go
	wmap            map[string]bool
	wavNames        string // for display purposes
	funcs           map[string]fn
	solo            int // index of most recent solo
	unsolo          muteSlice
	hasOperand      map[string]bool
	daisyChains     []int
	tapeLen         int
	lenExported     int
	exportedSignals map[string]int
	listingState
	soundcard
}

type processor func(systemState) (systemState, int)

type operatorCheck struct {
	Opd     bool // indicates if has operand
	N       int  // index for sound engine switch
	process processor
}

var operators = map[string]operatorCheck{ // would be nice if switch indexes could be generated from a common root
	// this map is effectively a constant and not mutated
	//name  operand N  process           comment
	"+":      {yes, 1, noCheck},       // add
	"out":    {yes, 2, checkOut},      // send to named signal
	".out":   {yes, 2, checkOut},      // alias of out
	"out+":   {yes, 3, checkOut},      // add to named signal
	"in":     {yes, 4, checkIn},       // input numerical value or receive from named signal
	"sine":   {not, 5, noCheck},       // shape linear input to sine
	"mod":    {yes, 6, noCheck},       // output = input MOD operand
	"gt":     {yes, 7, noCheck},       // greater than
	"lt":     {yes, 8, noCheck},       // less than
	"mul":    {yes, 9, noCheck},       // multiply
	"*":      {yes, 9, noCheck},       // alias of mul
	"x":      {yes, 9, noCheck},       // alias of mul
	"abs":    {not, 10, noCheck},      // absolute
	"tanh":   {not, 11, noCheck},      // hyperbolic tangent
	"pow":    {yes, 12, noCheck},      // power
	"base":   {yes, 13, noCheck},      // operand to the power of input
	"clip":   {yes, 14, noCheck},      // clip input
	"nois":   {not, 15, noCheck},      // white noise source
	"push":   {not, 16, noCheck},      // push to listing stack
	"pop":    {not, 17, checkPushPop}, // pop from listing stack
	"buff":   {yes, 18, buffUnique},   // listing buff loop, alias of buff0
	"buff0":  {yes, 18, buffUnique},   // listing buff loop
	"buff1":  {yes, 54, noCheck},      // listing buff loop
	"buff2":  {yes, 55, noCheck},      // listing buff loop
	"buff3":  {yes, 56, noCheck},      // listing buff loop
	"--":     {yes, 19, noCheck},      // subtract from operand
	"tap":    {yes, 20, noCheck},      // tap from loop
	"f2c":    {not, 21, noCheck},      // convert frequency to co-efficient
	"wav":    {yes, 22, checkWav},     // play wav file
	"8bit":   {yes, 23, noCheck},      // quantise input
	"index":  {not, 24, noCheck},      // index of listing // change to signal?
	"<sync":  {yes, 25, noCheck},      // receive sync pulse
	">sync":  {not, 26, noCheck},      // send sync pulse
	".>sync": {not, 26, noCheck},      // alias, launches listing
	//	"jl0":    {yes, 27, noCheck},    // jump if less than zero
	"/*":      {yes, 27, noCheck},		 // comments and/or name
	"level":  {yes, 28, noCheck}, // vary level of a listing
	".level": {yes, 28, noCheck}, // alias, launches listing
	"lvl":    {yes, 28, noCheck}, // vary level of a listing
	".lvl":   {yes, 28, noCheck}, // alias, launches listing
	"from":   {yes, 29, noCheck},     // receive output from a listing
	"sgn":    {not, 30, noCheck},        // sign of input
	"log":    {not, 31, noCheck},        // base-2 logarithm of input
	"/":      {yes, 32, noCheck},        // division
	"sub":    {yes, 33, noCheck},        // subtract operand
	"-":      {yes, 33, noCheck},        // alias of sub
	"setmix": {yes, 34, noCheck},        // set sensible level
	"print":  {not, 35, noCheck},        // print input to info display
	"\\":     {yes, 36, noCheck},        // "\"
	"out*":   {yes, 37, checkOut},     // multiply named signal
	"pan":    {yes, 38, noCheck}, // vary pan of a listing
	".pan":   {yes, 38, noCheck}, // alias, launches listing
	"all":    {not, 39, checkIndex},     // receive output of all preceding listings
	"fft":    {not, 40, noCheck},        // create fourier transform
	"ifft":   {not, 41, noCheck},        // receive from fourier representation
	"fftrnc": {yes, 42, noCheck},        // truncate spectrum
	"shfft":  {yes, 43, noCheck},        // shift spectrum
	"ffrz":   {yes, 44, noCheck},        // freeze-hold spectrum
	"gafft":  {yes, 45, noCheck},        // gate spectrum
	"rev":    {not, 46, noCheck},        // reverse spectrum
	"ffltr":  {yes, 47, noCheck},        // apply weighted average filter to spectrum
	"ffzy":   {not, 48, noCheck},        // rotate phases by random values
	"ffaze":  {yes, 49, noCheck},        // rotate phases by operand
	"reu":    {not, 50, noCheck},        // reverse each half of complex spectrum
	"halt":   {not, 51, noCheck},        // halt sound engine for time specified by input (experimental)
	"4lp":    {not, 52, checkAlp},        // prototype all-pass filter
	"panic":  {not, 53, noCheck},        // artificially induce a SE panic, for testing

	// specials. Not intended for sound engine, except 'deleted'
	"]":       {not, 0, endFunctionDefine},   // end function input
	":":       {yes, 0, modeSet},             // command
	"fade":    {yes, 0, checkFade},           // set fade out
	"del":     {yes, 0, enactDelete},         // delete a listing
	"erase":   {yes, 0, eraseOperations},     // erase a listing
	"mute":    {yes, 0, enactMute},           // mute a listing
	"m":       {yes, 0, enactMute},           // alias of mute
	"solo":    {yes, 0, enactSolo},           // solo a listing
	"release": {yes, 0, checkRelease},        // set limiter release
	"unmute":  {not, 0, unmuteAll},           // unmute all listings
	"unsolo":  {not, 0, unmuteAll},           // alias for unmute all listings
	".mute":   {yes, 0, enactMute},           // alias, launches listing
	".del":    {yes, 0, enactDelete},         // alias, launches listing
	".solo":   {yes, 0, enactSolo},           // alias, launches listing
	"//":      {yes, 0, checkComment},        // comments
	"load":    {yes, 0, loadReloadAppend},    // load listing by filename
	"ld":      {yes, 0, loadReloadAppend},    // alias of load
	"[":       {yes, 0, beginFunctionDefine}, // begin function input
	"ls":      {yes, 0, ls},                  // list listings
	"ct":      {yes, 0, adjustClip},          // individual clip threshold
	"rld":     {yes, 0, loadReloadAppend},    // reload a listing
	"r":       {yes, 0, loadReloadAppend},    // alias of rld
	"s":       {yes, 0, enactSolo},           // alias of solo
	"e":       {yes, 0, eraseOperations},     // alias of erase
	"apd":     {yes, 0, loadReloadAppend},    // launch index to new listing
	"do":      {yes, 0, doLoop},              // repeat next operation [operand] times
	"d":       {yes, 0, enactDelete},         // alias of del
	"deleted": {not, 0, noCheck},             // for internal use
	"m+":      {yes, 0, enactMute},           // add to mute group
	"n":       {yes, 0, enactMute},           // add to mute group
	"gain":    {yes, 0, adjustGain},          // set overall mono gain before limiter
	"record":  {yes, 0, recordWav},           // commence recording of wav file
	"wait":    {yes, 0, enactWait},           // for testing scripts, rounded to Milliseconds
}

type syncState int

type data struct {
	listingStack
	daisyChains []int
}

type opSE struct {
	N   int // signal number
	Opn int // operation switch index
	P   bool
	i   int // index of persisted signal
	//Opd string
}

type listingStack struct {
	reload  int
	listing []opSE
	sigs    []float64
	stack   []float64
	syncSt8 syncState
	m       float64
	buff    []float64
	buff1   []float64
	buff2   []float64
	buff3   []float64
	alp     [alpLen]float64
	alp1    [alpLen]float64
	alp2    [alpLen]float64
	alp3    [alpLen]float64
	lv, pan,
	peakfreq float64
	fftArr,
	ifftArr,
	ifft2 [N]float64
	z, zf [N]complex128
	ffrz  bool
	lim,
	limPreH, limPreHX,
	limPreL, limPreLX float64
}

const infoBuffer = 96

// communication channels
var (
	stop     = make(chan struct{}) // confirm on close()
	pause    = make(chan bool)     // bool is purely semantic
	transmit = make(chan *data)
	accepted = make(chan int)
	report   = make(chan int)  // send current listing on panic

	info    = make(chan string, infoBuffer) // arbitrary buffer length, 48000Hz = 960 x 50Hz
	carryOn = make(chan bool)
)

type muteSlice []float64

// communication variables
var (
	started bool // latches
	exit    bool // initiate shutdown
	mutes   muteSlice
	levels  []float64
	rs      bool                                     // root-sync between running instances
	fade    = 1 / (MIN_FADE * SampleRate)
	release = releaseFrom(defaultRelease, SampleRate) // should be calculated from sc.sampleRate
	gain    = baseGain
	clipThr = 1.0 // individual listing limiter threshold
	rst   bool
	underRun int
	eq bool = yes
)

type noise uint64

var mouse = struct {
	X, // -255 to 255
	Y,
	Left, // 0 or 1
	Right,
	Middle float64
	mc bool
}{
	X:  1,
	Y:  1,
	mc: yes, // mouse curve: not=linear, yes=exponential
}

type disp struct { // indicates:
	On      bool          // Syntə is running
	Mode    string        // func add fon/foff
	Vu      float64       // output sound level
	Clip    bool          // sound engine has clipped on output
	Load    time.Duration // sound engine loop time used
	Info    string        // messages sent from msg()
	MouseX  float64       // mouse X coordinate
	MouseY  float64       // mouse Y coordinate
	Paused  bool          // sound engine is paused
	Mute    []bool        // mutes of all listings
	SR      float64       // current sample rate
	GR      bool          // limiter is in effect
	GRl     int          // per-listing limiter is in effect
	Sync    bool          // sync pulse sent
	Verbose bool          // show unrolled functions - all operations
	Channel string        // stereo/mono
}

var display = disp{}

type wavs []struct {
	Name string
	Data []float64
}

type token struct {
	tk     string
	reload int
	ext    bool
}

var (
	tokens   = make(chan token, 2<<12) // arbitrary capacity, will block input in extreme circumstances
	lockLoad = make(chan struct{}, 1)  // mutex on transferring listings
)

const ( // used in token parsing
	startNewOperation = iota
	startNewListing
	exitNow
	nextOperation
	continueParsing
)

type clear func(s string, i ...interface{}) int

var (
	writeLog bool
	log *os.File
)

const (
	backendPA  = iota
	backendSDL 
	backendOSS 
)

func checkFlag(sr float64) float64 {
	if len(os.Args) < 3 {
		return sr
	}
	switch os.Args[1] {
	case "--sr", "--SR", "-s":
		// break
	default:
		return sr
	}
	if os.Args[2] == "44" { // for convenience
		os.Args[2] = "44.1"
	}
	flag, err := strconv.ParseFloat(os.Args[2], 64)
	if err != nil {
		return sr
	}
	if flag < 200 { // auto-convert kHz
		flag *= 1e3
	}
	if flag < 12000 || flag > 192000 {
		// alert user here
		return sr
	}
    return flag
}


func main() {
	if len(os.Args) < 2 {
		if !run(os.Stdin, backendPA) {
			p("trying SDL2 backend...")
			run(os.Stdin, backendSDL)
		}
		return
	}
	switch os.Args[1] {
	case "--log":
		var err error
		log, err = os.OpenFile("info.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			pf("unable to log: %s\n", err)
			return
		}
		defer log.Close()
		_, err = log.WriteString(sf("\n-- Syntə info log %s --\n", time.Now()))
		if err != nil {
			pf("unable to log: %s\n", err)
			return
		}
		writeLog = true
		p("logging...")
	case "--prof", "-p":
		f, rr := os.Create("cpu.prof")
		if e(rr) {
			pf("no cpu profile: %v\n", rr)
		}
		defer f.Close()
		if rr := pprof.StartCPUProfile(f); e(rr) {
			pf("profiling not started: %v\n", rr)
		}
		defer pprof.StopCPUProfile() //*/
	case "-u", "--usage", "-h", "--help":
		p(`Available flags:
		--log
		--prof
		--mem
		--sr <hz>
		--info
		--listings.
Only one may be used at a time`)
		return
	case "--info", "-i":
		infoTelem()
		return
	case "--listings", "--listing", "-l":
		listingsDisplay()
		return
	case "--sdl", "--SDL", "--sdl2", "--SDL2":
		run(os.Stdin, backendSDL)
		return
	case "--oss", "--OSS", "-o", "--mackie", "-m":
		run(os.Stdin, backendOSS)
		return
	case "--sr", "--SR", "-s":
		// allow for checkFlag()
	default:
		p("flag not recognised")
		return
	}
	if !run(os.Stdin, backendPA) {
		p("trying SDL2 backend...")
		run(os.Stdin, backendSDL)
	}
	if os.Args[1] == "--mem" {
		f, rr := os.Create("mem.prof")
		if e(rr) {
			pf("no mem profile: %v\n", rr)
		}
		pprof.WriteHeapProfile(f)
		f.Close()
	}
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

func emptyTokens() {
	for len(tokens) > 0 {
		<-tokens
	}
}

func run(from io.Reader, da int) bool {
	saveJson(disp{On: false}, infoFile)
	time.Sleep(30 * time.Millisecond) // infodisplay would be written in this time
	Json, _ := os.ReadFile(infoFile)
	json.Unmarshal(Json, &display)
	if display.On {
		pf("instance of synte already running in this directory\n")
		return true
	}
	saveJson([]listing{{operation{Op: advisory}}}, listingsFile)

	setup := setupSoundcard{}
	switch da {
	case backendPA:
		var ok bool
		setup, ok = setupPortaudio()
		if !ok {
			return false
		}
		defer setup.cln()
	case backendSDL:
		var ok bool
		setup, ok = setupSDL()
		if !ok {
			return false
		}
		defer setup.cln()
	case backendOSS:
		var ok bool
		setup, ok = setupOSS()
		if !ok {
			return false
		}
		defer setup.cln()
	}
	sc := setup.soundcard

	SampleRate = sc.sampleRate

	display = disp{
		On:		 true,
		Mode:    "off",
		MouseX:  1,
		MouseY:  1,
		SR:		 SampleRate,
		Channel: "stereo",
	}
	go infoDisplay()

	msg(setup.info)

	if writeLog {
		log.WriteString(sf("soundcard: %dbit %2gkHz\n", sc.format, sc.sampleRate))
	}

	t, twavs, wavSlice := newSystemState(sc)
	go SoundEngine(sc, twavs)
	go mouseRead()

	// TODO add sc, twavs as args to watchdog, they don't mutate
	go func() { // watchdog, anonymous to use variable in scope: dispListings
		// This function will restart the sound engine in the event of a panic
		for {
			current := <-report // unblocks on sound engine panic
			<-stop
			if exit { // don't restart if legitimate exit
				return
			}
			stop = make(chan struct{})
			go SoundEngine(sc, twavs)
			lockLoad <- struct{}{}
			emptyTokens()
			tokens <- token{"_", -1, yes}              // hack to restart input
			for i := 0; i < len(t.dispListings); i++ { // preload listings into tokens buffer
				if t.dispListings[i][0].Op == "deleted" {
					continue
				}
				rr := reloadExcept(current, i)
				if e(rr) {
					msg("%v", rr)
					break
				}
			}
			t.do = 0
			<-lockLoad
			msg("listing %d deleted, scan edit and reload", current)
			msg(">>> Sound Engine restarted")
			time.Sleep(1 * time.Second) // hold-off
		}
	}()

	go readInput(from) // scan stdin from goroutine to allow external concurrent input
	go reloadListing() // poll '.temp/*.syt' modified time and reload if changed

	usage := loadUsage() // local usage telemetry
	loadExternalFile := not // TODO move this to listingState

start:
	for { // main loop
		t = initialiseListing(t)
		for i, w := range wavSlice {
			t.createListing = addSignal(t.createListing, w.Name, float64(i))
			rate := 1.0 / float64(len(w.Data))
			name := "r."+w.Name
			t.createListing = addSignal(t.createListing, name, rate)
		}
		// the purpose of clr is to reset the input if error while receiving tokens from external source, declared in this scope to read value of loadExternalFile
		t.clr = func(s string, i ...interface{}) int {
			emptyTokens()
			info <- fmt.Sprintf(s, i...)
			<-carryOn
			if loadExternalFile { // TODO remove this
				return startNewListing //
			} //
			return startNewOperation
		}
		if !loadExternalFile {
			displayHeader()
		}

	input:
		for { // input loop
			t.newOperation = newOperation{}
			if !loadExternalFile {
				pf("\r\t")
			}
			var do int
			t, loadExternalFile, do = parseNewOperation(t)
			switch do {
			case startNewListing:
				loadExternalFile = not
		//		if loadExternalFile {
					//	emptyTokens()
		//		}
				continue start
			case startNewOperation:
				if loadExternalFile {
					loadExternalFile = not
					emptyTokens()
					continue start
				}
				continue input
			case exitNow:
				break start
			}
			usage[t.operator] += 1

			// process exported signals
			// TODO make this a function and add to parseFunction too
			if _, inSg := t.signals[t.operand]; !inSg &&
			isUppercaseInitial(t.operand) &&
			!t.num.Is && !t.fIn &&
			t.operator != "//" && t.operator != "/*" { // optional: && t.operator == "out"
				if t.lenExported > maxExports {
					msg("we've ran out of exported signals :(")
					continue
				}
				if _, exported := t.exportedSignals[t.operand]; !exported {
					t.exportedSignals[t.operand] = lenReserved + t.lenExported
					t.daisyChains = append(t.daisyChains, lenReserved+t.lenExported)
					t.lenExported++
					msg("%s added to exported signals", t.operand)
				}
				t.signals[t.operand] = t.exportedSignals[t.operand]
			}
			o := operation{Op: t.operator, Opd: t.operand, num: t.num.Is, ber: t.num.Ber}
			t.dispListing = append(t.dispListing, o)
			if !t.isFunction { // contents of function have been added already
				t.newListing = append(t.newListing, o)
			}
			if t.fIn {
				continue
			}
			// include contents of a function
			switch o := t.newListing[len(t.newListing)-1]; o.Op {
			case "out":
				if o.Opd == "dac" {
					break input
				}
			case ".out", ".>sync", ".level", ".lvl", ".pan", "//", "deleted":
				break input
			}
			if !loadExternalFile {
				msg(" ")
			}
		}

		if !popPushParity(t) {
			continue
		}
		if multipleBuff(t) {
			continue
		}

		for _, o := range t.newListing {
			infoIfLogging("assign: num=%t,%f -> %s %s", o.num, o.ber, o.Op, o.Opd)
			if _, in := t.signals[o.Opd]; in {
				continue
			}
			if o.Opd == "" {
				t.signals[o.Opd] = 1
				continue
			}
			if o.num {
				t.createListing = addSignal(t.createListing, o.Opd, o.ber)
				infoIfLogging("  num: %s at %d -> %f", o.Opd, len(t.newSignals)-1, o.ber)
				continue
			}
			def := 0.0
			switch strings.TrimPrefix(o.Opd, "^")[:1] {
			case "'":
				def = 1
			case "\"":
				def = 0.5
			}
			t.createListing = addSignal(t.createListing, o.Opd, def)
			infoIfLogging("  sig: %s at %d, def: %2.1f", o.Opd, len(t.newSignals)-1, def)
		}

		if t.reload > -1 && t.reload < len(t.verbose) {
			for l, o := range t.newListing {
				for _, v := range t.verbose[t.reload] {
					if o.num || o.Opd != v.Opd || o.Opd == "" {
						continue
					}
					t.newListing[l].P = yes // persist signal
					t.newListing[l].i = v.N
				}
			}
		}

		for i, o := range t.newListing {
			t.newListing[i].N = t.signals[o.Opd]
			s := t.signals[o.Opd]
			infoIfLogging("adding: %s at %d -> %f", o.Opd, s, t.newSignals[s])
			t.newListing[i].Opn = operators[o.Op].N
		}

		if display.Paused {
			<-pause
			display.Paused = not
		}

		lockLoad <- struct{}{}
		if !started { // anull/truncate these in case sound engine restarted
			t.dispListings = make([]listing, 0, 15) // arbitrary capacity
			t.verbose = make([]listing, 0, 15)
		}
		transmit <- collate(&t)
		a := <-accepted
		if a != len(t.dispListings) {
			infoIfLogging("len(mutes): %d, len(disp): %d, accepted: %d", len(mutes), len(t.dispListings), a)
			time.Sleep(200 * time.Millisecond)
		}
		<-lockLoad

		if !started {
			started = yes
		}

		timestamp := time.Now().Format("02-01-06.15:04")
		f := "recordings/listing." + timestamp + ".json"
		if !saveJson(t.newListing, f) {
			msg("listing not recorded, check 'recordings/' directory exists")
		}
		display.Verbose = not
		if !saveJson(t.dispListings, listingsFile) {
			msg("listing display not updated, check %q exists", listingsFile)
		}
	}
	if record {
		closeWavFile()
	}
	saveUsage(usage, t)
	if underRun > 0 {
		pf("underruns: %d", underRun)
	}
	return true // success
}

func parseNewOperation(t systemState) (systemState, bool, int) {
	ldExt, result := readTokenPair(&t)
	if result != nextOperation {
		t.do = 0
		return t, ldExt, result
	}
	for t.do > 1 { // one done below
		tokens <- token{t.operator, -1, not}
		d := strings.ReplaceAll(t.operand, "{i}", sf("%d", t.to-t.do+1))
		d = strings.ReplaceAll(d, "{i+1}", sf("%d", t.to-t.do+2))
		if y := t.hasOperand[t.operator]; y { // to avoid blank opds being sent
			tokens <- token{d, -1, not}
		}
		t.do--
	}
	t.operand = strings.ReplaceAll(t.operand, "{i}", sf("%d", 0))
	t.operand = strings.ReplaceAll(t.operand, "{i+1}", sf("%d", 1))

	t, r := addIfFunction(t)
	if r != continueParsing {
		return t, ldExt, r
	}
	t, r = operators[t.operator].process(t)
	return t, ldExt, r
}

func addIfFunction(t systemState) (systemState, int) {
	if !t.isFunction {
		return t, continueParsing
	}
	function, ok := parseFunction(t)
	if !ok {
		return t, startNewOperation
	}
	t.funCount++
	t.newListing = append(t.newListing, function...)
	return t, nextOperation
}

func loadNewListing(listing []operation) []opSE {
	l := make([]opSE, len(listing))
	for i, o := range listing {
		l[i] = opSE{
			N:   o.N,
			Opn: o.Opn,
			P:   o.P,
			//Opd: o.Opd,
			i:   o.i,
		}
	}
	return l
}

func collate(t *systemState) *data {
	safe := t.newSignals
	if t.newListing[0].Op == "deleted" {
		safe = make([]float64, lenReserved + maxExports)
	}
	d := &data{
		daisyChains: t.daisyChains,
		listingStack: listingStack{
			reload:  t.reload,
			listing: loadNewListing(t.newListing),
			lv:       1,
			peakfreq: 800 / t.sampleRate,
			buff:     make([]float64, t.tapeLen),
			buff1:    make([]float64, t.tapeLen),
			buff2:    make([]float64, t.tapeLen),
			buff3:    make([]float64, t.tapeLen),
			sigs:     safe,
			stack:    make([]float64, 0, 4),
		},
	}
	m := 1.0
	switch o := t.newListing[len(t.newListing)-1]; o.Op {
	case ".out", ".>sync", ".level", ".lvl", ".pan", "deleted": // silent listings
		m = 0 // to display as muted
	}
	if t.reload > -1 && t.reload < len(t.dispListings) {
		infoIfLogging("reload: %d, len(disp): %d", t.reload, len(t.dispListings))
		t.dispListings[t.reload] = t.dispListing
		t.verbose[t.reload] = t.newListing
		mutes.set(t.reload, m)
		if levels[t.reload] < 0.1 {
			 // to avoid ghost listings from previous level set to zero
			levels[t.reload] = 1
		}
		return d
	}
	t.dispListings = append(t.dispListings, t.dispListing)
	t.verbose = append(t.verbose, t.newListing)
	if len(mutes) >= len(t.dispListings) { // if restart has happened
		infoIfLogging("append mutes skipped: %d", len(t.dispListings)-1)
		return d
	}
	infoIfLogging("append: %d", len(t.dispListings)-1)
	display.Mute = append(display.Mute, (m == 0))
	mutes = append(mutes, m)
	levels = append(levels, 1)
	t.unsolo = append(t.unsolo, m)
	saveTempFile(*t, len(mutes)-1) // second argument sets name of file
	return d
}

const (
	mute   = iota // 0
	unmute        // 1
)

func (m *muteSlice) set(i int, v float64) {
	if i >= len(display.Mute) {
		infoIfLogging("out of bounds display.Mute access: %d, len=%d", i, len(display.Mute))
		return
	}
	if i >= len(*m) {
		infoIfLogging("out of bounds mutes access: %d, len=%d", i, len(*m))
		return
	}
	display.Mute[i] = v == mute // convert to bool
	(*m)[i] = v
}

type callback func(sr float64)

type cleanup func()

type soundcard struct {
	sampleRate float64
	format     int
	output     callback
}

type setupSoundcard struct {
	soundcard
	cln cleanup
	info string
}

func receiveSample(
	s stereoPair,
	loadThresh time.Duration,
	started bool,
) (stereoPair, bool) {
	se := stereoPair{}
	select {
	case <-stop: // if sound engine has ended
		return s, not
	case se = <-samples:
		// nothing to do
	default:
		if len(samples) > 0 { // prefer samples
			se = <-samples
			break
		}
		if !started || s.pause {
			return s, yes
		}
		// only degrade when load is high, to avoid lazy underruns
		if display.Load > loadThresh {
			underRun++
			return s, yes
		}
		se = <- samples // otherwise wait for new sample
	}
	return se, yes
}

func loadThreshAt(sr float64) time.Duration {
	return LoadThresh * time.Second / ( 100 * time.Duration(sr))
}

type stereoPair struct {
	left, right float64
	running, pause bool
}

func (y *stereoPair) stereoLpf(x stereoPair, coeff float64) {
	y.left += (x.left - y.left) * coeff
	y.right += (x.right - y.right) * coeff
	// running and pause need to be updated by incoming samples
	y.running = x.running
	y.pause = x.pause
}


func readTokenPair(t *systemState) (bool, int) {
	tt := <-tokens
	t.operator, t.reload = tt.tk, tt.reload
	if (len(t.operator) > 2 && byte(t.operator[1]) == 91) || t.operator == "_" || t.operator == "" {
		return tt.ext, startNewOperation
	}
	t.operator = strings.TrimSuffix(t.operator, ",")  // to allow comma separation of tokens
	if len(t.operator) < 1 {
		<-tokens
		return tt.ext, startNewOperation
	}
	if t.operator[:1] == ":" { // hacky shorthand
		t.operand = t.operator[1:]
		t.operator = ":"
		return tt.ext, nextOperation
	}
	hO, in := t.hasOperand[t.operator]
	if !in {
		r := t.clr("operator or function doesn't exist: %s", t.operator)
		return tt.ext, r
	}
	_, t.isFunction = t.funcs[t.operator]

	if !hO {
		return tt.ext, nextOperation
	}
	// parse second token
	tt = <-tokens
	t.operand, t.reload = tt.tk, tt.reload
	t.operand = strings.TrimSuffix(t.operand, ",") // to allow comma separation of tokens
	if t.operand == "_" || t.operand == "" {
		return tt.ext, startNewOperation
	}
	s := strings.ReplaceAll(t.operand, "{i}", "0")
	s = strings.ReplaceAll(s, "{i+1}", "0")
	t.operands = strings.Split(s, ",")
	if !t.isFunction && len(t.operands) > 1 {
		r := t.clr("only functions can have multiple operands")
		return tt.ext, r
	}
	pass := t.wmap[t.operand] && t.operator == "wav"
	switch t.operator { // operand can start with a number
	case "ls", "load", "//":
		pass = true
	}
	if !strings.ContainsAny(s[:1], "+-.0123456789") || pass || t.isFunction {
		return tt.ext, nextOperation
	}
	if t.num.Ber, t.num.Is = parseType(s, t.operator); !t.num.Is {
		r := t.clr("")
		return tt.ext, r // parseType will report error
	}
	//infoIfLogging("token num: %f -> %s %s", t.num.Is, t.num.Ber, t.operator, t.operand)
	return tt.ext, nextOperation
}

type args struct{ at, at1, at2 bool }

func parseFunction(t systemState) (listing, bool) {
	function := make(listing, len(t.funcs[t.operator].Body))
	copy(function, t.funcs[t.operator].Body)
	funArgs := args{}
	funArgs, function = processFunction(t.funCount, t, function)
	ok := argsCorrect(t.operator, funArgs, t.clr, len(t.operands))
	if !ok {
		return nil, not
	}
	for i, opd := range t.operands { // opd shadowed
		if t.operands[i] == "" {
			t.clr("%s: empty argument %d", t.operator, i+1)
			return nil, not
		}
		if !strings.ContainsAny(opd[:1], "+-.0123456789") {
			continue
		}
		if _, ok := parseType(opd, ""); !ok {
			return nil, not // parseType will report error
		}
	}
	for i, o := range function {
		if o.Opd == "" {
			continue
		}
		switch o.Opd {
		case "@":
			o.Opd = t.operands[0]
		case "@1":
			o.Opd = t.operands[1]
		case "@2":
			o.Opd = t.operands[2]
		}
		if strings.ContainsAny(o.Opd[:1], "+-.0123456789") {
			o.ber, o.num = parseType(o.Opd, o.Op)
		}
		function[i] = o
	}
	return function, yes
}

func processFunction(fun int, t systemState, f listing) (args, listing) {
	funArgs := args{}
	for i, o := range f {
		if o.Opd == "" {
			continue
		}
		if _, in := t.signals[o.Opd]; in || isUppercaseInitialOrDefaultExported(o.Opd) {
			continue
		}
		funArgs = countFuncArgs(o.Opd, funArgs)
		switch o.Opd[:1] {
		case "^", "@":
			continue
		}
		if strings.ContainsAny(o.Opd[:1], "+-.0123456789") {
			if _, num := parseType(o.Opd, o.Op); num {
				continue
			}
		}
		// TODO add Exported signals here?
		f[i].Opd += sf(".%d", fun)
		switch o.Op {
		case "out":
			t.out[f[i].Opd] = assigned // implicitly de-referenced
		}
	}
	return funArgs, f
}

func countFuncArgs(opd string, funArgs args) args {
	switch opd {
	case "@":
		funArgs.at = yes
	case "@1":
		funArgs.at1 = yes
	case "@2":
		funArgs.at2 = yes
	}
	return funArgs
}

func argsCorrect(op string, funArgs args, clr clear, l int) bool {
	a := 0
	switch funArgs {
	case args{not, not, not}:
		// nop
	case args{yes, not, not}:
		a = 1
	case args{yes, yes, not}:
		a = 2
	case args{yes, yes, yes}:
		a = 3
	default:
		clr("malformed function") // probably not needed
		return not
	}
	if a < l {
		switch {
		case l-a == 1:
			msg("%s: last operand ignored", op)
		case l-a > 1:
			msg("last %d operands ignored", l-a)
		}
	}
	if a > l {
		switch {
		case a == 1:
			clr("%s requires an operand", op)
			return not
		case a > 1:
			clr("%s requires %d operands", op, a)
			return not
		}
	}
	return yes
}

// parseType() evaluates conversion of types
func parseType(expr, op string) (n float64, b bool) { // TODO pass in s.sampleRate
	switch {
	case len(expr) > 1 && expr[len(expr)-1:] == "!":
		if n, b = evaluateExpr(expr[:len(expr)-1]); !b {
			return 0, false
		}
	case len(expr) > 2 && expr[len(expr)-2:] == "ms":
		if n, b = evaluateExpr(expr[:len(expr)-2]); !b {
			msg("erm s?")
			return 0, false
		}
		n = 1 / ((n / 1000) * SampleRate)
		if !nyquist(n, expr) {
			return 0, false
		}
	case len(expr) > 3 && expr[len(expr)-3:] == "bpm":
		if n, b = evaluateExpr(expr[:len(expr)-3]); !b {
			return 0, false
		}
		if n > 3000 && op == "in" {
			msg("%.fbpm? You're 'aving a larf mate", n)
			return 0, false
		}
		n /= 60
		n /= SampleRate
	case len(expr) > 1 && expr[len(expr)-1:] == "m":
		if n, b = evaluateExpr(expr[:len(expr)-1]); !b {
			return 0, false
		}
		n *= 60
		n = 1 / (n * SampleRate)
	case len(expr) > 4 && expr[len(expr)-4:] == "mins":
		if n, b = evaluateExpr(expr[:len(expr)-4]); !b {
			return 0, false
		}
		n *= 60
		n = 1 / (n * SampleRate)
	case len(expr) > 1 && expr[len(expr)-1:] == "s":
		if n, b = evaluateExpr(expr[:len(expr)-1]); !b {
			return 0, false
		}
		n = 1 / (n * SampleRate)
		if !nyquist(n, expr) {
			return 0, false
		}
	case len(expr) > 3 && expr[len(expr)-3:] == "khz":
		if n, b = evaluateExpr(expr[:len(expr)-3]); !b {
			return 0, false
		}
		n *= 1e3
		n /= SampleRate
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
		n = math.Pow(10, n)
	default:
		if n, b = evaluateExpr(expr); !b {
			return 0, false
		}
		if math.Abs(n) > 100 {
			msg("%.3g exceeds sensible values, use a type", n)
			return 0, false
		}
	}
	if math.IsInf(n, 0) || n != n { // ideally also check for zero in specific cases
		msg("number not useful")
		return 0, false
	}
	return n, true
}
func nyquist(n float64, e string) bool {
	ny := 2e4 / SampleRate
	if bounds(n, ny) {
		msg("'%s' is an inaudible frequency >20kHz", e)
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

// evaluateExpr() won't handle expressions containing eg. 1e-3-1e-2
func evaluateExpr(expr string) (float64, bool) {
	opds := []string{expr}
	var rr error
	var n, n2 float64
	var op string
	if n, rr = strconv.ParseFloat(opds[0], 64); !e(rr) {
		return n, true
	}
	for _, v := range []string{"*", "/", "+", "-"} {
		if strings.Contains(strings.TrimPrefix(expr, "-"), v) {
			opds = strings.SplitN(expr, v, 2)
			if strings.HasPrefix(expr, "-") {
				opds = strings.SplitN(strings.TrimPrefix(expr, "-"), v, 2)
				opds[0] = "-" + opds[0]
			}
			op = v
			break
		}
	}
	if n, rr = strconv.ParseFloat(opds[0], 64); e(rr) {
		msg("%s is not a number", opds[0])
		return 0, false
	}
	if len(opds) == 1 {
		return n, true
	}
	if len(opds) > 2 {
		msg("third operand in expression ignored")
		return 0, false
	}
	if n2, rr = strconv.ParseFloat(opds[1], 64); e(rr) {
		msg("%s is not a number", opds[1])
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

func infoIfLogging(s string, i ...interface{}) {
	if !writeLog {
		return
	}
	info <- sf(s, i...)
	<-carryOn
}

func infoDisplay() {
	var (
		c, s, g, gl int
	)
	infoF, err := os.OpenFile(logFile, os.O_TRUNC|os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		pf("unable to write messages to %q: %s\n", logFile, err)
	}
	defer infoF.Close()
	h := sf("\n-- Syntə -- \t\t%s\n\n", time.Now().Format("02/01/06 15:04"))
	_, err = infoF.WriteString(h)
	if err != nil {
		pf("unable to log: %s\n", err)
	}
	for {
		if writeLog {
			display.Info = "Logging..."
		}
		if !saveJson(display, infoFile) {
			pf("info display not updated, check file %s exists\n", infoFile)
			time.Sleep(2 * time.Second)
			return
		}
		select {
		case i := <-info:
			infoF.WriteString(i + "\n")
		case carryOn <- yes: // semaphore: received, continue
		case <-stop:
			if !exit {
				break
			}
			display.Info = sf("Syntə closed")
			display.On = not // stops timer in info display
			display.SR = 0 // so previous soundcard info not displayed, in case different
			display.Load = 0
			saveJson(display, infoFile)
			return
		default: // passthrough
		}
		time.Sleep(20 * time.Millisecond) // coarse loop timing

		display.Clip, c = holdIndicator(display.Clip, c)
		display.Sync, s = holdIndicator(display.Sync, s)
		display.GR, g   = holdIndicator(display.GR, g)
		var dGRl bool
		dGRl, gl        = holdIndicator(display.GRl > 0, gl)
		if !dGRl {
			display.GRl = 0
		}
	}
}

func holdIndicator(d bool, i int) (bool, int) {
	if d {
		i++
	}
	if i > 20 {
		return not, 0
	}
	return d, i
}

func transfer(d []listingStack, tr *data) ([]listingStack, []int) {
	if tr.reload < len(d) && tr.reload > -1 { // for d reload
		coreDump(d[tr.reload], "reloaded_listing_old")
		sg := d[tr.reload].sigs
		d[tr.reload].listing = tr.listing
		d[tr.reload].sigs = tr.sigs
		if rst {
			return d, tr.daisyChains
		}
		for _, o := range tr.listing {
			if o.P {
				d[tr.reload].sigs[o.N] = sg[o.i]
			}
		}
		d[tr.reload].pan = 0 // to avoid 'stuck' pans
		coreDump(d[tr.reload], "reloaded_listing_new")
		return d, tr.daisyChains
	}
	coreDump(tr.listingStack, "launched_listing")
	return append(d, tr.listingStack), tr.daisyChains
}

var samples = make(chan stereoPair, writeBufferLen)

// The Sound Engine does the bare minimum to generate audio
// It is freewheeling, it won't block on the action of any other goroutine, only on IO, namely writing to soundcard
// The latency and jitter of the audio output is entirely dependent on the soundcard and its OS driver,
// except where the calculations don't complete in time under heavy load and output buffer underruns. Frequency accuracy is determined by the soundcard clock and precision of float64 type
// Now with glitch protection! IO handled in separate go routine. Pitch accuracy will degrade under heavy load
func SoundEngine(sc soundcard, wavs [][]float64) {
	defer close(stop)

	const (
		Tau = 2 * math.Pi

		RateIntegrationTime = writeBufferLen // to display load, number of samples

		run syncState = iota
		on
		off
	)

	var (
		lpf15Hz = lpf_coeff(10, sc.sampleRate)  // smooth mouse, mutes, gain
		lpf1kHz = lpf_coeff(1e3, sc.sampleRate) // smooth levels

		// per-listing limiter
		hpf7241Hz = hpf_coeff(7241, sc.sampleRate)       // high emphasis
		hpf160Hz  = hpf_coeff(160, sc.sampleRate)        // low emphasis
		lpf2point4Hz  = lpf_coeff(2.4435, sc.sampleRate) // 'VU' averaging ~400ms
		headroom = math.Pow10(36/20) // decibels

		// main out DC blocking
		hpf20Hz = hpf_coeff(20, sc.sampleRate)

		// main output limiter
		hiBandCoeff  = hpf_coeff(10240, sc.sampleRate)
		midBandCoeff = hpf_coeff(320, sc.sampleRate)

		// loudness eq
		hpf320Hz  = hpf_coeff(320, sc.sampleRate)        // loudness eq
	)

	const (
		Thr = 1.0 // must be less than or equal to one
		holdTime = 0.02 // 20ms
	)

	var (
		no = noise(time.Now().UnixNano())
		tapeLen = int(sc.sampleRate) * TAPE_LENGTH

		twentyHz = 20/sc.sampleRate

		lim, h float64 = Thr, 2 // limiter, hold
		//hold = math.Pow(10, -2/(holdTime*sc.sampleRate))
		hold = releaseFrom(holdTime, sc.sampleRate)
		env  float64 = 1      // for exit envelope
		mid, // output
		peak, // vu meter
		dither float64
		n int // loop counter

		rate     = time.Duration(7292) // loop timer, initialised to approximate resting rate
		lastTime time.Time
		rates    [RateIntegrationTime]time.Duration
		t        time.Duration

		s      float64 = 1    // sync=0
		mx, my float64 = 1, 1 // mouse smooth intermediates
		hpfM, xM float64      // DC-blocking high pass filter
		hpfS, xS float64      // DC-blocking high pass filter
		eqM, eqXM float64     // loudness eq, mid
		eqS, eqXS float64     // loudness eq, sides
		g      float64        // gain smooth intermediate
		sum,				  // mono sum for detection
		hiBand, hiBandPrev,
		midBand, midBandPrev float64                      // limiter pre-emphasis
		α       = 1 / (sc.sampleRate/(2*math.Pi*194) + 1) // co-efficient for setmix
		//hroom   = (sc.convFactor - 1.0) / sc.convFactor   // headroom for positive dither
		pd      int                                       // slated for removal
		sides   float64                                   // for stereo
		current int                                       // tracks index of active listing for recover()
		p       = 1.0                                     // pause variable

		daisyChains = make([]int, 0, 16) // made explicitly here to set capacity
	)
	defer close(samples)
	no *= 77777777777 // force overflow

	d := make([]listingStack, 0, 15) // this slice stores all listings and signals

	defer func() { // fail gracefully
		switch err := recover(); err {
		case nil:
			return // exit normally
		default:
			msg("oops - %s (view log in `debug/`)", err) // report error to infoDisplay
			stack := debug.Stack()
			infoIfLogging("%s", stack, err) // print stack trace
			if !writeLog {
				s := []byte(sf("err:\n%s \n\nstack:\n%s", err, stack))
				save(s, sf("debug/%s_stack_trace.txt", time.Now()))
				coreDump(d[current], "panicked_listing")
			}
			env = 0
			started = not
			report <- current
		}
	}()

	go sc.output(sc.sampleRate)
	defer closeOutput(sc.sampleRate)

	tr := *<-transmit // wait for first listing
	d = append(d, tr.listingStack)
	daisyChains = tr.daisyChains
	accepted <- len(d) // acknowledge
	coreDump(d[0], "first_listing")

	lastTime = time.Now()
	samples <- stereoPair{running: yes}
	for {
		select {
		case <-pause:
			p = 0
		case t := <-transmit:
			d, daisyChains = transfer(d, t)
			accepted <- len(d)
			if rs && rootSync() {
				lastTime = time.Now()
			}
		default:
			// play
		}
		if p == 0 && d[0].m < 1e-4 { // -80dB
			samples <- stereoPair{running: yes, pause: yes}
			pause <- not // blocks until `: play`, bool is purely semantic
			if exit {
				return
			}
			samples <- stereoPair{running: yes, pause: not}
			p = 1
			lastTime = time.Now()
		}

		if n%15127 == 0 { // arbitrary interval all-zeros protection for noise lfsr
			no ^= 1 << 27
		}

		mo := mouse
		mx = mx + (mo.X-mx)*lpf15Hz
		my = my + (mo.Y-my)*lpf15Hz

		//for i, l := range d { // this is incredibly slow
	listings:
		for i := 0; i < len(d); i++ { // much faster
			current = i
			//for _, ii := range daisyChains {
			for ii := 0; ii < len(daisyChains); ii++ {
				d[i].sigs[daisyChains[ii]] = d[(i+len(d)-1)%len(d)].sigs[daisyChains[ii]]
			}
			d[i].m = d[i].m + (p*mutes[i]-d[i].m)*lpf15Hz // anti-click filter
			d[i].lv = d[i].lv + (levels[i]-d[i].lv)*lpf1kHz
			//sigs := d[i].sigs
			// mouse values
			d[i].sigs[4] = mx
			d[i].sigs[5] = my
			d[i].sigs[6] = mo.Left
			d[i].sigs[7] = mo.Right
			d[i].sigs[8] = mo.Middle
			r := 0.0
			//op := 0
			ll := len(d[i].listing)
			for ii := 0; ii < ll; ii++ {
				//o := d[i].listing[ii]
				switch d[i].listing[ii].Opn {
				case 0: // "deleted", "//"
					continue listings
				case 1: // "+"
					r += d[i].sigs[d[i].listing[ii].N]
				case 2: // "out"
					d[i].sigs[d[i].listing[ii].N] = r
				case 3: // "out+"
					d[i].sigs[d[i].listing[ii].N] += r
				case 4: // "in"
					r = d[i].sigs[d[i].listing[ii].N]
				case 5: // "sine"
					//r = math.Sin(Tau * r)
					r = sine(r)
				case 6: // "mod"
					r = mod(r, d[i].sigs[d[i].listing[ii].N])
				case 7: // "gt"
					if r >= d[i].sigs[d[i].listing[ii].N] {
						r = 1
					} else {
						r = 0
					}
				case 8: // "lt"
					if r <= d[i].sigs[d[i].listing[ii].N] {
						r = 1
					} else {
						r = 0
					}
				case 9: // "mul", "x", "*":
					r *= d[i].sigs[d[i].listing[ii].N]
				case 10: // "abs"
					r = math.Abs(r)
				case 11: // "tanh"
					r = tanh(r)
				case 12: // "pow"
					//if math.Signbit(d[i].sigs[d[i].listing[ii].N]) && r == 0 {
					//	r = math.Copysign(1e-308, r)
					//}
					r = math.Pow(math.Abs(r), math.Abs(d[i].sigs[d[i].listing[ii].N]))
				case 13: // "base"
					sg := d[i].sigs[d[i].listing[ii].N]
					switch sg {
					case math.E, -math.E:
						r = math.Exp(r)
					case 2, -2:
						r = math.Exp2(r)
					default:
						r = math.Pow(math.Abs(sg), r)
					}
				case 14: // "clip"
					switch {
					case d[i].sigs[d[i].listing[ii].N] == 0:
						r = math.Max(0, math.Min(1, r))
					case d[i].sigs[d[i].listing[ii].N] > 0:
						r = math.Max(-d[i].sigs[d[i].listing[ii].N], math.Min(d[i].sigs[d[i].listing[ii].N], r))
					case d[i].sigs[d[i].listing[ii].N] < 0:
						r = math.Min(-d[i].sigs[d[i].listing[ii].N], math.Max(d[i].sigs[d[i].listing[ii].N], r))
					}
				case 15: // "nois"
					r *= no.ise() // roll a fresh one
					//if r > 0.9999 { panic("test") } // for testing
				case 16: // "push"
					d[i].stack = append(d[i].stack, r)
				case 17: // "pop"
					r = d[i].stack[len(d[i].stack)-1]
					d[i].stack = d[i].stack[:len(d[i].stack)-1]
				case 18: // "buff", "buff0"
					r = interpolatedBuffer(
						d[i].buff,
						d[i].sigs[d[i].listing[ii].N],
						r,
						n,
						tapeLen,
					)
				case 19: // "--"
					r = d[i].sigs[d[i].listing[ii].N] - r
				case 20: // "tap"
					r += interpolatedTap(
						d[i].buff,
						d[i].sigs[d[i].listing[ii].N],
						n,
						tapeLen,
					)
				case 21: // "f2c" // r = 1 / (1 + 1/(Tau*r))
					r = math.Abs(r)
					r *= Tau
					r /= (r + 1)
				case 22: // "wav"
					w:= wavs[int(d[i].sigs[d[i].listing[ii].N])]
					r += 1 // to allow negative input to reverse playback
					r = math.Abs(r)
					r = math.Mod(r, 1)
					l := len(w)
					r *= float64(l)
					r = interpolation(w, r)
				case 23: // "8bit"
					if d[i].sigs[d[i].listing[ii].N] == 0 {
						continue
					}
					r = float64(int8(r*d[i].sigs[d[i].listing[ii].N])) / d[i].sigs[d[i].listing[ii].N]
				case 24: // "index"
					r = float64(i)
				case 25: // "<sync"
					r *= s
					r += (1 - s) * d[i].sigs[d[i].listing[ii].N] // phase offset
				case 26: // ">sync", ".>sync"
					switch { // syncSt8 is a slice to make multiple >sync operations independent
					case r <= 0 && d[i].syncSt8 == run: // edge-detect
						s = 0
						display.Sync = yes
						d[i].syncSt8 = on
					case d[i].syncSt8 == on: // single sample pulse
						s = 1
						d[i].syncSt8 = off
					case r > 0: // reset
						d[i].syncSt8 = run
					}
				case 27:
					// nop
				/*case 27: // "jl0"
				if r <= 0 {
					op += int(d[i].sigs[d[i].listing[ii].N])
				}
				if op > len(list)-2 {
					op = len(list) - 2
				}*/
				case 28: // "level", ".level"
					l := int(d[i].sigs[d[i].listing[ii].N])
					if l > len(d)-1 || l < 0 {
						continue
					}
					levels[l] = r
					//levels[Min(len(levels), int(d[i].sigs[d[i].listing[ii].N]))] = r // alternative
				case 29: // "from"
					l := int(d[i].sigs[d[i].listing[ii].N])
					if l > len(d)-1 || l < 0 {
						continue
					}
					if l == i {
						continue
					}
					r = d[l].sigs[0]
				case 30: // "sgn"
					r = 1 - float64(math.Float64bits(r)>>62)
				case 31: // "log"
					r = math.Abs(r) // avoiding NaN
					r = math.Log2(r)
				case 32: // "/"
					//if d[i].sigs[d[i].listing[ii].N] == 0 {
						// swap for arbitrary small number
					//	d[i].sigs[d[i].listing[ii].N] = math.Copysign(1e-15, d[i].sigs[d[i].listing[ii].N])
					//}
					//r /= math.Max(-0.1, math.Min(0.1, d[i].sigs[d[i].listing[ii].N])) // alternative
					d := d[i].sigs[d[i].listing[ii].N]
					if d < 0.01 && d >= 0 {
						d = 0.01
					}
					if d > -0.01 && d < 0 {
						d = -0.01
					}
					r /= d
				//	r /= d[i].sigs[d[i].listing[ii].N]
				case 33: // "sub"
					r -= d[i].sigs[d[i].listing[ii].N]
				case 34: // "setmix"
					a := math.Max(twentyHz , math.Abs(d[i].sigs[d[i].listing[ii].N]))
					δ := a - d[i].peakfreq
					s := 0.0007 + math.Min(1, (math.Abs(δ) * a / d[i].peakfreq))
					d[i].peakfreq += δ * α * s
					//r *= math.Min(1, math.Sqrt(40/(d[i].peakfreq*sc.sampleRate+20)))
					r *= math.Sqrt(twentyHz/d[i].peakfreq)
				case 35: // "print"
					pd++ // unnecessary?
					if (pd)%32768 == 0 && !exit {
						info <- sf("listing %d: %.5g", i, r)
						pd += int(no >> 50)
					}
				case 36: // "\\"
					//if r == 0 {
					//	r = math.Copysign(1e-16, r)
					//}
					//r = d[i].sigs[d[i].listing[ii].N] / r

					if r < 0.01 && r >= 0 {
						r = 0.01
					}
					if r > -0.1 && r < 0 {
						r = -0.01
					}
					r = d[i].sigs[d[i].listing[ii].N] / r
				case 37: // "out*"
					d[i].sigs[d[i].listing[ii].N] *= r
				case 38: // "pan", ".pan"
					l := int(d[i].sigs[d[i].listing[ii].N])
					if l > len(d)-1 || l < 0 {
						continue
					}
					d[l].pan = math.Max(-1, math.Min(1, r))
				case 39: // "all"
					// r := 0 // allow mixing in of preceding listing
					for ii := range d[i].listing {
						if ii >= i {
							break // only 'all' preceding
						}
						r += d[ii].sigs[0]
					}
				case 40: // "fft"
					d[i].fftArr[n%N] = r
					if n%N2 == 0 && n >= N && !d[i].ffrz {
						nn := n % N
						var zz [N]complex128
						for n := range d[i].fftArr { // n is shadowed
							ww := float64(n) * N1
							w := math.Pow(1-ww*ww, 1.25) // modified Welch
							zz[n] = complex(w*d[i].fftArr[(n+nn)%N], 0)
						}
						d[i].z = fft(zz, 1)
					}
				case 41: // "ifft"
					if n%N == 0 && n >= N {
						zz := fft(d[i].z, -1)
						for n, z := range zz { // n, z are shadowed
							w := (1 - math.Cos(Tau*float64(n)*N1)) * 0.5 // Hann
							d[i].ifftArr[n] = w * real(z) * invN2
						}
					}
					if n%N == N2+1 && n >= N {
						zz := fft(d[i].z, -1)
						for n, z := range zz { // n, z are shadowed
							w := (1 - math.Cos(Tau*float64(n)*N1)) * 0.5 // Hann
							d[i].ifft2[n] = w * real(z) * invN2
						}
					}
					if !d[i].ffrz {
						r = d[i].ifftArr[n%N] + d[i].ifft2[(n+N2)%N]
					} else {
						r = (d[i].ifftArr[n%N] + d[i].ifftArr[(n+N2)%N])
					}
				case 42: // "fftrnc"
					if n%N2 == 0 && n >= N && !d[i].ffrz {
						switch {
						case d[i].sigs[d[i].listing[ii].N] > 0:
							l := int(N * d[i].sigs[d[i].listing[ii].N])
							for n := l; n < N; n++ {
								d[i].z[n] = complex(0, 0)
							}
						case d[i].sigs[d[i].listing[ii].N] < 0:
							l := -int(N * d[i].sigs[d[i].listing[ii].N])
							for n := range d[i].z {
								if n > l || n < N-l {
									d[i].z[n] = complex(0, 0)
								}
							}
						}
					}
				case 43: // "shfft"
					s := d[i].sigs[d[i].listing[ii].N]
					if n%N2 == 0 && n >= N && !d[i].ffrz {
						l := int(mod(s, 1) * N)
						for n := range d[i].z {
							nn := (N + n + l) % N
							d[i].z[n] = d[i].z[nn]
						}
					}
				case 44: // "ffrz"
					d[i].ffrz = d[i].sigs[d[i].listing[ii].N] == 0
				case 45: // "gafft"
					if n%N2 == 0 && n >= N && !d[i].ffrz {
						s := d[i].sigs[d[i].listing[ii].N] * 50
						gt := yes
						if s < 0 {
							s = -s
							gt = not
						}
						for n, zz := range d[i].z {
							if gt && math.Abs(real(zz)) < s {
								d[i].z[n] = 0
							} else if !gt && math.Abs(real(zz)) > s {
								d[i].z[n] = 0
							}
						}
					}
				case 46: // "rev"
					if n%N2 == 0 && n >= N && !d[i].ffrz {
						ii := i // from 'the blue book':
						for i, j := 0, len(d[ii].z)-1; i < j; i, j = i+1, j-1 {
							d[ii].z[i], d[ii].z[j] = d[ii].z[j], d[ii].z[i]
						}
					}
				case 47: // "ffltr"
					if n%N2 == 0 && n >= N && !d[i].ffrz {
						coeff := complex(math.Abs(d[i].sigs[d[i].listing[ii].N]*N), 0)
						coeff *= Tau
						coeff /= (coeff + 1)
						for n := range d[i].z {
							d[i].zf[n] = d[i].zf[n] + (d[i].z[n]-d[i].zf[n])*coeff
							d[i].z[n] = d[i].zf[n]
						}
					}
				case 48: // "ffzy"
					if n%N2 == 0 && n >= N && !d[i].ffrz {
						for n := range d[i].z {
							r, θ := cmplx.Polar(d[i].z[n])
							θ += math.Pi * no.ise()
							d[i].z[n] = cmplx.Rect(r, θ)
						}
					}
				case 49: // "ffaze"
					if n%N2 == 0 && n >= N && !d[i].ffrz {
						for n := range d[i].z {
							r, θ := cmplx.Polar(d[i].z[n])
							θ += Tau * d[i].sigs[d[i].listing[ii].N]
							d[i].z[n] = cmplx.Rect(r, θ)
						}
					}
				case 50: // "reu"
					if n%N2 == 0 && n >= N && !d[i].ffrz {
						ii := i // from 'the blue book':
						for i, j := 0, len(d[ii].z)/2; i < j; i, j = i+1, j-1 {
							d[ii].z[i], d[ii].z[j] = d[ii].z[j], d[ii].z[i]
						}
						for i, j := len(d[ii].z)/2, len(d[ii].z)-1; i < j; i, j = i+1, j-1 {
							d[ii].z[i], d[ii].z[j] = d[ii].z[j], d[ii].z[i]
						}
					}
				case 51: // "halt"
					r = math.Max(0, math.Min(1, r))
					time.Sleep(time.Duration(1e9 * r / sc.sampleRate))
				case 52: // "4lp"
					in := r - d[i].alp[(n+alpLen-int(0.0047*sc.sampleRate))%alpLen]/2
					d[i].alp[n%alpLen] = in
					a := d[i].alp[(n+alpLen-int(0.0047*sc.sampleRate))%alpLen] + in/2 // 4.7ms

					in2 := a - d[i].alp1[(n+alpLen-int(0.0076*sc.sampleRate))%alpLen]/2
					d[i].alp1[n%alpLen] = in2
					a2 := d[i].alp1[(n+alpLen-int(0.0076*sc.sampleRate))%alpLen] + in2/2 // 7.6ms

					in3 := a2 - d[i].alp2[(n+alpLen-int(0.0123*sc.sampleRate))%alpLen]/2
					d[i].alp2[n%alpLen] = in3
					a3 := d[i].alp2[(n+alpLen-int(0.0123*sc.sampleRate))%alpLen] + in3/2 // 12.3ms

					in4 := a3 - d[i].alp3[(n+alpLen-int(0.0198*sc.sampleRate))%alpLen]/2
					d[i].alp3[n%alpLen] = in4
					r = d[i].alp3[(n+alpLen-int(0.0198*sc.sampleRate))%alpLen] + in4/2 // 19.8ms
					r = math.Max(-5, math.Min(5, r)) // to mitigate possible instability
					// 4.7, 5.4, 9.1, 1.27 // alternative delays
				case 53: // "panic"
					panic("test")
				case 54: // "buff1"
					r = interpolatedBuffer(
						d[i].buff1,
						d[i].sigs[d[i].listing[ii].N],
						r,
						n,
						tapeLen,
					)
				case 55: // "buff2"
					r = interpolatedBuffer(
						d[i].buff2,
						d[i].sigs[d[i].listing[ii].N],
						r,
						n,
						tapeLen,
					)
				case 56: // "buff3"
					r = interpolatedBuffer(
						d[i].buff3,
						d[i].sigs[d[i].listing[ii].N],
						r,
						n,
						tapeLen,
					)
				default:
					continue listings
				}
				//op++
			}
			d[i].stack = d[i].stack[:0] // delete stack

			if math.IsInf(d[i].sigs[0], 0) { // infinity to '93
				d[i].sigs[0] = 0
				panic(sf("listing: %d, %d - ±Inf", i, current))
			}
			if d[i].sigs[0] != d[i].sigs[0] { // test for NaN
				d[i].sigs[0] = 0
				panic(sf("listing: %d, %d - NaN", i, current))
			}
			d[i].sigs[0] *= d[i].m * d[i].lv
			out := d[i].sigs[0]

			d[i].limPreH = ( d[i].limPreH + out - d[i].limPreHX ) * hpf7241Hz
			d[i].limPreHX = out
			d[i].limPreL = ( d[i].limPreL + out - d[i].limPreLX ) * hpf160Hz
			d[i].limPreLX = out
			det := math.Abs((28 * d[i].limPreH + 2 * d[i].limPreL + 0.48 * out)) - clipThr
			// ~300ms integration
			d[i].lim = d[i].lim + (math.Max(0, det) - d[i].lim)*lpf2point4Hz
			if d[i].lim > headroom {
				d[i].lim *= hpf20Hz // to mitigate 'stuck' limiting following excessive input
			}
			out *= clipThr / (d[i].lim + clipThr)
			if d[i].lim > 0.06 { // indicate meaningful limiting only
				display.GRl = i+1
			}
			// +36dB artificial headroom, to block unbounded DC
			out = math.Max(-headroom, math.Min(headroom, out))
			sides += out * d[i].pan * 0.5
			mid += out * (1 - math.Abs(d[i].pan*0.5))
			sum += out
		}

		g += (gain - g)*lpf15Hz
		mid *= g
		sides *= g
		sum *= g
		hpfM = (hpfM + mid - xM) * hpf20Hz
		xM, mid = mid, hpfM
		hpfS = (hpfS + sides - xS) * hpf20Hz
		xS, sides = sides, hpfS
		// sidechain pre-emphasis
		hiBand = (hiBand + sum - hiBandPrev) * hiBandCoeff
		hiBandPrev = sum
		midBand = (midBand + sum - midBandPrev) * midBandCoeff
		midBandPrev = sum
		det := math.Abs(23*hiBand + 3.25*midBand + 0.55*sum) - Thr
		if det > lim { // limiter detection
			lim = det // peak detect
			h = 1
		}
		mid *= Thr / (lim + Thr) // VCA
		sides *= Thr / (lim + Thr)
		h *= hold
		lim *= release + 0.1 / (1/(0.01 + 1e3*h) + 0.1 / (1 - release))
		display.GR = lim > 0.06
		eqM = (eqM + mid - eqXM) * hpf320Hz
		eqXM = mid
		eqS = (eqS + sides - eqXS) * hpf320Hz
		eqXS = sides
		if eq { // high shelving boost
			mid = eqM * 2 + mid * 0.9
			sides = eqS * 2 + sides * 0.9
		}
		if exit {
			mid *= env // fade out
			sides *= env
			env -= fade // linear fade-out (perceived as logarithmic)
			if env < 0 {
				return
			}
		}
		dither = no.ise()
		dither += no.ise()
		dither *= 0.5
		//mid *= hroom
		mid += dither / math.MaxInt16 // set to fixed amount
		sides += dither / math.MaxInt16
		peak += (math.Abs(mid) - peak) * lpf2point4Hz // 'VU' style metering
		//if abs := math.Abs(mid); abs > peak { // peak detect metering
		//	peak = abs
		//}
		display.Vu = peak
		//peak -= 5e-5 // meter ballistics, linear (effectively logarithmic decay in dB)
		//if peak < 0 {
		//	peak = 0
		//}
		sides = math.Max(-0.5, math.Min(0.5, sides))
		if record {
			L := math.Max(-1, math.Min(1, mid+sides)) * math.MaxInt32
			R := math.Max(-1, math.Min(1, mid-sides)) * math.MaxInt32
			writeWav(L, R)
		}
		t = time.Since(lastTime)
		samples <- stereoPair{left: mid + sides, right: mid - sides, running: yes}
		lastTime = time.Now()
		rate += t
		rates[n%RateIntegrationTime] = t // rolling average buffer
		rate -= rates[(n+1)%RateIntegrationTime]
		if n%RateIntegrationTime == 0 {
			display.Load = rate / RateIntegrationTime
		}
		mid, sides, sum = 0, 0, 0
		n++
	}
}

func closeOutput(sampleRate float64) {
	samples <- stereoPair{running: not}
	for len(samples) > 0 { /* wait for samples to be received */ }
	// portaudio requires 4x buffer delay
	t := 4 * writeBufferLen * time.Second / time.Duration(sampleRate)
	time.Sleep(t) // wait for samples to be written
}

func releaseFrom(t, SR float64) float64 {
	return math.Pow(10, -4/(t*SR))
}

func octave(oct float64) float64 {
	return 20*math.Pow(2, oct) // 20hz root frequency
}

func lpf_coeff(f, SR float64) float64 {
	return 1 / (1 + 1/(Tau*f/SR))
}

func hpf_coeff(f, SR float64) float64 {
	return 1 / (1 + Tau*f/SR)
}

func clip(in float64) float64 { // hard clip
	if in > 1 {
		display.Clip = yes
		return 1
	}
	if in < -1 {
		display.Clip = yes
		return -1
	}
	return in
}

const width = 2 << 16 // precision of tanh table
var tanhTab = make([]float64, width)

/*
var sineTab = make([]float64, int(SampleRate))
func init() {
	for i := range sineTab {
		// using cosine, even function avoids negation for -ve x
		sineTab[i] = math.Cos(2 * math.Pi * float64(i) / SampleRate)
	}
}*/

func init() {
	for i := range tanhTab {
		tanhTab[i] = math.Tanh(float64(i) / width)
	}
}

const Tau = 2 * math.Pi

func sine(x float64) float64 {
	return math.Cos(Tau * x)
	/*
	   	if x < 0 {
	   		x = -x
	   	}

	   sr := int(SampleRate)
	   a := int(x * SampleRate)
	   sa := sineTab[a%sr]
	   sb := sineTab[(a+1)%sr]
	   xx := mod((x*SampleRate)-float64(a), SampleRate-1)
	   return sa + ((sb - sa) * xx) // linear interpolation
	*/
}

func tanh(x float64) float64 {
	if x < -1 || x > 1 {
		return math.Tanh(x)
	}
	neg := not
	if x < 0 {
		neg = yes
		x = -x
	}
	w := (width - 2.0) // slightly imprecise
	x *= w
	a := int(x)
	ta := tanhTab[a]
	tb := tanhTab[a+1]
	xx := x - float64(a)
	if neg {
		return -(ta + ((tb - ta) * xx))
	}
	return ta + ((tb - ta) * xx)
}

func (n *noise) ise() float64 {
	*n ^= *n << 13
	*n ^= *n >> 7
	*n ^= *n << 17
	return float64(*n)*twoInvMaxUint - 1
}

//var invMaxInt32 = 1.0 / math.MaxInt32

func mod(x, y float64) float64 {
	if y == 0 {
		return 0
		// return x ?
	}
	if y == 1 {
		_, f := math.Modf(x)
		return f
	}
	return math.Mod(x, y)
	/*
	   pos := yes

	   	if x < 0 {
	   		pos = not
	   		x = -x
	   	}

	   m := uint32(math.MaxInt32 * x / y)
	   //m %= uint32(MaxInt32*y) // dirty mod

	   	if pos {
	   		return float64(m) * invMaxInt32
	   	}

	   return -float64(m) * invMaxInt32
	*/
}

const (
	N     = 2 << 12       // fft window size
	N2    = N >> 1        // half fft window
	invN2 = 1.0 / N2      // scale factor
	N1    = 1.0 / (N - 1) // scale factor
)

func fft(y [N]complex128, s float64) [N]complex128 {
	var x [N]complex128
	for r, l := N2, 1; r > 0; r /= 2 {
		y, x = x, y
		ωi, ωr := math.Sincos(-s * math.Pi / float64(l))
		//ω := complex(math.Cos(-s * Pi / float64(l)), math.Sin(-s * Pi / float64(l)))
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

var sf = fmt.Sprintf

// msg sends a formatted string to info display
var msg = func(s string, i ...interface{}) {
	info <- fmt.Sprintf(s, i...)
	<-carryOn
}

// error handling
func e(rr error) bool {
	return rr != nil
}

// operatorCheck process field functions
// These must be of type processor func(systemState) (systemState, int)

func noCheck(s systemState) (systemState, int) {
	return s, nextOperation
}

func checkOut(s systemState) (systemState, int) {
	_, alreadyOut := s.out[s.operand]
	switch {
	case s.num.Is:
		return s, s.clr("output to number not permitted")
	case s.operand[:1] == "@":
		return s, s.clr("can't send to @, represents function operand")
	case isUppercaseInitialOrDefaultExported(s.operand):
		// TODO this needs to check for no prior `out` to same signal
		if _, exp := s.exportedSignals[s.operand]; !exp && s.operator ==  "out+" {
			msg("remember to reset %s with `out` once in the cycle", s.operand)
		}
		return s, nextOperation
	case s.operator == "out+" || s.operator == "out*":
		priorOut := not
		for _, o := range s.newListing {
			if o.Op == "out" && o.Opd == s.operand {
				priorOut = yes
			}
		}
		if !priorOut {
			msg("first instance changed to out")
			s.operator = "out"
		}
	case alreadyOut:
		return s, s.clr("%s: duplicate output to signal, c'est interdit", s.operand)
	}
	if s.operator == "out" && s.operand[:1] != "^" {
		// not in switch because s.operator may be changed above
		s.out[s.operand] = assigned
	}
	return s, nextOperation
}

func checkIn(s systemState) (systemState, int) {
	if s.num.Is || isUppercaseInitialOrDefaultExported(s.operand) {
		return s, nextOperation
	}
	priorOut := not
	for _, o := range s.newListing {
		if o.Op == "out" && o.Opd == s.operand {
			priorOut = yes
		}
	}
	if priorOut {
		return s, nextOperation
	}
	// false positives for wav rate
	return s, nextOperation
}

func endFunctionDefine(t systemState) (systemState, int) {
	if !t.fIn || len(t.newListing[t.st+1:]) < 1 {
		msg("no function definition")
		return t, startNewOperation
	}
	h := not
	for _, o := range t.newListing[t.st+1:] {
		if o.Opd == "@" { // set but don't reset
			h = yes
			break
		}
	}
	name := t.newListing[t.st].Opd
	t.hasOperand[name] = h
	t.funcs[name] = fn{Comment: t.funcs[name].Comment, Body: t.newListing[t.st+1:]}
	msg("function %q ready.", name)
	if !saveJson(t.funcs, funcsFile) {
		msg("function not saved!")
	} else {
		msg("function saved")
	}
	t.fIn = not
	return t, startNewListing
}

func checkPushPop(s systemState) (systemState, int) {
	p := 0
	for _, o := range s.newListing {
		if o.Op == "push" {
			p++
		}
		if o.Op == "pop" {
			p--
		}
	}
	if p <= 0 {
		msg("no push to pop")
		return s, startNewOperation
	}
	return s, nextOperation
}

func popPushParity(s systemState) bool {
	p := 0
	for _, o := range s.newListing {
		if o.Op == "push" {
			p++
		}
		if o.Op == "pop" {
			p--
		}
	}
	if p != 0 {
		// this will also catch pop without push, however that will have been checked previously 
		msg("push without pop")
		return false
	}
	return true
}

// the error messages could be improved to give more context
func multipleBuff(s systemState) bool {
	var bf bool
	var b [3]bool
	for l, o := range s.newListing {
		if o.Op == "buff" || o.Op == "buff0" {
			if !bf {
				bf = true
				continue
			}
			msg("%d: more than one buff", l)
			return true
		}
		for i := range b {
			if o.Op == fmt.Sprintf("buff%d", i+1) {
				if !b[i] {
					b[i] = true
					continue
				}
				msg("%d: more than one buff%d", l, i+1)
				return true
			}
		}
	}
	return false
}

func buffUnique(s systemState) (systemState, int) {
	for _, o := range s.newListing {
		if o.Op == "buff" || o.Op == "buff0" {
			msg("only one buff per listing")
			return s, startNewOperation
		}
	}
	return s, nextOperation
}

func parseIndex(s listingState, l int) (int, bool) {
	if l < 0 {
		msg("nothing to %s", s.operator)
		return 0, not
	}
	if s.operand == "" { // ignore checks for empty operands, iffy?
		return 0, yes
	}
	n, rr := strconv.Atoi(s.operand)
	if e(rr) {
		msg("%s is not an integer", s.operand)
		return 0, not
	}
	if n < 0 || n > l {
		msg("%s out of range", s.operand)
		return 0, not
	}
	return n, yes
}

func excludeCurrent(op string, i, l int) bool {
	if i > l-1 {
		msg("can't %s current or non-extant listing: %d", op, l)
		return yes
	}
	return not
}

func eraseOperations(s systemState) (systemState, int) {
	n, ok := parseIndex(s.listingState, len(s.dispListing))
	if !ok {
		return s, startNewOperation // error reported by parseIndex
	}
	for i := 0; i < len(s.dispListing)-n; i++ { // recompile
		tokens <- token{s.dispListing[i].Op, -1, yes}
		if len(s.dispListing[i].Opd) > 0 { // dodgy?
			tokens <- token{s.dispListing[i].Opd, -1, yes}
		}
	}
	tokens <- token{"", -1, not}
	return s, startNewListing
}

func checkWav(s systemState) (systemState, int) {
	if s.wmap[s.operand] || (s.operand == "@" && s.fIn) {
		return s, nextOperation
	}
	return s, s.clr("%q isn't in wav list", s.operand)
}

func enactMute(s systemState) (systemState, int) {
	i, ok := parseIndex(s.listingState, len(mutes))
	if !ok || excludeCurrent(s.operator, i, len(mutes)) {
		return s, startNewOperation // error reported by parseIndex
	}
	if s.operator == "m+" || s.operator == "n" {
		s.muteGroup = append(s.muteGroup, i) // add to mute group
		return s, startNewOperation
	}
	s.muteGroup = append(s.muteGroup, i)
	for _, i := range s.muteGroup {
		mutes.set(i, 1-mutes[i])          // toggle
		s.unsolo[i] = mutes[i]            // save status for unsolo
		if s.solo == i && mutes[i] == 0 { // if muting solo'd listing reset solo
			s.solo = -1
		}
	}
	if s.operator[:1] == "." && len(s.newListing) > 0 {
		tokens <- token{"mix", -1, not}
	}
	s.muteGroup = []int{}
	return s, startNewOperation
}

func enactSolo(s systemState) (systemState, int) {
	i, ok := parseIndex(s.listingState, len(mutes))
	if !ok {
		return s, startNewOperation // error reported by parseIndex
	}
	if i == len(mutes) && s.operator != ".solo" {
		msg("operand out of range")
		return s, startNewOperation
	}
	if s.solo == i { // unsolo index given by operand
		for ii := range mutes { // i is shadowed
			if i == ii {
				continue
			}
			mutes.set(ii, s.unsolo[ii]*(1-mutes[ii])) // restore all other mutes
		}
		for _, i := range s.muteGroup {
			mutes.set(i, unmute) // unmute from above
		}
		s.muteGroup = []int{}
		s.solo = -1 // unset solo index
	} else { // solo index given by operand
		for ii := range mutes {
			if ii == i {
				mutes.set(i, unmute) // unmute solo'd index
				continue
			}
			s.unsolo[ii] = mutes[ii] // save all mutes
			mutes.set(ii, mute)      // mute all other listings
		}
		for _, i := range s.muteGroup {
			mutes.set(i, unmute) // unmute from above
		}
		s.muteGroup = []int{}
		s.solo = i // save index of solo
	}
	if s.operator[:1] == "." && len(s.newListing) > 0 {
		tokens <- token{"mix", -1, not}
	}
	return s, startNewOperation
}

func beginFunctionDefine(s systemState) (systemState, int) {
	if _, ok := s.funcs[s.operand]; ok {
		msg("will overwrite existing function!")
	} else if _, ok := s.hasOperand[s.operand]; ok { // using this map to avoid cyclic reference of operators
		msg("duplicate of extant operator, use another name")
		return s, startNewOperation // only return early if not a function and in hasOperand map
	}
	s.st = len(s.newListing) // because current input hasn't been added yet
	s.fIn = yes
	msg("begin function definition,")
	msg("use @ for operand signal")
	return s, nextOperation
}

func doLoop(s systemState) (systemState, int) {
	var rr error
	s.do, rr = strconv.Atoi(s.operand)
	if e(rr) { // returns do as zero
		msg("operand not an integer")
		return s, startNewOperation
	}
	msg("next operation repeated %dx", s.do)
	s.to = s.do
	return s, startNewOperation
}

func modeSet(s systemState) (systemState, int) {
	if s.operand == "p" { // toggle pause/play
		switch {
		case display.Paused:
			s.operand = "play"
		default:
			s.operand = "pause"
		}
	}
	switch s.operand {
	case "exit", "q":
		p("\nexiting...")
		exit = yes
		if display.Paused {
			<-pause
		}
		if started {
			<-stop // received when shutdown complete
		} else {
			close(stop)
		}
		saveJson([]listing{{operation{Op: advisory}}}, listingsFile)
		p("Stopped")
		time.Sleep(30 * time.Millisecond) // wait for infoDisplay to finish
		return s, exitNow
	case "erase", "e":
		return s, startNewListing
	case "pause":
		if started && !display.Paused {
			pause <- yes // bool is purely semantic
			display.Paused = yes
		}
	case "play":
		if !display.Paused {
			return s, startNewOperation
		}
		<-pause
		display.Paused = not
	case "verbose":
		switch display.Verbose {
		case not:
			if !saveJson(s.verbose, listingsFile) {
				msg("listing display not updated, check %q exists", listingsFile)
			}
		case yes:
			if !saveJson(s.dispListings, listingsFile) {
				msg("listing display not updated, check %q exists", listingsFile)
			}
		}
		display.Verbose = !display.Verbose
	case "stats":
		if !started {
			return s, startNewOperation
		}
		stats := new(debug.GCStats)
		debug.ReadGCStats(stats)
		msg("___GC statistics:___")
		msg("No.: %v", stats.NumGC)
		msg("Tot.: %v", stats.PauseTotal)
		msg("Avg.: %v", stats.PauseTotal/time.Duration(stats.NumGC))
	case "mstat":
		if !started {
			return s, startNewOperation
		}
		stats := new(runtime.MemStats)
		runtime.ReadMemStats(stats)
		msg("___Mem Stats:___")
		msg("Alloc: %v", stats.Alloc)
		msg("Sys: %v", stats.Sys)
		msg("Live: %v", stats.Mallocs-stats.Frees)
	case "mc": // mouse curve, exp or lin
		mouse.mc = !mouse.mc
	case "rs": // root sync, is this needed any more?
		rs = yes
		msg("next launch will sync to root instance")
	case "reset", "r":
		rst = !rst
		s := "off"
		if rst {
			s = "on"
		}
		msg("reset: %s", s)
	case "eq":
		eq = !eq
		s := "off"
		if eq {
			s = "on"
		}
		msg("eq: %s", s)
	default:
		msg("unrecognised mode: %s", s.operand)
	}
	return s, startNewOperation
}

func enactDelete(s systemState) (systemState, int) {
	n, ok := parseIndex(s.listingState, len(s.dispListings))
	if !ok || excludeCurrent(s.operator, n, len(s.dispListings)) {
		return s, startNewOperation // error reported by parseIndex
	}
	mutes.set(n, mute)  // wintermute
	if display.Paused { // play resumed to enact mute
		<-pause
		display.Paused = not
	}
	time.Sleep(150 * time.Millisecond) // wait for envelope to complete
	if s.operator == ".del" {
		tokens <- token{"mix", -1, not}
	}
	// reload as deleted
	s.reload = n
	tokens <- token{"deleted", s.reload, yes}
	return s, startNewListing
}

func checkIndexIncl(s systemState) (systemState, int) { // eg. listing can level or pan itself
	if _, ok := parseIndex(s.listingState, len(s.dispListings)); !ok {
		return s, startNewOperation // error reported by parseIndex
	}
	return s, nextOperation
}

func checkIndex(s systemState) (systemState, int) {
	i, ok := parseIndex(s.listingState, len(s.dispListings))
	if !ok || excludeCurrent(s.operator, i, len(s.dispListings)) {
		return s, startNewOperation // error reported by parseIndex
	}
	return s, nextOperation
}

func unmuteAll(s systemState) (systemState, int) {
	for i := range mutes {
		mutes.set(i, unmute)
	}
	return s, startNewOperation
}

func parseFloat(num number, lowerBound, upperBound float64) (v float64, ok bool) {
	if !num.Is {
		msg("not a number")
		return 0, not
	}
	v = num.Ber
	if v < lowerBound {
		v = lowerBound
	}
	if v > upperBound {
		v = upperBound
	}
	return v, yes
}
func reportFloatSet(op string, f float64) {
	if f > 1/SampleRate {
		msg("%s set to %.3gms", op, 1e3/(f*SampleRate)) //TODO
		return
	}
	msg("%s set to %.3gs", op, 1/(f*SampleRate)) //TODO
}

func checkFade(s systemState) (systemState, int) {
	fd, ok := parseFloat(s.num, 1/(MAX_FADE*s.sampleRate), 1/(MIN_FADE*s.sampleRate))
	if !ok { // error reported by parseFloat
		return s, startNewOperation
	}
	fade = fd
	reportFloatSet(s.operator, fd)
	return s, startNewOperation
}

func checkRelease(s systemState) (systemState, int) {
	if s.operand == "time" || s.operand == "is" {
		msg("limiter release is: %.4gms",
		// inverse of: math.Pow(10, -4/(t*SR)) from func releaseFrom()
		1000 * ((-4 / math.Log10(release)) / s.sampleRate))
		return s, startNewOperation
	}
	v, ok := parseFloat(s.num, 1/(MAX_RELEASE*s.sampleRate), 1/(MIN_RELEASE*s.sampleRate))
	if !ok { // error reported by parseFloat
		return s, startNewOperation
	}
	//release = math.Pow(125e-6, v)
	release = releaseFrom(1/(v*s.sampleRate), s.sampleRate)
	reportFloatSet("limiter "+s.operator, v) // report embellished
	return s, startNewOperation
}

func adjustGain(s systemState) (systemState, int) {
	if s.operand == "zero" {
		gain = baseGain
		msg("gain set to %.2gdb", 20*math.Log10(gain/baseGain))
		return s, startNewOperation
	}
	if s.operand == "is" {
		msg("gain set to %.2gdb", 20*math.Log10(gain/baseGain))
		return s, startNewOperation
	}
	n, ok := parseType(s.operand, s.operator)
	if !ok {
		return s, startNewOperation
	}
	gain *= math.Abs(n)
	if math.Abs(math.Log10(gain/baseGain)) < 1e-12 { // if log(1) ≈ 0 to 1
		gain = baseGain
	}
	if gain < 0.05*baseGain { // lower bound ~ -26db
		gain = 0.05 * baseGain
	}
	msg("gain set to %.2gdb", 20*math.Log10(gain/baseGain))
	return s, startNewOperation
}

func adjustClip(s systemState) (systemState, int) {
	if n, ok := parseType(s.operand, s.operator); ok { // permissive, no bounds check
		clipThr = n
		msg("clip threshold set to %.3g", clipThr)
	}
	return s, startNewOperation
}

func checkComment(s systemState) (systemState, int) {
	if len(s.newListing) > 0 {
		msg("a comment has to be the first and only operation of a listing...")
		return s, startNewOperation
	}
	return s, nextOperation
}

func checkAlp(s systemState) (systemState, int) {
	for _, o := range s.newListing {
		if o.Op == "4lp" {
			msg("using more than one %s4lp%s in a listing may cause instability")
			break
		}
	}
	return s, nextOperation
}

func enactWait(s systemState) (systemState, int) {
	if t, ok := parseType(s.operand, s.operator); ok { // permissive, no bounds check
		pf("waiting...\n\t")
		time.Sleep(time.Second * 1e3 / time.Duration(t * s.sampleRate * 1e3))
	}
	return s, startNewOperation
}

func isUppercaseInitialOrDefaultExported(operand string) bool {
	switch operand {
	case "dac", "tempo", "pitch", "grid", "sync": // needs to include wav signals
		return yes
	}
	return isUppercaseInitial(operand)
}

func isUppercaseInitial(operand string) bool {
	if operand == "" {
		return not
	}
	switch {
	case len(operand) == 1:
		return unicode.IsUpper([]rune(operand)[0])
	case operand[:1] == "'" || operand[:1] == "^" || operand[:1] == "\"":
		return unicode.IsUpper([]rune(operand)[1])
	case len(operand) == 2:
		return unicode.IsUpper([]rune(operand)[0])
	case (operand[1:2] == "'" || operand[1:2] == "\"") && operand[:1] == "^":
		return unicode.IsUpper([]rune(operand)[2])
	}
	return unicode.IsUpper([]rune(operand)[0])
}

func newSystemState(sc soundcard) (systemState, [][]float64, wavs) {
	t := systemState{
		soundcard:       sc,
		funcs:           make(map[string]fn),
		daisyChains:     []int{2, 3, 9, 10}, // pitch,tempo,grid,sync
		solo:            -1,
		exportedSignals: map[string]int{},
	}

	loadFunctions(&t.funcs)
	t.hasOperand = make(map[string]bool, len(operators)+len(t.funcs))
	for k, o := range operators {
		t.hasOperand[k] = o.Opd
	}
	for k, f := range t.funcs {
		h := not
		for _, o := range f.Body {
			if o.Opd == "@" { // set but don't reset
				h = yes
				break
			}
		}
		t.hasOperand[k] = h
	}

	// process wavs
	wavSlice := decodeWavs()
	wavs := make([][]float64, 0, len(wavSlice))
	t.wmap = map[string]bool{}
	for _, w := range wavSlice {
		t.wavNames += w.Name + " "
		t.wmap[w.Name] = yes
		wavs = append(wavs, w.Data)
	}

	return t, wavs, wavSlice
}

func initialiseListing(t systemState) systemState {
	t.listingState = listingState{}
	t.newSignals = make([]float64, 0, 30) // capacity is nominal
	t.signals = make(map[string]int, 30)
	t.out = make(map[string]struct{}, 30) // arbitrary capacity
	// set-up state
	res := [lenReserved]string{
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
		"sync",
	}
	for _, name := range res {
		t.createListing = addSignal(t.createListing, name, 0)
		t.out[name] = assigned
	}
	// clear space for exported signals
	t.newSignals = append(t.newSignals, make([]float64, maxExports)...)
	preDefined := []struct{
		name string
		val  float64
	}{
		{"ln2", math.Ln2},
		{"ln3", math.Log(3)},
		{"ln5", math.Log(5)},
		{"E", math.E},     // e
		{"Pi", math.Pi},   // π
		{"Phi", math.Phi}, // φ
		{"invSR", 1 / t.sampleRate},
		{"SR", t.sampleRate},
		{"Epsilon", math.SmallestNonzeroFloat64}, // ε, epsilon
		{"wavR", 1.0 / (WAV_TIME * t.sampleRate)},
		{"semitone", math.Pow(2, 1.0/12)},
		{"Tau", 2 * math.Pi}, // 2π
		{"ln7", math.Log(7)},
		{"^freq", DEFAULT_FREQ},           // for setmix
		{"fifth", math.Pow(2, 7.0/12)},    // equal temperament ≈ 1.5 (2:3)
		{"third", math.Pow(2, 1.0/3)},     // major, equal temperament ≈ 1.25 (4:5)
		{"seventh", math.Pow(2, 11.0/12)}, // major, equal temperament ≈ 1.875 (8:15)
	}
	for _, p := range preDefined {
		t.createListing = addSignal(t.createListing, p.name, p.val)
	}
	t.tapeLen = TAPE_LENGTH * int(t.sampleRate)
	t.reload = -1
	return t
}

func addSignal(t createListing, name string, val float64) createListing {
	if _, in := t.signals[name]; in {
		return t
	}
	t.newSignals = append(t.newSignals, val)
	t.signals[name] = len(t.newSignals)-1
	//infoIfLogging("addSignal: index=%s val=%f,   index=%d", name, val, t.signals[name])
	return t
}

const debugFormat = `
reload: %d
list: %s
stack: %v
syncSt8: %v
m: %f
sigs: %s
level: %f
pan: %f
peakfreq: %f
lim %f
`
func coreDump(d listingStack, name string) {
	if !writeLog && name != "panicked_listing" {
		return
	}
	sg := ""
	for i, f := range d.sigs {
		sg += sf("%d: %f   ", i, f)
	}
	b := sf(debugFormat,
		d.reload,
		d.listing,
		d.stack,
		d.syncSt8,
		d.m,
		sg,
		d.lv,
		d.pan,
		d.peakfreq,
		d.lim,
	)
	if writeLog {
		msg(b)
		return
	}
	if !save([]byte(b), sf("debug/%s_%s.txt", time.Now(), name)) {
		f := sf(".%s_%s.txt", time.Now(), name)
		if save([]byte(b), f) {
			msg("debug info saved as: %q", f)
		}
	}
}

func (o opSE) String() string {
	for operator, op := range operators {
		if int(o.Opn) == op.N {
			p := "."
			if o.P {
				p = "P"
			}
			if o.Opn == 0 {
				operator = "// or deleted"
			}
			return sf("%s ?_%d %s, ", operator, /*o.Opd,*/ o.N, p)
		}
	}
	return sf("%d %d, ", o.Opn, o.N)
}

func interpolatedBuffer(buff []float64, sig, r float64, n, tapeLen int) float64 {
	buff[n%tapeLen] = r // record head

	return interpolatedTap(buff, sig, n, tapeLen)
}

func interpolatedTap(buff []float64, sig float64, n, tapeLen int) float64 {
	t := 0.0
	if sig != 0 {
		t = 1 / sig
	}
	x := mod(float64(n+tapeLen)-t, float64(tapeLen)) // cound also be len(buff)
	return interpolation(buff, x)
}

func interpolation(buff []float64, x float64) float64 {
	l := len(buff)
	x = math.Max(0, math.Min(float64(l), math.Abs(x))) // bounds enforcing
	i := int(x) // effective floor()
	x0 := buff[(i+l-2)%l] // these two wrap around
	x1 := buff[(i+l-1)%l]
	x2 := buff[i%l]
	x3 := buff[(i+1)%l]
	x4 := buff[(i+2)%l]
	x5 := buff[(i+3)%l]
	x6 := buff[(i+4)%l]
	// half-band sinc, 13-tap
	y0 := x2
	y1 := a5*(x0+x5) + a3*(x1+x4) + a1*(x2+x3)
	y2 := x3
	y3 := a5*(x1+x6) + a3*(x2+x5) + a1*(x3+x4)
	z := x - math.Floor(x) - 0.5
	// 4-point 4th order "optimal" interpolation filter by Olli Niemitalo
	ev1, od1 := y2+y1, y2-y1
	ev2, od2 := y3+y0, y3-y0
	c0 := ev1*0.45645918406487612 + ev2*0.04354173901996461
	c1 := od1*0.47236675362442071 + od2*0.17686613581136501
	c2 := ev1*-0.253674794204558521 + ev2*0.25371918651882464
	c3 := od1*-0.37917091811631082 + od2*0.11952965967158
	c4 := ev1*0.04252164479749607 + ev2*-0.04289144034653719
	return (((c4*z+c3)*z+c2)*z+c1)*z + c0
}

var ( // these could be hard-coded as constants
	a1 = sincCoeff(1.0)
	a3 = sincCoeff(3.0)
	a5 = sincCoeff(5.0)
)

func sincCoeff(x float64) float64 {
	return math.Sin(0.5*x*math.Pi)/(math.Pi*x)  * (math.Cos(x*math.Pi/6)+1)
}

func init() {
	norm := 1.0 / (2*(a1+a3+a5))
	a1 *= norm
	a3 *= norm
	a5 *= norm
}
