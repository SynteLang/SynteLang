//go:build !(linux || android)

// sdl2 backend
package main

// typedef unsigned char Uint8;
// void callbackSDL(void *userdata, Uint8 *stream, int len);
import "C"
import (
	"fmt"
	"math"
	"unsafe"

	"github.com/veandco/go-sdl2/sdl"
)

// const DisallowChanges = 0

type stereoOut struct {
	l, r int16
}

var out = make(chan stereoOut)

/*
	example code:
	hdr := reflect.SliceHeader{Data: unintptr(unsafe.Pointer(stream)), Len: n, Cap: n}
	buf := *(*[]C.float)(unsafe.Pointer(&hdr)
*/

//export callbackSDL
func callbackSDL(userdata unsafe.Pointer, stream *C.Uint8, length C.int) {
	hdr := unsafe.Slice(stream, length)
	buf := *(*[]C.short)(unsafe.Pointer(&hdr))
	//buf := *(*[]C.float)(unsafe.Pointer(unsafe.SliceData(*stream)))

	n := int(length) / 2
	for i := 0; i < n; i += 2 {
		o := <-out
		buf[i] = C.short(o.l)
		buf[i+1] = C.short(o.r)
	}
}

func setupSDL() (setupSoundcard, bool) {
	setup := setupSoundcard{}
	err := sdl.Init(sdl.INIT_AUDIO)
	if err != nil {
		pf("unable to initialise sdl: %s", err)
		return setup, false
	}

	sr := checkFlag(SAMPLE_RATE)
	spec := &sdl.AudioSpec{
		Freq:	  int32(sr),
		Format:   sdl.AUDIO_S16LSB, // other formats untested
		Channels: 2,
		Samples:  writeBufferLen,
		Callback: sdl.AudioCallback(C.callbackSDL),
	}
	// obtained := &sdl.AudioSpec{}
	err = sdl.OpenAudio(spec, nil)
	// this will be replaced with: sdl.OpenAudioDevice(nil, spec, obtained, nil, DisallowChanges)
	fmt.Printf("\033[2J") // clear terminal
	fmt.Printf("\033[H") // reset cursor
	if err != nil {
		pf("unable to open sdl audio: %s", err)
		return setup, false
	}

	setup.soundcard = soundcard{sr, 32, outputSDL}
	setup.cln = func() {
		sdl.CloseAudio()
		sdl.Quit()
	}
	setup.info = sf(`SDL audio backend
	Sample rate: %d
	Format:      16bit
	Channels:    %d
`,
		spec.Freq,
		spec.Channels,
	)
	return setup, true
}

func outputSDL(sr float64) {
	defer close(out)
	var (
		lpf12kHz = lpf_coeff(OutputFilter, sr)
		loadThresh = loadThreshAt(sr)
	)
	s := <-samples
	sdl.PauseAudio(false)
	started := not
	for s.running {
		for i := 0; i < writeBufferLen; i++ {
			se, ok := receiveSample(s, loadThresh, started)
			if !ok {
				return
			}
			s.stereoLpf(se, lpf12kHz)
			if !s.running {
				break
			}
			out <- stereoOut{ // clip will display info
				l: int16(clip(s.left)* math.MaxInt16),
				r: int16(clip(s.right)* math.MaxInt16),
			}
		}
		started = yes
	}
}
