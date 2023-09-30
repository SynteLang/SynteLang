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

// There are 7 goroutines (aside from main), they are:
// go SoundEngine(), blocks on write to soundcard input buffer, shutdown with ": exit"
// go infoDisplay(), timed slowly at > 20ms, explicitly returned from on exit
// go mouseRead(), blocks on mouse input, rechecks approx 20 samples later (at 48kHz)
// go func(), anonymous restart watchdog, waits on close of stop channel
// go readInput(), scan stdin from goroutine to allow external concurrent input, blocks on stdin
// go reloadListing(), poll '.temp/*.syt' modified time and reload if changed, timed slowly at > 84ms
// go func(), anonymous, handles writing to soundcard within SoundEngine(), blocks on write to soundcard

package main

import (
	"bufio"
	"fmt"
	. "math" // don't do this!
	"math/cmplx"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
	"unicode"
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
	NOISE_FREQ     = 0.0625 // 3kHz @ 48kHz Sample rate
	FDOUT          = 1e-4
	MIN_FADE       = 175e-3 // 175ms
	MAX_FADE       = 120    // 120s
	MIN_RELEASE    = 50e-3  // 50ms
	MAX_RELEASE    = 50     // 50s
	twoInvMaxUint = 2.0 / MaxUint64
)

var (
	convFactor         = float64(MaxInt16) // checked below
	SampleRate float64 = SAMPLE_RATE
	TLlen      int     = SAMPLE_RATE * TAPE_LENGTH
)

// terminal colours, eg. sf("%stest%s test", yellow, reset)
const (
	reset   = "\x1b[0m"
	italic  = "\x1b[3m"
	red     = "\x1b[31m"
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
}
type listing []operation

type createListing struct {
	newListing  listing
	dispListing listing
	newSignals  []float64
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
	out map[string]struct{}
	clr clear
	newOperation
	fIn bool // yes = inside function definition
	st, // func def start
	fun, // don't worry the fun will increase!
	do, to int
	muteGroup []int // new mute group
}

type fn struct {
	Comment string
	Body    listing
}

type systemState struct {
	dispListings []listing
	wmap         map[string]bool
	funcs        map[string]fn
	funcsave     bool
	code         *[]listing
	solo         int
	unsolo       muteSlice
	listingState
	hasOperand map[string]bool
	sc         soundcard
}

type processor func(*systemState) int

type operatorParticulars struct {
	Opd     bool // indicates if has operand
	N       int  // index for sound engine switch
	process processor
}

var operators = map[string]operatorParticulars{ // would be nice if switch indexes could be generated from a common root
	//name  operand N  process           comment
	"+":      {yes, 1, noCheck},         // add
	"out":    {yes, 2, checkOut},        // send to named signal
	".out":   {yes, 2, checkOut},        // alias of out
	">":      {yes, 2, checkOut},        // alias of out
	"out+":   {yes, 3, checkOut},        // add to named signal
	">+":     {yes, 3, checkOut},        // alias of out+
	"in":     {yes, 4, noCheck},         // input numerical value or receive from named signal
	"<":      {yes, 4, noCheck},         // alias of in
	"sine":   {not, 5, noCheck},         // shape linear input to sine
	"mod":    {yes, 6, noCheck},         // output = input MOD operand
	"gt":     {yes, 7, noCheck},         // greater than
	"lt":     {yes, 8, noCheck},         // less than
	"mul":    {yes, 9, noCheck},         // multiply
	"*":      {yes, 9, noCheck},         // alias of mul
	"x":      {yes, 9, noCheck},         // alias of mul
	"abs":    {not, 10, noCheck},        // absolute
	"tanh":   {not, 11, noCheck},        // hyperbolic tangent
	"pow":    {yes, 12, noCheck},        // power
	"base":   {yes, 13, noCheck},        // operand to the power of input
	"clip":   {yes, 14, noCheck},        // clip input
	"noise":  {not, 15, setNoiseFreq},   // white noise source
	"push":   {not, 16, noCheck},        // push to listing stack
	"pop":    {not, 17, checkPushPop},   // pop from listing stack
	"(":      {not, 16, noCheck},        // alias of push
	")":      {not, 17, noCheck},        // alias of pop
	"tape":   {yes, 18, tapeUnique},     // listing tape loop
	"--":     {yes, 19, noCheck},        // subtract from operand
	"tap":    {yes, 20, noCheck},        // tap from loop
	"f2c":    {not, 21, noCheck},        // convert frequency to co-efficient
	"wav":    {yes, 22, checkWav},       // play wav file
	"8bit":   {yes, 23, noCheck},        // quantise input
	"index":  {not, 24, noCheck},        // index of listing // change to signal?
	"<sync":  {yes, 25, noCheck},        // receive sync pulse
	">sync":  {not, 26, noCheck},        // send sync pulse
	".>sync": {not, 26, noCheck},        // alias, launches listing
	"jl0":    {yes, 27, noCheck},        // jump if less than zero
	"level":  {yes, 28, checkIndexIncl}, // vary level of a listing
	".level": {yes, 28, checkIndexIncl}, // alias, launches listing
	"from":   {yes, 29, checkIndex},     // receive output from a listing
	"sgn":    {not, 30, noCheck},        // sign of input
	//	"deleted":      {yes, 31, noCheck}, // specified below
	"/":      {yes, 32, noCheck},        // division
	"sub":    {yes, 33, noCheck},        // subtract operand
	"-":      {yes, 33, noCheck},        // alias of sub
	"setmix": {yes, 34, noCheck},        // set sensible level
	"print":  {not, 35, noCheck},        // print input to info display
	"\\":     {yes, 36, noCheck},        // "\"
	"pan":    {yes, 38, checkIndexIncl}, // vary pan of a listing
	".pan":   {yes, 38, checkIndexIncl}, // alias, launches listing
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

	// specials
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
	"rpl":     {yes, 0, enactRpl},            // replace a listing
	".rpl":    {yes, 0, enactRpl},            // launch listing in place of another
	"s":       {yes, 0, enactSolo},           // alias of solo
	"e":       {yes, 0, eraseOperations},     // alias of erase
	"apd":     {yes, 0, loadReloadAppend},    // launch index to new listing
	"do":      {yes, 0, doLoop},              // repeat next operation [operand] times
	"d":       {yes, 0, enactDelete},         // alias of del
	"deleted": {not, 0, noCheck},             // for internal use
	"/*":      {yes, 0, noCheck},             // non-breaking comments, nop
	"m+":      {yes, 0, enactMute},           // add to mute group
	"gain":    {yes, 0, adjustGain},          // set overall mono gain before limiter
	"record":  {yes, 0, recordWav},           // commence recording of wav file
}

var transfer struct { // make this a slice of structs?
	Listing []listing
	Signals [][]float64
	Wavs    [][]float64 // sample
}

// communication channels
var (
	stop     = make(chan struct{}) // confirm on close()
	pause    = make(chan bool)     // bool is purely semantic
	transmit = make(chan bool)
	accepted = make(chan bool)

	info    = make(chan string, 96) // arbitrary buffer length, 48000Hz = 960 x 50Hz
	carryOn = make(chan bool)
	infoff  = make(chan struct{}) // shut-off info display (and external input)
)

type muteSlice []float64

// communication variables
var (
	started bool      // latches
	exit    bool      // initiate shutdown
	mutes   muteSlice // move to transfer struct?
	level   []float64 // move to transfer struct?
	reload  = -1
	ds,
	rs bool // root-sync between running instances
	daisyChains []int // list of exported signals to be daisy-chained
	fade        = 1/(MIN_FADE*SAMPLE_RATE) //Pow(FDOUT, 1/(MIN_FADE*SAMPLE_RATE))
	release     = Pow(8000, -1.0/(0.5*SAMPLE_RATE)) // 500ms
	ct          = 2.0                               // individual listing clip threshold
	gain        = 1.0
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
var mc = yes // mouse curve: not=linear, yes=exponential

type disp struct { // indicates:
	On      bool          // Syntə is running
	Mode    string        // func add fon/foff
	Vu      float64       // output sound level
	Clip    bool          // sound engine has clipped on output
	Load    time.Duration // sound engine loop time used
	Info    string        // messages sent from msg()
	MouseX  float64       // mouse X coordinate
	MouseY  float64       // mouse Y coordinate
	Protect bool          // redundant
	Paused  bool          // sound engine is paused
	Mute    []bool        // mutes of all listings
	SR      float64       // current sample rate (not shown)
	GR      bool          // limiter is in effect
	Sync    bool          // sync pulse sent
	Verbose bool          // show unrolled functions - all operations
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

type token struct {
	tk     string
	reload int
	ext    bool
}

var (
	tokens = make(chan token, 2<<12) // arbitrary capacity, will block input in extreme circumstances
	lockLoad = make(chan struct{}, 1) // mutex on transferring listings
)

var rpl = -1 // synchronised to 'reload' at new listing start and if error, hacky global

const ( // used in token parsing
	startNewOperation = iota
	startNewListing
	exitNow
	nextOperation
)

type clear func(s string, i ...interface{}) int

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
	startProfiling(not)
	saveJson([]listing{{operation{Op: advisory}}}, "displaylisting.json")

	sc, success := setupSoundCard("/dev/dsp")
	if !success {
		p("unable to setup soundcard")
		sc.file.Close()
		return
	}
	defer sc.file.Close()
	SampleRate, convFactor = sc.sampleRate, sc.convFactor // change later

	t := systemState{sc: sc} // s yorks

	go infoDisplay()
	go SoundEngine(sc.file, sc.format)
	go mouseRead()

	// process wavs
	wavSlice := decodeWavs()
	transfer.Wavs = make([][]float64, 0, len(wavSlice))
	t.wmap = map[string]bool{}
	wavNames := ""
	for _, w := range wavSlice {
		wavNames += w.Name + " "
		t.wmap[w.Name] = yes
		transfer.Wavs = append(transfer.Wavs, w.Data)
	}

	restart := not     // controls whether listing is saved to temp on launch

	go func() { // watchdog, anonymous to use variables in scope
		// This function will restart the sound engine in the event of a runtime panic
		for {
			<-stop // wait until stop channel closed
			if exit { // don't restart if legitimate exit
				return
			}
			stop = make(chan struct{})
			go SoundEngine(sc.file, sc.format)
			lockLoad <- struct{}{}
			for len(tokens) > 0 { // empty incoming tokens
				<-tokens
			}
			tokens <- token{"_", -1, yes}                // hack to restart input
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
					tokens <- token{"deleted", -1, yes}
					tokens <- token{"out", -1, yes}
					tokens <- token{"dac", -1, yes}
					continue
				}
				for s.Scan() { // listings dumped into tokens chan
					tokens <- token{s.Text(), -1, yes} // tokens could block here, theoretically
				}
				inputF.Close()
			}
			transfer.Listing = nil
			transfer.Signals = nil
			t.dispListings = nil
			transmit <- yes
			<-accepted
			restart = yes // don't save temp files
			<-lockLoad
			msg("%s>>> Sound Engine restarted%s", italic, reset)
		}
	}()

	go readInput()     // scan stdin from goroutine to allow external concurrent input
	go reloadListing() // poll '.temp/*.syt' modified time and reload if changed

	// set-up state
	reservedSignalNames := []string{ // order is important
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
	lenReserved := len(reservedSignalNames) // use this as starting point for exported signals
	daisyChains = []int{2, 3, 9, 10}        // pitch,tempo,grid,sync
	for i := 0; i < EXPORTED_LIMIT; i++ {   // add 12 reserved signals for inter-list signals
		reservedSignalNames = append(reservedSignalNames, sf("***%d", i+lenReserved)) // placeholder
	}
	lenExported := 0
	t.funcs = make(map[string]fn)
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
	usage := loadUsage() // local usage telemetry
	ext := not           // loading external listing state

	t.code = &t.dispListings // code sent to listings.go
	t.solo = -1              // index of most recent solo

start:
	for { // main loop
		t.listingState = listingState{}
		t.newSignals = make([]float64, len(reservedSignalNames), 30) // capacity is nominal
		// signals map with predefined constants, mutable
		signals := map[string]float64{ // reset and add predefined signals
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
			"^freq":    NOISE_FREQ,      // default frequency for setmix, suitable for noise
			"null":     0,               // only necessary if zero is banned in Syntə again
			"fifth":    Pow(2, 7.0/12),  // equal temperament ≈ 1.5 (2:3)
			"third":    Pow(2, 1.0/3),   // major, equal temperament ≈ 1.25 (4:5)
			"seventh":  Pow(2, 11.0/12), // major, equal temperament ≈ 1.875 (8:15)
		}
		for i, w := range wavSlice { // add to signals map, with current sample rate
			signals[w.Name] = float64(i)
			signals["l."+w.Name] = float64(len(w.Data)-1) / (WAV_TIME * SampleRate)
			signals["r."+w.Name] = 1.0 / float64(len(w.Data))
		}
		TLlen = int(SampleRate * TAPE_LENGTH) // necessary?
		t.out = make(map[string]struct{}, 30) // to check for multiple outs to same signal name
		for _, v := range reservedSignalNames {
			switch v {
			case "tempo", "pitch", "grid", "sync":
				continue
			}
			if isUppercaseInitial(v) {
				continue
			}
			t.out[v] = assigned
		}
		reload = -1 // index to be launched to
		rpl = reload
		// the purpose of clr is to reset the input if error while receiving tokens from external source
		t.clr = func(s string, i ...interface{}) int { // must be type: clear
			for len(tokens) > 0 { // empty remainder of incoming tokens and abandon reload
				<-tokens
			}
			rpl = reload
			info <- fmt.Sprintf(s, i...)
			<-carryOn
			if ext {
				return startNewListing
			}
			return startNewOperation
		}

	input:
		for { // input loop
			t.newOperation = newOperation{}
			if len(tokens) == 0 {
				// Not strictly correct. No files saved to temp while restart is true,
				// tokens will be empty at completion of restart unless received from stdin within
				// time taken to compile and launch. This is not critical as restart only controls file save.
				restart = not
			}
			if !ext {
				displayHeader(sc, wavNames, t)
			}
			var result int
			switch reload, ext, result = readTokenPair(&t); result {
			case startNewOperation:
				continue input
			case startNewListing:
				ext = not
				continue start
			}

			for t.do > 1 { // one done below
				tokens <- token{t.operator, -1, not}
				d := strings.ReplaceAll(t.operand, "{i}", sf("%d", t.to-t.do+1))
				d = strings.ReplaceAll(d, "{i+1}", sf("%d", t.to-t.do+2))
				if y := t.hasOperand[t.operator]; y { // to avoid weird blank opds being sent
					tokens <- token{d, -1, not}
				}
				t.do--
			}
			t.operand = strings.ReplaceAll(t.operand, "{i}", sf("%d", 0))
			t.operand = strings.ReplaceAll(t.operand, "{i+1}", sf("%d", 1))

			if t.isFunction {
				function, ok := parseFunction(t, signals, t.out)
				switch {
				case !ok && !ext:
					continue input
				case !ok && ext:
					ext = not
					continue start
				}
				t.fun++
				t.newListing = append(t.newListing, function...)
			} else {
				switch r := operators[t.operator].process(&t); r {
				case startNewOperation:
					continue input
				case startNewListing:
					continue start
				case exitNow:
					break start
				}
			}

			// process exported signals
			alreadyIn := not
			for _, v := range reservedSignalNames {
				if v == t.operand {
					alreadyIn = yes // signal already exported or reserved
				}
			}
			_, inSg := signals[t.operand]
			if !inSg && !alreadyIn && !t.num.Is && !t.fIn && t.operator != "//" && isUppercaseInitial(t.operand) {
				if lenExported > EXPORTED_LIMIT {
					msg("we've ran out of exported signals :(")
					continue
				}
				reservedSignalNames[lenReserved+lenExported] = t.operand
				daisyChains = append(daisyChains, lenReserved+lenExported)
				lenExported++
				msg("%s%s added to exported signals%s", t.operand, italic, reset)
			}

			// add to listing
			t.dispListing = append(t.dispListing, operation{Op: t.operator, Opd: t.operand})
			usage[t.operator] += 1
			if !t.isFunction {
				t.newListing = append(t.newListing, operation{Op: t.operator, Opd: t.operand})
			}
			if t.fIn {
				continue
			}
			// break and launch
			switch o := t.newListing[len(t.newListing)-1]; o.Op {
			case "out", ">":
				if o.Opd == "dac" {
					break input
				}
			case ".out", ".>sync", ".level", ".pan": // override mutes and levels below, for silent listings
				if reload < 0 || reload > len(transfer.Listing)-1 {
					mutes = append(mutes, 0)
					t.unsolo = append(t.unsolo, 0)
					display.Mute = append(display.Mute, yes)
					level = append(level, 1)
				} else {
					t.unsolo[reload] = 0
					mutes.set(reload, mute)
				}
				break input
			case "//":
				break input
			}
			if !ext {
				msg(" ")
			}
		}
		// end of input

		for _, o := range t.newListing {
			if _, in := signals[o.Opd]; in || len(o.Opd) == 0 {
				continue
			}
			if strings.ContainsAny(o.Opd[:1], "+-.0123456789") { // wavs already in signals map
				signals[o.Opd], _ = parseType(o.Opd, o.Op) // number assigned, error checked above
			} else { // assign initial value
				i := 0
				if o.Opd[:1] == "^" {
					i++
				}
				switch o.Opd[i : i+1] {
				case "'":
					signals[o.Opd] = 1
				case "\"":
					signals[o.Opd] = 0.5
				default:
					signals[o.Opd] = 0
				}
			}
		}

		i := len(t.newSignals)        // to ignore reserved signals
		for k, v := range signals { // assign signals to slice from map
			t.newSignals = append(t.newSignals, v)
			for ii, o := range t.newListing {
				if o.Opd == k {
					o.N = i
				}
				for i, pre := range reservedSignalNames { // reserved signals are added in order
					if o.Opd == pre {
						o.N = i // shadowed
					}
				}
				o.Opn = operators[o.Op].N
				t.newListing[ii] = o
			}
			i++
		}

		if display.Paused { // resume play on launch if paused
			<-pause
			display.Paused = not
		}
		lockLoad <- struct{}{}
		//transfer to sound engine, or if reload, replace existing at that index
		if reload < 0 || reload > len(transfer.Listing)-1 {
			t.dispListings = append(t.dispListings, t.dispListing)
			transfer.Listing = append(transfer.Listing, t.newListing)
			transfer.Signals = append(transfer.Signals, t.newSignals)
			if len(mutes) < len(transfer.Listing) { // not if restarting
				mutes = append(mutes, 1)
				t.unsolo = append(t.unsolo, 1)
				display.Mute = append(display.Mute, not)
				level = append(level, 1)
			}
			transmit <- yes
			<-accepted
			saveTempFile(restart, t) // skipped if restarting
		} else { // reloaded listing isn't saved to '.temp/'
			t.dispListings[reload] = t.dispListing
			transfer.Listing[reload] = t.newListing
			transfer.Signals[reload] = t.newSignals
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
		if !saveJson(t.newListing, f) {                      // save as plain text instead?
			msg("%slisting not recorded, check 'recordings/' directory exists%s", italic, reset)
		}
		if !saveJson(*t.code, "displaylisting.json") {
			msg("%slisting display not updated, check file %s'displaylisting.json'%s exists%s",
				italic, reset, italic, reset)
		}
	}
	if record {
		closeWavFile()
	}
	saveUsage(usage, t)
}

const (
	mute   = iota // 0
	unmute        // 1
)

func (m *muteSlice) set(i int, v float64) {
	display.Mute[i] = v == 0 // convert to bool
	(*m)[i] = v
}

type soundcard struct {
	file       *os.File // TODO: this needs to be changed
	channels   string
	sampleRate float64
	format     int
	convFactor float64
}

func readTokenPair(t *systemState) (reload int, ext bool, result int) {
	tt := <-tokens
	t.operator, reload, ext = tt.tk, tt.reload, tt.ext
	if (len(t.operator) > 2 && byte(t.operator[1]) == 91) || t.operator == "_" || t.operator == "" {
		return reload, ext, startNewOperation
	}
	t.operator = strings.TrimSuffix(t.operator, ",")  // to allow comma separation of tokens
	if len(t.operator) > 1 && t.operator[:1] == ":" { // hacky shorthand
		t.operand = t.operator[1:]
		t.operator = ":"
		return reload, ext, nextOperation
	}
	hO, in := t.hasOperand[t.operator]
	if !in {
		r := t.clr("%soperator or function doesn't exist:%s %s", italic, reset, t.operator)
		return reload, ext, r
	}
	_, t.isFunction = t.funcs[t.operator]

	if !hO {
		return reload, ext, nextOperation
	}
	// parse second token
	tt = <-tokens
	t.operand, reload, ext = tt.tk, tt.reload, tt.ext
	t.operand = strings.TrimSuffix(t.operand, ",") // to allow comma separation of tokens
	if t.operand == "_" || t.operand == "" {
		return reload, ext, startNewOperation
	}
	s := strings.ReplaceAll(t.operand, "{i}", "0")
	s = strings.ReplaceAll(s, "{i+1}", "0")
	t.operands = strings.Split(s, ",")
	if !t.isFunction && len(t.operands) > 1 {
		r := t.clr("only functions can have multiple operands")
		return reload, ext, r
	}
	pass := t.wmap[t.operand] && t.operator == "wav"
	switch t.operator { // operand can start with a number
	case "ls", "load", "//": pass = true
	}
	if !strings.ContainsAny(s[:1], "+-.0123456789") || pass || t.isFunction {
		return reload, ext, nextOperation
	}
	if t.num.Ber, t.num.Is = parseType(s, t.operator); !t.num.Is {
		r := t.clr("")
		return reload, ext, r // parseType will report error
	}
	return reload, ext, nextOperation
}

func parseFunction(
	t systemState,
	signals map[string]float64,
	out map[string]struct{}, // implictly de-referenced
) (function listing, result bool) {
	function = make(listing, len(t.funcs[t.operator].Body))
	copy(function, t.funcs[t.operator].Body)
	s := sf(".%d", t.fun)
	type mm struct{ at, at1, at2 bool }
	m := mm{}
	for i, o := range function {
		if len(o.Opd) == 0 {
			continue
		}
		switch o.Opd {
		case "dac", "tempo", "pitch", "grid", "sync":
			continue
		case "@":
			m.at = yes
		case "@1":
			m.at1 = yes
		case "@2":
			m.at2 = yes
		}
		if _, in := signals[o.Opd]; in || isUppercaseInitial(o.Opd) {
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
		switch o.Op {
		case "out", ">":
			out[function[i].Opd] = assigned
		}
	}
	mmm := 0
	switch m {
	case mm{not, not, not}:
		// nop
	case mm{yes, not, not}:
		mmm = 1
	case mm{yes, yes, not}:
		mmm = 2
	case mm{yes, yes, yes}:
		mmm = 3
	default:
		t.clr("malformed function") // probably not needed
		return nil, not
	}
	l := len(t.operands)
	if mmm < l {
		switch {
		case l-mmm == 1:
			msg("%slast operand ignored%s", italic, reset)
		case l-mmm > 1:
			msg("%slast %d operands ignored%s", italic, l-mmm, reset)
		}
	}
	if mmm > l {
		switch {
		case mmm == 1:
			t.clr("%sthe function requires an operand%s", italic, reset)
			return nil, not
		case mmm > 1:
			t.clr("%sthe function requires %d operands%s", italic, mmm, reset)
			return nil, not
		}
	}
	for i, opd := range t.operands { // opd shadowed
		if t.operands[i] == "" {
			t.clr("empty argument %d", i+1)
			return nil, not
		}
		if strings.ContainsAny(opd[:1], "+-.0123456789") {
			if _, ok := parseType(opd, ""); !ok {
				return nil, not // parseType will report error
			}
		}
	}
	for i, o := range function {
		if len(o.Opd) == 0 {
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
		function[i] = o
	}
	return function, yes
}

// parseType() evaluates conversion of types
func parseType(expr, op string) (n float64, b bool) {
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
	case len(expr) > 3 && expr[len(expr)-3:] == "bpm":
		if n, b = evaluateExpr(expr[:len(expr)-3]); !b {
			return 0, false
		}
		if n > 3000 && op == "in" {
			msg("%.fbpm? You're 'aving a larf mate", n)
			return 0, false
		}
		if n < 10 {
			msg("erm, why?")
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
		n = Pow(10, n)
	default:
		if n, b = evaluateExpr(expr); !b {
			return 0, false
		}
		if Abs(n) > 64 {
			msg("%.3g exceeds sensible values, use a type", n)
			return 0, false
		}
	}
	if IsInf(n, 0) || n != n { // ideally also check for zero in specific cases
		msg("number not useful")
		return 0, false
	}
	return n, true
}
func nyquist(n float64, e string) bool {
	ny := 2e4 / SampleRate
	if bounds(n, ny) {
		msg("'%s' %sis an inaudible frequency >20kHz%s", e, italic, reset)
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
// not designed to handle expressions containing eg. 1e-3-1e-2
func evaluateExpr(expr string) (float64, bool) {
	opds := []string{expr}
	var rr error
	var n, n2 float64
	var op string
	if n, rr = strconv.ParseFloat(opds[0], 64); !e(rr) { // early return
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
		msg("%s third operand in expression ignored%s", italic, reset)
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

func infoDisplay() {
	file := "infodisplay.json"
	c := 1
	s := 1
	display.Info = "clear"
	for {
		if !saveJson(display, file) {
			pf("%sinfo display not updated, check file %s%s%s exists%s\n",
				italic, reset, file, italic, reset)
			time.Sleep(2 * time.Second)
			return
		}
		select {
		case display.Info = <-info:
		case carryOn <- yes: // semaphore: received, continue
		case <-infoff:
			display.Info = sf("%sSyntə closed%s", italic, reset)
			display.On = not // stops timer in info display
			saveJson(display, file)
			return
		default: // passthrough
		}
		time.Sleep(20 * time.Millisecond) // coarse loop timing
		if display.Clip {
			c++
		}
		if c > 20 { // clip timeout
			display.Clip = not
			c = 1
		}
		if display.Sync {
			s++
		}
		if s > 10 { // sync timeout
			display.Sync = not
			s = 1
		}
	}
}

type stereoPair struct {
	left,
	right float64
}

// The Sound Engine does the bare minimum to generate audio
// It is freewheeling, it won't block on the action of any other goroutine, only on IO, namely writing to soundcard
// The latency and jitter of the audio output is entirely dependent on the soundcard and its OS driver,
// except where the calculations don't complete in time under heavy load and the soundcard driver buffer underruns. Frequency accuracy is determined by the soundcard clock and precision of float64 type
// If the loop time exceeds the sample rate over number of samples given by RATE the Sound Engine will panic
// The data transfer structures need a good clean up
// Some work has been done on profiling, beyond design choices such as using slices instead of maps
// Using floats is probably somewhat profligate, later on this may be converted to int type which would provide ample dynamic range
// Now with glitch protection! IO handled in separate go routine
func SoundEngine(file *os.File, bits int) {
	defer close(stop)
	w := bufio.NewWriterSize(file, 256) // need to establish whether buffering is necessary here
	defer w.Flush()
	//w := file // unbuffered alternative
	output := selectOutput(bits)
	if output == nil {
		return
	}

	const (
		Tau        = 2 * Pi
		RATE       = 2 << 14 // lenient
	)

	var (
		no     noise   = noise(time.Now().UnixNano())
		l, h   float64 = 1, 2 // limiter, hold
		dac    float64        // output
		env    float64 = 1    // for exit envelope
		peak   float64        // vu meter
		dither float64
		n      int // loop counter

		rate     time.Duration = time.Duration(7292) // loop timer, initialised to approximate resting rate
		lastTime time.Time
		rates    [RATE]time.Duration
		t        time.Duration

		s        float64 = 1 // sync=0
		mx, my float64 = 1, 1 // mouse smooth intermediates
		hpf, x float64        // DC-blocking high pass filter
		hpf2560, x2560,
		hpf160, x160 float64 // limiter detection
		lpf50, lpf510,
		deemph float64 // de-emphasis
		//α                     = 30 * 1 / (SampleRate/(2*Pi*6.3) + 1) // co-efficient for setmix
		α                     = 1 / (SampleRate/(2*Pi*194) + 1) // co-efficient for setmix
		hroom                 = (convFactor - 1.0) / convFactor // headroom for positive dither
		c, mixF       float64 = 4, 4                            // mix factor
		pd            int
		sides   float64
		setmixDefault = 800 / SampleRate
		current       int
		p = 1.0
		endPause int
		samples = make(chan stereoPair, 2400) // buffer up to 50ms of samples (@ 48kHz), introduces latency
	)
	defer close(samples)
	no *= 77777777777 // force overflow
	defer func() {    // fail gracefully
		switch r := recover(); r {
		case nil:
			return // exit normally
		default:
			msg("oops - %v", r) // report error to infoDisplay
			/*
			var buf [4096]byte
			n := runtime.Stack(buf[:], false)
			msg("%s", buf[:n]) // print stack trace to infoDisplay*/
			if current > len(transfer.Listing)-1 {
				current = len(transfer.Listing) - 1
			}
			if current > len(transfer.Listing)-1 || len(transfer.Signals) < 1 {
				break
			}
			transfer.Listing[current] = listing{operation{Op: "deleted", Opn: 31}} // delete listing
			transfer.Signals[current][0] = 0                                       // silence listing
			info <- sf("listing deleted: %d", current)
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
	copy(listings, transfer.Listing) // TODO: these don't properly de-reference because a slice of slices,
	copy(sigs, transfer.Signals)     // listings need to be copied individually
	copy(wavs, transfer.Wavs)        // for now it's not a problem, or the copying is not even necessary 
	tapes := make([][]float64, len(transfer.Listing), 26)
	for i := range tapes { // i is shadowed
		tapes[i] = make([]float64, TLlen)
	}
	tf := make([]float64, len(transfer.Listing), len(transfer.Listing)+31)
	th := make([]float64, len(transfer.Listing), len(transfer.Listing)+31)
	tx := make([]float64, len(transfer.Listing), len(transfer.Listing)+31)
	pan := make([]float64, len(transfer.Listing), len(transfer.Listing)+31)
	accepted <- yes
	type st8 int
	const (
		run st8 = iota
		on
		off
	)
	syncSt8 := make([]st8, len(transfer.Listing), len(transfer.Listing)+27)
	peakfreq := make([]float64, len(transfer.Listing), len(transfer.Listing)+28) // peak frequency for setlevel
	for i := range peakfreq {
		peakfreq[i] = setmixDefault
	}
	m := make([]float64, len(transfer.Listing), len(transfer.Listing)+29)  // filter intermediate for mute
	lv := make([]float64, len(transfer.Listing), len(transfer.Listing)+30) // filter intermediate for level
	fftArray := make([][N]float64, len(transfer.Listing), len(transfer.Listing)+32)
	ifftArray := make([][N]float64, len(transfer.Listing), len(transfer.Listing)+32)
	ifft2 := make([][N]float64, len(transfer.Listing), len(transfer.Listing)+32)
	z := make([][N]complex128, len(transfer.Listing), len(transfer.Listing)+33)
	zf := make([][N]complex128, len(transfer.Listing), len(transfer.Listing)+33)
	ffrz := make([]bool, len(transfer.Listing), len(transfer.Listing)+33)
	go func() { // if samples channel runs empty insert zeros instead and filter heavily
		lpf := stereoPair{}
		loop:
		for env > 0 || n%1024 != 0 { // finish on end of buffer
			select {
			case s, ok := <-samples:
				if !ok { break loop }
				lpf.stereoLpf(s, 0.5)
			default:
				lpf.stereoLpf(stereoPair{}, 0.0013)
			}
			L := clip(lpf.left) * convFactor // clip will display info
			R := clip(lpf.right) * convFactor // clip will display info
			output(w, L)
			output(w, R)
		}
	}()

	lastTime = time.Now()
	for {
		select {
		case <-pause:
			p = 0
			endPause = n
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
				syncSt8 = append(syncSt8, run)
				peakfreq = append(peakfreq, setmixDefault)
				stacks = append(stacks, stack)
				fftArray = append(fftArray, [N]float64{})
				ifftArray = append(ifftArray, [N]float64{})
				ifft2 = append(ifft2, [N]float64{})
				z = append(z, [N]complex128{})
				zf = append(zf, [N]complex128{})
			} else if reload > -1 {
				m[reload] = 0 // m ramps to mute value on reload
				syncSt8[reload] = 0
			}
			if rs && rootSync() {
				lastTime = time.Now()
			}
		default:
			// play
		}
		if p == 0 && endPause < n-12000 {
			pause <- not // blocks until `: play`, bool is purely semantic
			if exit {
				break
			}
			p = 1
			lastTime = time.Now()
		}

		if n%15127 == 0 { // arbitrary interval all-zeros protection for noise lfsr
			no ^= 1 << 27
		}

		mo := mouse
		mx = mx + (mo.X-mx)*0.0026 // lpf @ ~20Hz
		my = my + (mo.Y-my)*0.0013

		for i, list := range listings {
			for _, ii := range daisyChains {
				sigs[i][ii] = sigs[(i+len(sigs)-1)%len(sigs)][ii]
			}
			m[i] = m[i] + (p*mutes[i]-m[i])*0.0013   // anti-click filter @ ~10hz
			lv[i] = lv[i] + (level[i]-lv[i])*0.125 // @ 1091hz
			// mouse values
			sigs[i][4] = mx
			sigs[i][5] = my
			sigs[i][6] = mo.Left
			sigs[i][7] = mo.Right
			sigs[i][8] = mo.Middle
			r := 0.0
			op := 0
			for _, o := range list {
				current = i
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
					//r = Sin(Tau * r)
					r = sine(r)
				case 6: // "mod"
					r = mod(r, sigs[i][o.N])
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
					r = tanh(r)
				case 12: // "pow"
					if Signbit(sigs[i][o.N]) && r == 0 {
						r = Copysign(1e-308, r) // inverse is within upper range of float
					}
					r = Pow(r, sigs[i][o.N])
				case 13: // "base"
					sg := sigs[i][o.N]
					switch sg {
					case E:
						r = Exp(r)
					case 2:
						r = Exp2(r)
					default:
						r = Pow(sg, r)
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
					r *= no.ise() // roll a fresh one
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
					tapes[i][n%TLlen] = th[i] // record head
					tl := SampleRate * TAPE_LENGTH
					//t := Abs(Min(1/sigs[i][o.N], tl))
					t := Mod((1 / sigs[i][o.N]), tl)
					if sigs[i][o.N] == 0 {
						t = 0
					}
					xa := (n + TLlen - int(t)) % TLlen
					x := mod(float64(n+TLlen)-(t), tl)
					ta0 := tapes[i][(n+TLlen-int(t)-1)%TLlen]
					ta := tapes[i][xa] // play heads
					tb := tapes[i][(n+TLlen-int(t)+1)%TLlen]
					tb1 := tapes[i][(n+TLlen-int(t)+2)%TLlen]
					xx := mod(x-float64(xa), tl-1) // to avoid end of loop clicks
					z := xx - 0.5
					ev1, od1 := tb+ta, tb-ta
					ev2, od2 := tb1+ta0, tb1-ta0
					// 4-point 4th order "optimal" interpolation filter by Olli Niemitalo
					c0 := ev1*0.45645918406487612 + ev2*0.04354173901996461
					c1 := od1*0.47236675362442071 + od2*0.17686613581136501
					c2 := ev1*-0.253674794204558521 + ev2*0.25371918651882464
					c3 := od1*-0.37917091811631082 + od2*0.11952965967158
					c4 := ev1*0.04252164479749607 + ev2*-0.04289144034653719
					r = (((c4*z+c3)*z+c2)*z+c1)*z + c0
					tf[i] = (tf[i] + r) * 0.5 // roll off the top end @ 7640Hz
					r = tf[i]
				case 19: // "--"
					r = sigs[i][o.N] - r
				case 20: // "tap"
					tl := SampleRate * TAPE_LENGTH
					//t := Abs(Min(1/sigs[i][o.N], tl))
					t := Min(Abs(1/sigs[i][o.N]), tl)
					xa := (n + TLlen - int(t)) % TLlen
					x := mod(float64(n+TLlen)-(t), tl)
					ta0 := tapes[i][(n+TLlen-int(t)-1)%TLlen]
					ta := tapes[i][xa] // play heads
					tb := tapes[i][(n+TLlen-int(t)+1)%TLlen]
					tb1 := tapes[i][(n+TLlen-int(t)+2)%TLlen]
					z := mod(x-float64(xa), tl-1) - 0.5 // to avoid end of loop clicks
					// 4-point 2nd order "optimal" interpolation filter by Olli Niemitalo
					ev1, od1 := tb+ta, tb-ta
					ev2, od2 := tb1+ta0, tb1-ta0
					c0 := ev1*0.42334633257225274 + ev2*0.07668732202139628
					c1 := od1*0.26126047291143606 + od2*0.24778879018226652
					c2 := ev1*-0.213439787561776841 + ev2*0.21303593243799016
					r += (c2*z+c1)*z + c0
				case 21: // "f2c" // r = 1 / (1 + 1/(Tau*r))
					r = Abs(r)
					r *= Tau
					r /= (r + 1)
				case 22: // "wav"
					r += 1 // to allow negative input to reverse playback
					r = Abs(r)
					l := len(wavs[int(sigs[i][o.N])])
					r *= float64(l)
					x1 := int(r) % l
					w0 := wavs[int(sigs[i][o.N])][(l+int(r-1))%l]
					w1 := wavs[int(sigs[i][o.N])][x1]
					w2 := wavs[int(sigs[i][o.N])][int(r+1)%l]
					w3 := wavs[int(sigs[i][o.N])][int(r+2)%l]
					z := mod(r-float64(x1), float64(l-1)) - 0.5
					ev1, od1 := w2+w1, w2-w1
					ev2, od2 := w3+w0, w3-w0
					c0 := ev1*0.42334633257225274 + ev2*0.07668732202139628
					c1 := od1*0.26126047291143606 + od2*0.24778879018226652
					c2 := ev1*-0.213439787561776841 + ev2*0.21303593243799016
					r = (c2*z+c1)*z + c0
				case 23: // "8bit"
					r = float64(int8(r*sigs[i][o.N])) / sigs[i][o.N]
				case 24: // "index"
					r = float64(i)
				case 25: // "<sync"
					r *= s
					r += (1 - s) * sigs[i][o.N] // phase offset
				case 26: // ">sync", ".>sync"
					switch { // syncSt8 is a slice to make multiple >sync operations independent
					case r <= 0 && syncSt8[i] == run: // edge-detect
						s = 0
						display.Sync = yes
						syncSt8[i] = on
					case syncSt8[i] == on: // single sample pulse
						s = 1
						syncSt8[i] = off
					case r > 0: // reset
						syncSt8[i] = run
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
					a := Abs(sigs[i][o.N])
					d := a - peakfreq[i]
					//peakfreq[i] += d * α * (a / peakfreq[i])
					peakfreq[i] += d * α * (Abs(d) * a / peakfreq[i])
					//r *= Min(1, 140/(peakfreq[i]*SampleRate+20)) // ignoring density
					r *= Min(1, Sqrt(140/(peakfreq[i]*SampleRate+20)))
				case 35: // "print"
					pd++ // unnecessary?
					if (pd)%32768 == 0 && !exit {
						info <- sf("listing %d, op %d: %.5g", i, op, r)
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
					// r := 0 // allow mixing in of preceding listing
					c := -3.0 // to avoid being mixed twice
					for ii := range listings {
						if ii == i { // ignore current listing
							continue
						}
						r += sigs[ii][0]
						c += m[ii]
					}
					c = Max(c, 1)
					r /= c
				case 40: // "fft"
					fftArray[i][n%N] = r
					if n%N2 == 0 && n >= N && !ffrz[i] {
						nn := n % N
						var zz [N]complex128
						for n := range fftArray[i] { // n is shadowed
							ww := float64(n) * N1
							w := Pow(1-ww*ww, 1.25) // modified Welch
							zz[n] = complex(w*fftArray[i][(n+nn)%N], 0)
						}
						z[i] = fft(zz, 1)
					}
				case 41: // "ifft"
					if n%N == 0 && n >= N {
						zz := fft(z[i], -1)
						for n, z := range zz { // n, z are shadowed
							w := (1 - Cos(Tau*float64(n)*N1)) * 0.5 // Hann
							ifftArray[i][n] = w * real(z) * invN2
						}
					}
					if n%N == N2+1 && n >= N {
						zz := fft(z[i], -1)
						for n, z := range zz { // n, z are shadowed
							w := (1 - Cos(Tau*float64(n)*N1)) * 0.5 // Hann
							ifft2[i][n] = w * real(z) * invN2
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
						l := int(mod(s, 1) * N)
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
				case 46: // "rev"
					if n%N2 == 0 && n >= N && !ffrz[i] {
						ii := i // from 'the blue book':
						for i, j := 0, len(z[ii])-1; i < j; i, j = i+1, j-1 {
							z[ii][i], z[ii][j] = z[ii][j], z[ii][i]
						}
					}
				case 47: // "ffltr"
					if n%N2 == 0 && n >= N && !ffrz[i] {
						coeff := complex(Abs(sigs[i][o.N]*N), 0)
						coeff *= Tau
						coeff /= (coeff + 1)
						for n := range z[i] {
							zf[i][n] = zf[i][n] + (z[i][n]-zf[i][n])*coeff
							z[i][n] = zf[i][n]
						}
					}
				case 48: // "ffzy"
					if n%N2 == 0 && n >= N && !ffrz[i] {
						for n := range z[i] {
							r, θ := cmplx.Polar(z[i][n])
							θ += Pi * no.ise()
							z[i][n] = cmplx.Rect(r, θ)
						}
					}
				case 49: // "ffaze"
					if n%N2 == 0 && n >= N && !ffrz[i] {
						for n := range z[i] {
							r, θ := cmplx.Polar(z[i][n])
							θ += Tau * sigs[i][o.N]
							z[i][n] = cmplx.Rect(r, θ)
						}
					}
				case 50: // "reu"
					if n%N2 == 0 && n >= N && !ffrz[i] {
						ii := i // from 'the blue book':
						for i, j := 0, len(z[ii])/2; i < j; i, j = i+1, j-1 {
							z[ii][i], z[ii][j] = z[ii][j], z[ii][i]
						}
						for i, j := len(z[ii])/2, len(z[ii])-1; i < j; i, j = i+1, j-1 {
							z[ii][i], z[ii][j] = z[ii][j], z[ii][i]
						}
					}
				default:
					// nop, r = r
				}
				op++
			}
			// This can introduce distortion, which is mitigated by mixF filter below
			// Skipping loop early isn't really necessary, but it has been kept in as a source of character
			// The distortion arises because c is not incremented by 1 for unmuted listings
			// whose output is exactly zero, thereby modulating the mix factor
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
			c += m[i] // add mute to mix factor
			mid := sigs[i][0] * m[i] * lv[i] // sigs[i][0] left intact for `from` operator
			if mid > ct { // soft clip
				mid = ct + tanh(mid-ct)
				display.Clip = yes
			} else if mid < -ct {
				mid = tanh(mid+ct) - ct
				display.Clip = yes
			}
			pan[i] = Max(-1, Min(1, pan[i])) // to prevent overmodulation
			sides += pan[i] * mid * 0.5
			mid *= 1 - Abs(pan[i]*0.5)
			dac += mid
		}
		hpf = (hpf + dac - x) * 0.9994 // hpf ≈ 4.6Hz
		x = dac
		dac = hpf
		c += 16 / (c*c + 4.77)
		mixF = mixF + (Abs(c)-mixF)*0.00026 // ~2Hz @ 48kHz
		c = 0
		dac /= mixF
		sides /= mixF
		dac *= gain
		sides *= gain
		// limiter
		hpf2560 = (hpf2560 + dac - x2560) * 0.749
		x2560 = dac
		hpf160 = (hpf160 + dac - x160) * 0.97948
		x160 = dac
		// parallel low end path
		lpf50 = lpf50 + (dac-lpf50)*0.006536
		d := 4 * lpf50 / (1 + Abs(lpf50*4)) // tanh approximation
		lpf510 = lpf510 + (d-lpf510)*0.006536
		deemph = lpf510
		 // apply pre-emphasis to detection
		det := Abs(11.4*hpf2560+1.6*hpf160+dac) * 0.5
		if det > l {
			l = det // MC
			h = release
			display.GR = yes
		}
		dac /= l // VCA
		sides /= l
		dac += deemph // low end path is mono only, may clip at output
		h /= release
		l = (l-1)*(1/(h+1/(1-release))+release) + 1 // snubbed decay curve
		display.GR = l > 1+3e-4
		if exit {
			dac *= env // fade out
			sides *= env
			env -= fade // linear fade-out (perceived as logarithmic)
			if env < 0 {
				break // equivalent to: return
			}
		}
		dither = no.ise()
		dither += no.ise()
		dither *= 0.5
		dac *= hroom
		dac += dither / convFactor       // dither dac value ±1 from xorshift lfsr
		if abs := Abs(dac); abs > peak { // peak detect
			peak = abs
		}
		display.Vu = peak
		peak -= 5e-5 // meter ballistics, linear (effectively logarithmic decay in dB)
		if peak < 0 {
			peak = 0
		}
		sides = Max(-0.5, Min(0.5, sides))
		if record {
			L := Max(-1, Min(1, dac+sides)) * convFactor
			R := Max(-1, Min(1, dac-sides)) * convFactor
			writeWav(L, R)
		}
		t = time.Since(lastTime)
		samples <- stereoPair{left: dac+sides, right: dac-sides}
		lastTime = time.Now()
		rate += t
		rates[n%RATE] = t // rolling average buffer
		rate -= rates[(n+1)%RATE]
		if n%RATE == 0 {
			display.Load = rate / RATE
		}
		dac = 0
		sides = 0
		n++
	}
}

func clip(in float64) float64 { // hard clip
	if in > 1 {
		in = 1
		display.Clip = yes
	} else if in < -1 {
		in = -1
		display.Clip = yes
	}
	return in
}

func (y *stereoPair) stereoLpf(x stereoPair, coeff float64) {
	(*y).left += (x.left-(*y).left)*coeff
	(*y).right += (x.right-(*y).right)*coeff
}

var sineTab = make([]float64, int(SampleRate))

const width = 2 << 16 // precision of tanh table
var tanhTab = make([]float64, width)

func init() {
	for i := range sineTab {
		// using cosine, even function avoids negation for -ve x
		sineTab[i] = Cos(2 * Pi * float64(i) / SampleRate)
	}
}

func init() {
	for i := range tanhTab {
		tanhTab[i] = Tanh(float64(i) / width)
	}
}

const Tau = 2*Pi
func sine(x float64) float64 {
	return Sin(Tau * x)
	if x < 0 {
		x = -x
	}
	sr := int(SampleRate)
	a := int(x * SampleRate)
	sa := sineTab[a%sr]
	sb := sineTab[(a+1)%sr]
	xx := mod((x*SampleRate)-float64(a), SampleRate-1)
	return sa + ((sb - sa) * xx) // linear interpolation
}

func tanh(x float64) float64 {
	if x < -1 || x > 1 {
		return Tanh(x)
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
	return float64(*n) * twoInvMaxUint - 1
}
var invMaxInt32 = 1.0/MaxInt32
func mod(x, y float64) float64 {
	return Mod(x, y)
	pos := yes
	if x < 0 {
		pos = not
		x = -x
	}
	m := uint32(MaxInt32*x/y)
	//m %= uint32(MaxInt32*y) // dirty mod
	if pos {
		return float64(m)*invMaxInt32
	}
	return -float64(m)*invMaxInt32
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

// operatorParticulars process field functions
// These must be of type processor func(*systemState) int

func noCheck(_ *systemState) int {
	return nextOperation
}

func checkOut(s *systemState) int {
	_, in := s.out[s.operand]
	switch {
	case s.num.Is:
		return s.clr("%soutput to number not permitted%s", italic, reset)
	case in && s.operand[:1] != "^" && s.operand != "dac" && s.operator != "out+" && s.operator != ">+":
		return s.clr("%s: %sduplicate output to signal, c'est interdit%s", s.operand, italic, reset)
	case s.operand == "@":
		return s.clr("%scan't send to @, represents function operand%s", italic, reset)
	}
	if !isUppercaseInitial(s.operand) {
		s.out[s.operand] = assigned
	}
	return nextOperation
}

func endFunctionDefine(t *systemState) int {
	if !t.fIn || len(t.newListing[t.st+1:]) < 1 {
		msg("%sno function definition%s", italic, reset)
		return startNewOperation
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
	t.funcs[name] = fn{Body: t.newListing[t.st+1:]}
	msg("%sfunction %s%s%s ready%s.", italic, reset, name, italic, reset)
	if t.funcsave {
		if !saveJson(t.funcs, "functions.json") {
			msg("function not saved!")
		} else {
			msg("%sfunction saved%s", italic, reset)
		}
	}
	return startNewListing // fIn will be reset on start
}

func checkPushPop(s *systemState) int {
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
		msg("%sno push to pop%s", italic, reset)
		return startNewOperation
	}
	return nextOperation
}

func tapeUnique(s *systemState) int {
	for _, o := range s.newListing {
		if o.Op == "tape" {
			msg("%sonly one tape per listing%s", italic, reset)
			return startNewOperation
		}
	}
	return nextOperation
}

func parseIndex(s listingState, l int) (int, bool) {
	if l < 1 {
		msg("%snothing to %s%s", italic, reset, s.operator)
		return 0, not
	}
	if s.operand == "" { // ignore checks for empty operands, iffy?
		return 0, yes
	}
	n, rr := strconv.Atoi(s.operand)
	if e(rr) {
		msg("%soperand not an integer%s", italic, reset)
		return 0, not
	}
	if n < 0 || n > l {
		msg("%soperand out of range%s", italic, reset)
		return 0, not
	}
	return n, yes
}

func excludeCurrent(op string, i, l int) bool {
	if i > l-1 {
		msg("%scan't %s current listing%s", italic, op, reset)
		return yes
	}
	return not
}

func eraseOperations(s *systemState) int {
	n, ok := parseIndex(s.listingState, len(s.dispListing))
	if !ok {
		return startNewOperation // error reported by parseIndex
	}
	for i := 0; i < len(s.dispListing)-n; i++ { // recompile
		tokens <- token{s.dispListing[i].Op, -1, yes}
		if len(s.dispListing[i].Opd) > 0 { // dodgy?
			tokens <- token{s.dispListing[i].Opd, -1, yes}
		}
	}
	tokens <- token{"", -1, not}
	return startNewListing
}

func checkWav(s *systemState) int {
	if s.wmap[s.operand] || (s.operand == "@" && s.fIn) {
		return nextOperation
	}
	return s.clr("%s %sisn't in wav list%s", s.operand, italic, reset)
}

func enactMute(s *systemState) int {
	i, ok := parseIndex(s.listingState, len(transfer.Listing))
	if !ok || excludeCurrent(s.operator, i, len(transfer.Listing)) {
		return startNewOperation // error reported by parseIndex
	}
	if s.operator == "m+" {
		s.muteGroup = append(s.muteGroup, i) // add to mute group
		return startNewOperation
	}
	s.muteGroup = append(s.muteGroup, i)
	for _, i := range s.muteGroup {
		mutes.set(i, 1-mutes[i])              // toggle
		s.unsolo[i] = mutes[i]            // save status for unsolo
		if s.solo == i && mutes[i] == 0 { // if muting solo'd listing reset solo
			s.solo = -1
		}
	}
	if s.operator[:1] == "." && len(s.newListing) > 0 {
		tokens <- token{"mix", -1, not}
	}
	s.muteGroup = []int{}
	return startNewOperation
}

func enactSolo(s *systemState) int {
	i, ok := parseIndex(s.listingState, len(transfer.Listing))
	if !ok {
		return startNewOperation // error reported by parseIndex
	}
	if i == len(transfer.Listing) && s.operator != ".solo" {
		msg("operand out of range")
		return startNewOperation
	}
	if s.solo == i { // unsolo index given by operand
		for ii := range mutes { // i is shadowed
			if i == ii {
				continue
			}
			mutes.set(ii, s.unsolo[ii]) // restore all other mutes
		}
		s.solo = -1 // unset solo index
	} else { // solo index given by operand
		for ii := range mutes {
			if ii == i {
				mutes.set(i, unmute) // unmute solo'd index
				continue
			}
			s.unsolo[ii] = mutes[ii] // save all mutes
			mutes.set(ii, mute)         // mute all other listings
		}
		s.solo = i // save index of solo
	}
	if s.operator[:1] == "." && len(s.newListing) > 0 {
		tokens <- token{"mix", -1, not}
	}
	return startNewOperation
}

func setNoiseFreq(s *systemState) int {
	s.newListing = append(s.newListing, listing{{Op: "push"}, {Op: "in", Opd: sf("%v", NOISE_FREQ)}, {Op: "out", Opd: "^freq"}, {Op: "pop"}}...)
	return nextOperation
}

func beginFunctionDefine(s *systemState) int {
	if _, ok := s.funcs[s.operand]; ok {
		msg("%swill overwrite existing function!%s", red, reset)
	} else if _, ok := s.hasOperand[s.operand]; ok { // using this map to avoid cyclic reference of operators
		msg("%sduplicate of extant operator, use another name%s", italic, reset)
		return startNewOperation // only return early if not a function and in hasOperand map
	}
	s.st = len(s.newListing) // because current input hasn't been added yet
	s.fIn = yes
	msg("%sbegin function definition,%s", italic, reset)
	msg("%suse @ for operand signal%s", italic, reset)
	return nextOperation
}

func doLoop(s *systemState) int {
	var rr error
	s.do, rr = strconv.Atoi(s.operand)
	if e(rr) { // returns do as zero
		msg("%soperand not an integer%s", italic, reset)
		return startNewOperation
	}
	msg("%snext operation repeated%s %dx", italic, reset, s.do)
	s.to = s.do
	return startNewOperation
}
func modeSet(s *systemState) int {
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
		}
		saveJson([]listing{{operation{Op: advisory}}}, "displaylisting.json")
		p("Stopped")
		close(infoff)
		if s.funcsave && !saveJson(s.funcs, "functions.json") {
			msg("functions not saved!")
			return startNewListing
		}
		time.Sleep(30 * time.Millisecond) // wait for infoDisplay to finish
		return exitNow
	case "erase", "e":
		return startNewListing
	case "foff":
		s.funcsave = not
		display.Mode = "off"
	case "fon":
		s.funcsave = yes
		display.Mode = "on"
		if !saveJson(s.funcs, "functions.json") {
			msg("functions not saved!")
			return startNewOperation
		}
		msg("%sfunctions saved%s", italic, reset)
	case "pause":
		if started && !display.Paused {
			pause <- yes // bool is purely semantic
			display.Paused = yes
		}
	case "play":
		if !display.Paused {
			return startNewOperation
		}
		<-pause
		display.Paused = not
	case "clear", "c":
		msg("clear")
	case "verbose":
		switch s.code {
		case &s.dispListings:
			s.code = &transfer.Listing
		case &transfer.Listing:
			s.code = &s.dispListings
		}
		display.Verbose = !display.Verbose
		if !saveJson(*s.code, "displaylisting.json") {
			msg("%slisting display not updated, check %s'displaylisting.json'%s exists%s",
				italic, reset, italic, reset)
		}
	case "stats":
		if !started {
			return startNewOperation
		}
		stats := new(debug.GCStats)
		debug.ReadGCStats(stats)
		msg("___GC statistics:___")
		msg("No.: %v", stats.NumGC)
		msg("Tot.: %v", stats.PauseTotal)
		msg("Avg.: %v", stats.PauseTotal/time.Duration(stats.NumGC))
	case "mstat":
		if !started {
			return startNewOperation
		}
		stats := new(runtime.MemStats)
		runtime.ReadMemStats(stats)
		msg("___Mem Stats:___")
		msg("Alloc: %v", stats.Alloc)
		msg("Sys: %v", stats.Sys)
		msg("Live: %v", stats.Mallocs-stats.Frees)
	case "mc": // mouse curve, exp or lin
		mc = !mc
	case "ds":
		ds = yes // not intended to be invoked while paused
	case "rs": // root sync, is this needed any more?
		rs = yes
		msg("%snext launch will sync to root instance%s", italic, reset)
	default:
		msg("%sunrecognised mode: %s%s", italic, reset, s.operand)
	}
	return startNewOperation
}

func enactDelete(s *systemState) int {
	n, ok := parseIndex(s.listingState, len(transfer.Listing))
	if !ok || excludeCurrent(s.operator, n, len(transfer.Listing)) {
		return startNewOperation // error reported by parseIndex
	}
	mutes.set(n, mute)  // wintermute
	if display.Paused { // play resumed to enact deletion in sound engine
		<-pause
		display.Paused = not
	}
	time.Sleep(150 * time.Millisecond) // wait for envelope to complete
	transfer.Listing[n] = listing{operation{Op: "deleted", Opn: 31}}
	transfer.Signals[n][0] = 0 // silence listing
	s.dispListings[n] = listing{operation{Op: "deleted"}}
	transmit <- yes
	<-accepted
	if !saveJson(*(s.code), "displaylisting.json") {
		msg("%slisting display not updated, check %s'displaylisting.json'%s exists%s",
			italic, reset, italic, reset)
	}
	if s.operator[:1] == "." {
		tokens <- token{"mix", -1, not}
	}
	return startNewOperation
}

func checkIndexIncl(s *systemState) int { // eg. listing can level or pan itself
	if _, ok := parseIndex(s.listingState, len(transfer.Listing)); !ok {
		return startNewOperation // error reported by parseIndex
	}
	return nextOperation
}

func checkIndex(s *systemState) int {
	i, ok := parseIndex(s.listingState, len(transfer.Listing))
	if !ok || excludeCurrent(s.operator, i, len(transfer.Listing)) {
		return startNewOperation // error reported by parseIndex
	}
	return nextOperation
}

func enactRpl(s *systemState) int {
	reload := checkIndex(s)
	msg("%swill replace listing %s%d%s on launch%s", italic, reset, reload, italic, reset)
	return startNewOperation
}

func unmuteAll(s *systemState) int {
	if _, ok := parseIndex(s.listingState, len(transfer.Listing)); !ok {
		return startNewOperation
	}
	for i := range mutes {
		mutes.set(i, unmute)
	}
	return startNewOperation
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
		msg("%s%s set to%s %.3gms", italic, op, reset, 1e3/(f*SampleRate))
		return
	}
	msg("%s%s set to%s %.3gs", italic, op, reset, 1/(f*SampleRate))
}

func checkFade(s *systemState) int {
	fd, ok := parseFloat(s.num, 1/(MAX_FADE*SampleRate), 1/(MIN_FADE*SampleRate))
	if !ok { // error reported by parseFloat
		return startNewOperation
	}
	fade = fd //Pow(FDOUT, fd)
	reportFloatSet(s.operator, fd)
	return startNewOperation
}

func checkRelease(s *systemState) int {
	if s.operand == "time" {
		msg("%slimiter release is:%s %.4gms", italic, reset,
			-1000/(Log(release)*SampleRate/Log(8000)))
		return startNewOperation
	}
	v, ok := parseFloat(s.num, 1/(MAX_RELEASE*SampleRate), 1/(MIN_RELEASE*SampleRate))
	if !ok { // error reported by parseFloat
		return startNewOperation
	}
	release = Pow(125e-6, v)
	reportFloatSet("limiter "+s.operator, v) // report embellished
	return startNewOperation
}

func adjustGain(s *systemState) int {
	if s.operand == "zero" {
		gain = 1
	} else if s.operand == "is" {
	} else if n, ok := parseType(s.operand, s.operator); ok {
		gain *= n
		if Abs(Log10(gain)) < 1e-12 { // hacky
			gain = 1
		} else if gain < 0.05 { // lower bound ~ -26db
			gain = 0.05
		}
	}
	msg("%sgain set to %s%.2gdb", italic, reset, 20*Log10(gain))
	return startNewOperation
}

func adjustClip(s *systemState) int {
	if n, ok := parseType(s.operand, s.operator); ok { // permissive, no bounds check
		ct = n
		msg("%sclip threshold set to %.3g%s", italic, ct, reset)
	}
	return startNewOperation
}

func checkComment(s *systemState) int {
	if len(s.newListing) > 0 {
		msg("%sa comment has to be the first and only operation of a listing...%s", italic, reset)
		return startNewOperation
	}
	return nextOperation
}

func isUppercaseInitial(operand string) bool {
	switch len(operand) {
	case 0:
		return not
	case 1:
		return unicode.IsUpper([]rune(operand)[0])
	}
	r := 0
	if operand[:1] == "'" || operand[:1] == "^" {
		r = 1
	}
	return unicode.IsUpper([]rune(operand)[r])
}
