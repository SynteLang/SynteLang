//go:build freebsd

package main

import (
	"math"
	"os"
	"syscall"
	"unsafe"
)

const ( // operating system
	// set output only
	IOC_INOUT = 0xC0000000
	// set bit width
	SNDCTL_DSP_SETFMT = IOC_INOUT | (0x04&((1<<13)-1))<<16 | 0x50<<8 | 0x05
	// Format in Little Endian, see BYTE_ORDER
	AFMT_S32_LE  = 0x00001000
	AFMT_S24_LE  = 0x00010000
	AFMT_S16_LE  = 0x00000010
	AFMT_S8      = 0x00000040
	//AFMT_S32_BE = 0x00002000 // Big Endian
	SELECTED_FMT = AFMT_S32_LE
	// for Stereo
	SNDCTL_DSP_CHANNELS = 0xC0045003
	STEREO              = 1
	MONO                = 0
	//CHANNELS            = STEREO
	// set Sample Rate
	// SNDCTL_DSP_SPEED	= IOC_INOUT |(0x04 & ((1 << 13)-1))<<16 | 0x50 << 8 | 0x02
	SNDCTL_DSP_SPEED       = 0xC0045002
	//SAMPLE_RATE            = 48000 //hertz
)

func setupOSS() (setupSoundcard, bool) {
	setup := setupSoundcard{}
	// open audio output (everything is a file...)
	oss, rr := os.OpenFile("/dev/dsp3", os.O_WRONLY, 0644)
	if e(rr) {
		p(rr)
		p("soundcard not available, shutting down...")
		return setup, not
	}
	// set bit format
	var req uint32 = SNDCTL_DSP_SETFMT
	var data uint32 = SELECTED_FMT
	_, _, ern := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(oss.Fd()),
		uintptr(req),
		uintptr(unsafe.Pointer(&data)),
	)
	if ern != 0 {
		p("set format:", ern)
		return setup, not
	}
	convert := func(f float64) []byte { return []byte{} }
	switch data {
	case AFMT_S16_LE:
		setup.format = 16
		convert = convert16
	case AFMT_S24_LE:
		setup.format = 24
		convert = convert24
	case AFMT_S32_LE:
		setup.format = 32
		convert = convert32
	case AFMT_S8:
		setup.format = 8
		convert = convert8
	default:
		pf("Incompatible bit format, change requested format to\n%#08x\nin 'oss.go'\n", data)
		return setup, not
	}
	if data != SELECTED_FMT {
		pf("Requested bit format changed to %dbit\n", setup.format)
	}
	// set channels here, stereo or mono
	req = SNDCTL_DSP_CHANNELS
	data = CHANNELS-1
	_, _, ern = syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(oss.Fd()),
		uintptr(req),
		uintptr(unsafe.Pointer(&data)),
	)
	if ern != 0 || data != CHANNELS-1 {
		pf("\nrequested channels: %d\navailable: %d\n", CHANNELS, data+1)
		return setup, not
	}
	// set sample rate
	req = SNDCTL_DSP_SPEED
	sr := uint32(checkFlag(SAMPLE_RATE))
	data = sr
	_, _, ern = syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(oss.Fd()),
		uintptr(req),
		uintptr(unsafe.Pointer(&data)),
	)
	if ern != 0 {
		p("set rate:", ern) // do something else here
	}
	setup.sampleRate = float64(data)
	if data != sr {
		p("--requested sample rate not accepted--")
		pf("new sample rate: %vHz\n", setup.sampleRate)
	}
	setup.info = sf(`OSS audio backend
	Sample rate: %.f
	Format:      %dbit float
`,
		setup.sampleRate,
		setup.format,
	)

	setup.cln = func() {
		if oss.Close() != nil {
			p("unable to close soundcard")
		}
	}

	four := not
	if len(os.Args) > 1 && ( os.Args[1] == "--mackie" || os.Args[1] == "-m" ){
		four = yes
	}

	setup.output = func(float64) {
		var (
			lpf12kHz = lpf_coeff(OutputFilter, setup.sampleRate)
			loadThresh = loadThreshAt(setup.sampleRate)
			length = writeBufferLen * CHANNELS * (setup.format / 8)
		)
		if four {
			length *= 2
		}
		var buf = make([]byte, 0, length)
		s := <-samples
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
				buf = append(buf, convert(s.left)...)
				buf = append(buf, convert(s.right)...)
				if four {
					buf = append(buf, convert(s.left)...)
					buf = append(buf, convert(s.right)...)
				}
			}
			if len(buf) != length { // fill partial buffer caused by exit
				buf = append(buf, make([]byte, length-len(buf))...)
			}
			if _, err := oss.Write(buf); err != nil {
				pf("Write error occurred! Please restart\n")
				pf("err: %s\n", err)
				os.Exit(1) // instead of panic which would result in restart
			}
			buf = buf[:0] //make([]byte, 0, length)
			started = yes
		}
	}

	return setup, yes
}

func convert8(f float64) []byte {
	f = clip(f) // clip will display info
	f *= math.MaxInt8 
	v := int8(f)
	return []byte{byte(v)}
}

func convert16(f float64) []byte {
	b := make([]byte, 2)
	f = clip(f) // clip will display info
	f *= math.MaxInt16 
	v := int16(f)
	b[1] = byte(v>>8)
	b[0]   = byte(v)
	return b
}

func convert24(f float64) []byte {
	b := make([]byte, 3)
	f = clip(f) // clip will display info
	f *= math.MaxInt32 
	v := int32(f)
	// ignore soundcard.h - 24bits aligned
	b[2] = byte(v>>24)
	b[1] = byte(v>>16)
	b[0] = byte(v>>8)
	return b
}

func convert32(f float64) []byte {
	b := make([]byte, 4)
	f *= math.MaxInt32 
	v := int32(f)
	b[3] = byte(v>>24)
	b[2] = byte(v>>16)
	b[1] = byte(v>>8)
	b[0]   = byte(v)
	return b
}
