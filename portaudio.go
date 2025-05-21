// portaudio backend
package main

import (
	"fmt"
	"strings"
	"time"

	pa "github.com/gordonklaus/portaudio"
)

type format float32 

func setupPortaudio() (setupSoundcard, bool) {
	setup := setupSoundcard{}
	err := pa.Initialize()
	if err != nil {
		pf("unable to setup portaudio:\n%s\n", err)
		return setup, false
	}
	d, err := pa.DefaultOutputDevice()
	if err != nil {
		pf("error opening default output via portaudio:\n%s\n", err)
		return setup, false
	}
	buf := make([]format, writeBufferLen)
	out := make([][]format, 2)
	out[0], out[1] = buf, buf
	var channels int = CHANNELS
	if d.MaxOutputChannels >= 4 {
		channels = 4
		out = make([][]format, 4)
		out[0], out[1], out[2], out[3] = buf, buf, buf, buf
	}
	outBuf := &out
	stream, err := pa.OpenDefaultStream(
		0, channels,
		checkFlag(SAMPLE_RATE),
		writeBufferLen, &out,
	)
	fmt.Printf("\033[2J") // clear terminal
	fmt.Printf("\033[H") // reset cursor
	if err != nil {
		pf("%sunable to setup portaudio%s\n%s\n", bold, reset, err)
		return setup, false
	}
	setup.sampleRate = stream.Info().SampleRate
	setup.format = 32
	api, _ := pa.DefaultHostApi()
	setup.info = sf(`%s
Audio output: %s %s
channels: %d
default SR: %.f
`,
		strings.Split(pa.VersionText(), ",")[0],
		api.Type,
		d.Name,
		channels,
		d.DefaultSampleRate,
	)

	setup.cln = func() {
		stream.Close()
		err := pa.Terminate()
		if err != nil {
			pf("termination error: %s", err)
		}
	}
	setup.output = func(float64) {
		var (
			lpf12kHz = lpf_coeff(12000, setup.sampleRate)
			lpf15Hz = lpf_coeff(15, setup.sampleRate)
			loadThresh = 85 * time.Second / (100 * time.Duration(setup.sampleRate)) // 85%
			period = time.Second / time.Duration(setup.sampleRate-1)
			buffL = make([]format, writeBufferLen)
			buffR = make([]format, writeBufferLen)
		)
		chans := len(*outBuf)
		s := <-samples
		err := stream.Start()
		if err != nil {
			panic(err)
		}
		defer stream.Stop()
		started := not
		for s.running {
			for i := 0; i < writeBufferLen; i++ {
				se, ok := receiveSample(s, loadThresh, period, started, lpf15Hz)
				if !ok {
					return
				}
				s.stereoLpf(se, lpf12kHz)
				if !s.running {
					// erase remaining buffer first
					for j := i; j < writeBufferLen; j++ {
						buffL[j], buffR[j] = 0, 0
					}
					break
				}
				buffL[i] = format(clip(s.left)) // clip will display info
				buffR[i] = format(clip(s.right))
			}
			for i := 0; i < chans; i += 2 {
				(*outBuf)[i] = buffL
				(*outBuf)[i+1] = buffR
			}
			if err := stream.Write(); err != nil {
				panic(sf("\nwrite error - %v", err))
			}
			started = yes
		}
	}

	return setup, true
}

