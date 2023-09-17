//go:build (freebsd || linux) && amd64

// +build freebsd linux

//  is an audio live coding environment
// This file implements BSD and Linux specific functions for 64bit x86 

package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	. "math" // don't do this!
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
//	"unicode"
	"unsafe" // :D
)

// record indicates recording in progress
var (
	BYTE_ORDER  = binary.LittleEndian // not allowed in constants

	record bool
	wavHeader = []byte{82, 73, 70, 70, 36, 228, 87, 0, 87, 65, 86, 69, 102, 109, 116, 32, 16, 0, 0, 0, 1, 0, 2, 0, 128, 187, 0, 0, 0, 238, 2, 0, 4, 0, 16, 0, 100, 97, 116, 97, 0, 228, 87, 0} // 16bit signed PCM 48kHz
	wavFile *os.File
)

func setupSoundCard(file string) (sc soundcard, success bool) {
	// open audio output (everything is a file...)
	var rr error
	sc.file, rr = os.OpenFile(file, os.O_WRONLY, 0644)
	if e(rr) {
		p(rr)
		p("soundcard not available, shutting down...")
		return sc, not
	}

	// set bit format
	var req uint32 = SNDCTL_DSP_SETFMT
	var data uint32 = SELECTED_FMT
	_, _, ern := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(sc.file.Fd()),
		uintptr(req),
		uintptr(unsafe.Pointer(&data)),
	)
	if ern != 0 {
		p("set format:", ern)
		return sc, not
	}
	if data != SELECTED_FMT {
		info <- "Bit format not available! Change requested format in file"
	}
	sc.format = 16
	switch {
	case data == AFMT_S16_LE:
		sc.convFactor = MaxInt16
	case data == AFMT_S32_LE:
		sc.convFactor = MaxInt32
		sc.format = 32
	case data == AFMT_S8:
		sc.convFactor = MaxInt8
		sc.format = 8
	default:
		p("\n--Incompatible bit format!--\nChange requested format in file--\n")
		return sc, not
	}

	// set channels here, stereo or mono
	req = SNDCTL_DSP_CHANNELS
	data = CHANNELS
	_, _, ern = syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(sc.file.Fd()),
		uintptr(req),
		uintptr(unsafe.Pointer(&data)),
	)
	if ern != 0 || data != CHANNELS {
		p("\n--requested channels not accepted--")
		return sc, not
	}
	switch data {
	case STEREO:
		sc.channels = "stereo"
	case MONO:
		sc.channels = "mono"
	default:
		p("\n--Incompatible channels! Change requested format in file--\n")
		return sc, not
	}

	// set sample rate
	req = SNDCTL_DSP_SPEED
	data = SAMPLE_RATE
	_, _, ern = syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(sc.file.Fd()),
		uintptr(req),
		uintptr(unsafe.Pointer(&data)),
	)
	if ern != 0 {
		p("set rate:", ern) // do something else here
		time.Sleep(time.Second)
	}
	sc.sampleRate = float64(data)
	if data != SAMPLE_RATE {
		info <- "--requested sample rate not accepted--"
		info <- sf("new sample rate: %vHz", sc.sampleRate)
	}
	display.SR = sc.sampleRate // fixed at initial rate
	return sc, yes
}

func recordWav(s *systemState) int {
	if s.sc.sampleRate != 48000 || s.sc.format != 16 {
		msg("can only record at 16bit 48kHz")
		return startNewOperation
	}
	dir := "./audio-recordings/"
	f := s.operand + ".wav"
	files, rr := os.ReadDir(dir)
	if e(rr) {
		msg("unable to access '%s': %s", dir, rr)
		return startNewOperation
	}
	for _, file := range files {
		if file.Name() == f {
			msg("file '%s' in %s already exists", f, dir)
			return startNewOperation
		}
	}
	f = dir + f
	wavFile, rr = os.Create(f)
	if e(rr) {
		msg("%v", rr)
		return startNewOperation
	}
	wavFile.Write(wavHeader)
	for i := 0; i < 9600; i++ {
		binary.Write(wavFile, BYTE_ORDER, int16(0))
	}
	record = yes
	msg("now recording to '%s', ends on exit", f)
	return startNewOperation
}

func writeWav(L, R float64) {
	binary.Write(wavFile, binary.LittleEndian, int16(L))
	binary.Write(wavFile, binary.LittleEndian, int16(R))
}
func closeWavFile() {
	wavFile.Close()
}

// loads Syntə functions from file in project root called 'functions.json'
func loadFunctions(data *map[string]fn) {
	f := "functions.json"
	j, rr := os.ReadFile(f)
	rr2 := json.Unmarshal(j, data)
	if e(rr) || e(rr2) {
		msg("Error loading '%s': %v %v", f, rr, rr2)
	}
}

// used for saving info, listings, functions and code recordings (not audio)
func saveJson(data interface{}, f string) bool {
	j, rr := json.MarshalIndent(data, "", "\t")
	if e(rr) {
		msg("Error encoding '%s': %v", f, rr) 
		return false
	}
	return save(j, f)
}

func save(data []byte, file string) bool {
	if rr := os.WriteFile(file, data, 0644); e(rr) {
		msg("Error saving '%s': %v", file, rr)
		return false
	}
	return true
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
		n, err := io.ReadFull(r, data)
		if errors.Is(err, io.ErrUnexpectedEOF) {
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
		sr := binary.LittleEndian.Uint32(data[24:28])
		if sr%22050 != 0 && sr%48000 != 0 {
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
			wav.Data = decodeInt16(rb, file, make([]int16, to), float64(MaxInt16), to, channels)
		case 24:
			d := make([]byte, 0, len(data)*2)
			for i := 44; i < len(data)-3; i += 3 { // byte stuffing
				word := append(data[i:i+3], byte(0))
				d = append(d, word...)
			}
			rb = bytes.NewReader(d)
			wav.Data = decodeInt32(rb, file, make([]int32, to), float64(MaxInt32), to, channels)
		case 32:
			wav.Data = decodeInt32(rb, file, make([]int32, to), float64(MaxInt32), to, channels)
		default:
			msg("%s: needs to be 32, 24 or 16 bit", file)
			continue
		}
		l := len(file)
		wav.Name = strings.ReplaceAll(file[:l-4], " ", "")
		w = append(w, wav)
		r.Close()
		t := float64(len(wav.Data)) / float64(sr)
		c := "stereo"
		if channels == 1 {
			c = "mono  "
		}
		info <- sf("%s\t%2dbit  %3gkHz  %s  %.3gs", file, bits, float64(sr)/1000, c, t)
	}
	if len(w) == 0 {
		return nil
	}
	info <- sf("")
	return w
}

func decodeInt16(rb *bytes.Reader, file string, samples []int16, factor float64, to, channels int) []float64 {
	rr := binary.Read(rb, binary.LittleEndian, &samples)
	if e(rr) && !errors.Is(rr, io.ErrUnexpectedEOF) {
		msg("error decoding: %s %s", file, rr)
		return nil
	}
	// convert to syntə format
	wav := make([]float64, 0, to)
	for i := 0; i < to-channels+1; i += channels {
		var s float64
		if channels == 2 {
			s = (float64(samples[i]) + float64(samples[i+1])) / (2 * factor) // convert to mono
		} else {
			s = float64(samples[i]) / factor
		}
		wav = append(wav, s)
	}
	return wav
}

func decodeInt32(rb *bytes.Reader, file string, samples []int32, factor float64, to, channels int) []float64 {
	rr := binary.Read(rb, binary.LittleEndian, &samples)
	if e(rr) && !errors.Is(rr, io.ErrUnexpectedEOF) {
		msg("error decoding: %s %s", file, rr)
		return nil
	}
	// convert to syntə format
	wav := make([]float64, 0, to)
	for i := 0; i < to-channels+1; i += channels {
		var s float64
		if channels == 2 {
			s = (float64(samples[i]) + float64(samples[i+1])) / (2 * factor) // convert to mono
		} else {
			s = float64(samples[i]) / factor
		}
		wav = append(wav, s)
	}
	return wav
}

// quick and basic decode of mouse bytes
func mouseRead() {
	var file string
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
	bytes := make([]byte, 3) // shadows package name
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

// scan stdin from goroutine to allow external concurrent input
func readInput() {
	s := bufio.NewScanner(os.Stdin)
	s.Split(bufio.ScanWords)
	for !exit {
		s.Scan() // blocks on stdin
		tokens <- token{s.Text(), rpl, not}
	}
}

// shorthand, prints to stdout
func p(i ...interface{}) {
	fmt.Println(i...)
}

// shorthand, prints to stdout
func pf(s string, i ...interface{}) {
	fmt.Printf(s, i...)
}

// poll '.temp/*.syt' modified time and reload if changed
func reloadListing() {
	l := 0
	stat := make([]time.Time, 0)
	for {
		time.Sleep(32361 * time.Microsecond) // coarse loop timing
		lockLoad <- struct{}{}
		for ; l < len(transfer.Listing); l++ {
			stat = append(stat, time.Time{})
		}
		for i := 0; i < l; i++ {
			f := sf(".temp/%d.syt", i)
			st, rm := os.Stat(f)
			if e(rm) || st.ModTime().Equal(stat[i]) {
				continue
			}
			if stat[i].IsZero() { // initialise new listings for next loop
				stat[i] = st.ModTime()
				continue
			}
			tokens <- token{"rld", i, yes}
			tokens <- token{sf("%d", i), i, yes}
			stat[i] = st.ModTime()
		}
		<-lockLoad
	}
}

// rootsync can be used to synchronise two instances of Syntə, may be depricated in future
func rootSync() bool {
	f := "../infodisplay.json"
	d := disp{}
	s := not
	info <- "> waiting to sync"
	for {
		j, rr := os.ReadFile(f)
		if e(rr) {
			info <- sf("error reading: %v", rr)
			rs = not
			return false
		}
		json.Unmarshal(j, &d) // too many spurious errors
		if d.Sync && !s {
			break
		}
		s = d.Sync
	}
	rs = not
	info <- "< synced to root"
	return true
}

// clear screen, print header and what has been entered so far
func displayHeader(sc soundcard, wavNames string, t systemState) {
	pf("%s\033[H\033[2J", reset) // this clears prior error messages!
	pf(">  %dbit %2gkHz %s\n", sc.format, SampleRate/1000, sc.channels)
	pf("%sSyntə%s running...\n", cyan, reset)
	pf("Always protect your ears above +85dB SPL\n\n")
	if len(wavNames) > 0 {
		pf(" %swavs:%s %s\n\n", italic, reset, wavNames)
	}
	l := len(t.dispListings)
	if reload > -1 {
		l = reload
	}
	pf("\n%s%d%s:", cyan, l, reset)
	for i, o := range t.dispListing {
		switch t.dispListing[i].Op {
		case "in", "pop", "index", "[", "]", "from", "all":
			pf("\t  %s%s %s%s\n", yellow, o.Op, o.Opd, reset)
		default:
			if _, f := t.funcs[t.dispListing[i].Op]; f {
				pf("\t\u21AA %s%s %s%s%s\n", magenta, o.Op, yellow, o.Opd, reset)
				continue
			}
			pf("\t\u21AA %s%s %s%s\n", yellow, o.Op, o.Opd, reset)
		}
	}
	pf("\t  ")
}

func selectOutput(bits int) func(w io.Writer, f float64) {
	output := func(w io.Writer, f float64) {
		//binary.Write(w, BYTE_ORDER, int16(f))
		w.Write([]byte{byte(uint32(f)), byte(uint32(f) >> 8)}) // errors ignored
	}
	switch bits {
	case 8:
		output = func(w io.Writer, f float64) {
			//binary.Write(w, BYTE_ORDER, int8(f))
			w.Write([]byte{byte(f)}) // errors ignored
		}
	case 16:
		// already assigned
	case 32:
		output = func(w io.Writer, f float64) {
			//binary.Write(w, BYTE_ORDER, int32(f))
			w.Write([]byte{byte(uint32(f)), byte(uint32(f) >> 8), byte(uint32(f) >> 16), byte(uint32(f) >> 24)}) // errors ignored
		}
	default:
		msg("unable to write to soundcard!")
		return nil
	}
	return output
}

func startProfiling(prof bool) {
	if !prof {
		return
	}
	f, rr := os.Create("cpu.prof")
	if e(rr) {
		msg("no cpu profile: %v", rr)
		return
	}
	defer f.Close()
	if rr := pprof.StartCPUProfile(f); e(rr) {
		msg("profiling not started: %v", rr)
		return
	}
	defer pprof.StopCPUProfile()
}

func ls(s *systemState) int {
	if s.operand == "l" {
		s.operand += "istings"
	}
	dir := "./" + s.operand
	files, rr := os.ReadDir(dir)
	if e(rr) {
		msg("unable to access '%s': %s", dir, rr)
		return startNewOperation
	}
	extn := ".syt"
	if dir == "./wavs" {
		extn = ".wav"
	}
	ls := ""
	for _, file := range files {
		f := file.Name()
		if filepath.Ext(f) != extn {
			continue
		}
		ls += f[:len(f)-4] + "  "
	}
	if len(ls) == 0 {
		msg("no files")
		return startNewOperation
	}
	msg("%s", ls)
	msg("")
	return startNewOperation
}

func saveTempFile(r bool, t systemState) {
	if r { // hacky conditional
		return
	}
	// save listing as <n>.syt for the reload
	f := sf(".temp/%d.syt", len(transfer.Listing)-1)
	content := ""
	for _, d := range t.dispListing {
		content += d.Op
		if y := t.hasOperand[d.Op]; y {
			content += " " + d.Opd
		}
		content += "\n"
	}
	if rr := os.WriteFile(f, []byte(content), 0666); e(rr) {
		msg("%v", rr)
	}
}

func loadUsage() map[string]int {
	u := map[string]int{}
	f, rr := os.Open("usage.txt")
	if e(rr) {
		//msg("%v", rr)
		return u
	}
	s := bufio.NewScanner(f)
	s.Split(bufio.ScanWords)
	for s.Scan() {
		op := s.Text()
		if op == "unused:" {
			break
		}
		s.Scan()
		n, rr := strconv.Atoi(s.Text())
		if e(rr) {
			//msg("usage: %v", rr)
			continue
		}
		u[op] = n
	}
	return u
}

type pair struct {
	Key   string
	Value int
}
type pairs []pair

func saveUsage(u map[string]int, t systemState) {
	p := make(pairs, len(u))
	i := 0
	for k, v := range u {
		p[i] = pair{k, v}
		i++
	}
	sort.Slice(p, func(i, j int) bool { return p[i].Value > p[j].Value })
	data := ""
	for _, s := range p {
		data += sf("%s %d\n", s.Key, s.Value)
	}
	data += "\nunused:\n"
	data += "\n~operators~\n"
	for op := range operators {
		if _, in := u[op]; !in {
			data += sf("%s\n", op)
		}
	}
	data += "\n~functions~\n"
	for f := range t.funcs {
		if _, in := u[f]; !in {
			data += sf("%s\n", f)
		}
	}
	if rr := os.WriteFile("usage.txt", []byte(data), 0666); e(rr) {
		msg("%v", rr)
	}
}

func loadReloadAppend(t *systemState) int {
	switch t.operator {
	case "rld", "r":
		n, rr := strconv.Atoi(t.operand) // allow any index, no bounds check
		if e(rr) || n < 0 {
			msg("%soperand not valid:%s %s", italic, reset, t.operand)
			return startNewOperation
		}
		reload = n
		t.operand = ".temp/" + t.operand
	case "apd":
		reload = -1
		t.operand = ".temp/" + t.operand
	}
	inputF, rr := os.Open(t.operand + ".syt")
	if e(rr) {
		msg("%v", rr)
		reload = -1
		return startNewOperation
	}
	s := bufio.NewScanner(inputF)
	s.Split(bufio.ScanWords)
	for s.Scan() {
		tokens <- token{s.Text(), reload, yes}
	}
	inputF.Close()
	tokens <- token{"_", -1, not} // reset header
	return startNewListing
}

