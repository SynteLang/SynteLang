// Syntə is an audio live coding environment and language
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
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const tempDir = ".temp"

var (
	BYTE_ORDER = binary.LittleEndian // not allowed in constants
	// record indicates recording in progress
	record    bool
	wavHeader = []byte{82, 73, 70, 70, 36, 228, 87, 0, 87, 65, 86, 69, 102, 109, 116, 32, 16, 0, 0, 0, 1, 0, 2, 0, 128, 187, 0, 0, 0, 220, 5, 0, 8, 0, 32, 0, 100, 97, 116, 97, 0, 160, 187, 13} // 32bit stereo PCM 48kHz, 600s
	// wavHeader = []byte{82, 73, 70, 70, 36, 228, 87, 0, 87, 65, 86, 69, 102, 109, 116, 32, 16, 0, 0, 0, 1, 0, 2, 0, 128, 187, 0, 0, 0, 238, 2, 0, 4, 0, 16, 0, 100, 97, 116, 97, 0, 208, 221, 6} // 16bit stereo PCM 48kHz, 600s
	wavFile   *os.File
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
	if os.Args[2] == "44.1" { // for convenience
		os.Args[2] = "44"
	}
	flag, err := strconv.ParseFloat(os.Args[2], 64)
	if err != nil {
		return sr
	}
	switch flag {
	case 48:
		flag = 48000
	case 44:
		flag = 44100
	case 96:
		flag = 96000
	}
	if flag < 12000 || flag > 192000 {
		return sr
	}
    return flag
}

func recordWav(s systemState) (systemState, int) {
	if s.sampleRate != 48000 {
		msg("can only record at 48kHz")
		return s, startNewOperation
	}
	dir := "./audio-recordings/"
	f := s.operand + ".wav"
	files, rr := os.ReadDir(dir)
	if e(rr) {
		msg("unable to access '%s': %s", dir, rr)
		return s, startNewOperation
	}
	for _, file := range files {
		if file.Name() == f {
			msg("file '%s' in %s already exists", f, dir)
			return s, startNewOperation
		}
	}
	f = dir + f
	wavFile, rr = os.Create(f)
	if e(rr) {
		msg("%v", rr)
		return s, startNewOperation
	}
	wavFile.Write(wavHeader)
	for i := 0; i < 9600; i++ {
		binary.Write(wavFile, binary.LittleEndian, int16(0))
	}
	record = yes
	msg("%snow recording to:%s", italic, reset)
	msg("%s", f)
	msg("%s(ends on exit)%s", italic, reset)
	return s, startNewOperation
}

func writeWav(L, R float64) {
	binary.Write(wavFile, binary.LittleEndian, int32(L))
	binary.Write(wavFile, binary.LittleEndian, int32(R))
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
		pf("Error loading '%s': %v %v\n", f, rr, rr2)
	}
}

// used for saving info, listings, functions and code recordings (not audio)
func saveJson(data interface{}, f string) bool {
	j, rr := json.MarshalIndent(data, "", "\t")
	j = bytes.TrimSpace(j)
	j = append(j, []byte("\n")...)
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
		pf("%sno wavs:%s %v\n", italic, reset, rr)
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
	pf("%sProcessing wavs...%s\n", italic, reset)
	for _, file := range filelist {
		r, rr := os.Open("./wavs/" + file)
		if e(rr) {
			msg("error loading: %s %s", file, rr)
			continue
		}
		length := WAV_TIME * 192000 * 2
		data := make([]byte, 44+8*length) // enough for 32bit stereo WAV_TIME @ 192kHz
		n, err := io.ReadFull(r, data)
		//msg("bytes: %d", n)
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
		length = WAV_TIME * int(sr)
		bits := binary.LittleEndian.Uint16(data[34:36])
		to := channels * length
		if len(data) < to {
			to = len(data[44:]) / channels
		}
		rb := bytes.NewReader(data[44:])
		switch bits {
		case 16:
			wav.Data = decodeInt16(rb, file, make([]int16, to), float64(math.MaxInt16), to, channels)
		case 24:
			d := make([]byte, 0, len(data)*2)
			for i := 44; i < len(data)-3; i += 3 { // byte stuffing
				word := append(data[i:i+3], byte(0))
				d = append(d, word...)
			}
			rb = bytes.NewReader(d)
			wav.Data = decodeInt32(rb, file, make([]int32, to), float64(math.MaxInt32), to, channels)
		case 32:
			wav.Data = decodeInt32(rb, file, make([]int32, to), float64(math.MaxInt32), to, channels)
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
			c = "mono"
		}
		msg("%16s   %2dbit  %4.3gkHz  %-6s  %.3gs", wav.Name, bits, float64(sr)/1000, c, t)
	}
	if len(w) == 0 {
		return nil
	}
	return w
}

func decodeInt16(rb *bytes.Reader, file string, samples []int16, factor float64, to, channels int) []float64 {
	rr := binary.Read(rb, binary.LittleEndian, &samples)
	if e(rr) || errors.Is(rr, io.ErrUnexpectedEOF) {
		msg("error decoding: %s %s", file, rr)
		//return nil
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

// scan stdin from goroutine to allow external concurrent input
func readInput(from io.Reader) {
	s := bufio.NewScanner(from)
	s.Split(bufio.ScanWords)
	for !exit {
		s.Scan() // blocks on stdin
		tokens <- token{s.Text(), -1, not}
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

// poll '<tempDir>/*.syt' modified time and reload if changed
func reloadListing() {
	dir := "./"
	files, rr := os.ReadDir(dir)
	if e(rr) {
		msg("unable to access '%s': %s", dir, rr)
		return
	}
	tempExtant := not
	for _, f := range files {
		if f.IsDir() && f.Name() == tempDir {
			tempExtant = yes
		}
	}
	if !tempExtant {
		os.Mkdir(tempDir, 0664)
		pf("\n%q directory added\n", tempDir)
	}
	l := 0
	stat := make([]time.Time, 0)
	for {
		time.Sleep(32361 * time.Microsecond) // coarse loop timing
		lockLoad <- struct{}{}
		for ; l < len(mutes); l++ { // only loops over additional listings, likely just one
			stat = append(stat, time.Time{})
		}
		for i := 0; i < l; i++ {
			f := sf("%s/%d.syt", tempDir, i)
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
			if levels[i] < 0.1 {
				 // to avoid ghost listings from previous level set to zero
				levels[i] = 1
			}
		}
		<-lockLoad
	}
}

func reloadExcept(current, i int) error {
	f, rr := os.Open(sf("%s/%d.syt", tempDir, i))
	if e(rr) {
		return rr
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Split(bufio.ScanWords)
	if i == current {
		infoIfLogging("deleting: %d", i)
		tokens <- token{"deleted", -1, yes}
		return nil
	}
	infoIfLogging("restart: %d", i)
	for s.Scan() { // tokens could block here, theoretically
		tokens <- token{s.Text(), -1, yes}
	}
	return nil
}

// rootsync can be used to synchronise two instances of Syntə, may be deprecated in future
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
	if len(info) < infoBuffer {
		info <- "< synced to root"
	}
	return true
}

func displayHeader() {
	pf("\r->%sSyntə%s\n", cyan, reset)
}

func ls(s systemState) (systemState, int) {
	if s.operand == "l" {
		s.operand += "istings"
	}
	dir := "./" + s.operand
	files, rr := os.ReadDir(dir)
	if e(rr) {
		msg("unable to access '%s': %s", dir, rr)
		return s, startNewOperation
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
		return s, startNewOperation
	}
	msg("%s", ls)
	msg("")
	return s, startNewOperation
}

func saveTempFile(t systemState, l int) {
	if t.newListing[0].Op == "deleted" {
		return
	}
	// save listing as <n>.syt for the reload
	f := sf("%s/%d.syt", tempDir, l)
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

func loadReloadAppend(t systemState) (systemState, int) {
	switch t.operator {
	case "rld", "r":
		n, rr := strconv.Atoi(t.operand) // allow any index, no bounds check
		if e(rr) || n < 0 {
			msg("%soperand not valid:%s %s", italic, reset, t.operand)
			return t, startNewOperation
		}
		t.reload = n
		if len(mutes) > t.reload && !display.Paused {
			mutes[t.reload] = 0
			time.Sleep(10 * time.Millisecond)
		}
		t.operand = tempDir + "/" + t.operand
	case "apd":
		t.reload = -1
		t.operand = tempDir + "/" + t.operand
	}
	inputF, rr := os.Open(t.operand + ".syt")
	if e(rr) {
		msg("%v", rr)
		t.reload = -1
		return t, startNewOperation
	}
	s := bufio.NewScanner(inputF)
	s.Split(bufio.ScanWords)
	for s.Scan() {
		tokens <- token{s.Text(), t.reload, yes}
	}
	inputF.Close()
	return t, startNewListing
}
