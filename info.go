package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"time"
)

// terminal colours
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
	invert  = "\x1b[41m"
)

func main() {

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
		SR: 48000,
	}

	type message struct {
		Content string
		Added   time.Time
	}
	messages := make([]message, 11)

	timeout := 2
	clip := ""
	info := ""
	unprotected := ""
	paused := ""

	file := "infodisplay.json"

	var start time.Time
	var timer time.Duration
	var started bool
	var exit bool
	stop := make(chan struct{})

	go func() { // anonymous to include above variables in scope
		for {
			Json, err := os.ReadFile(file)
			err2 := json.Unmarshal(Json, &display)
			if err != nil || err2 != nil {
				//fmt.Printf("error loading %s: %v %v\n", file, err, err2)
				//fmt.Println(display)
				//fmt.Scanln()
				//time.Sleep(2 * time.Second)
			}
			fmt.Printf("\033[H\033[2J")
			fmt.Printf("%sSyntə info%s %spress enter to quit%s", cyan, reset, italic, reset)

			if display.Paused {
				paused = green + "paused" + reset
			} else {
				paused = ""
			}
			if display.List > 0 {
				if !started {
					start = time.Now()
					started = true
				}
				timer = time.Since(start).Round(time.Second)
			} else { // timer for continuous play
				timer = 0
				started = false
				info = "\n"
			}

			if display.Mode == "on" {
				display.Mode = italic + "funcsave: " + reset + display.Mode
			} else {
				display.Mode = "\t"
			}

			loadColour := ""
			load := 0.0
			if display.List > 0 {
				load = float64(display.Load) / (1e9 / display.SR)
			}
			if load > 0.9 {
				loadColour = red
			}
			L := fmt.Sprintf("%s%0.2f%s", loadColour, load, reset)

			if display.Info != messages[10].Content {
				m := message{display.Info, time.Now()}
				messages = append(messages, m)
				messages = messages[1:]
			}
			if display.Info == "clear" {
				for i := range messages {
					messages[i].Content = ""
				}
			}
			/*for i, m := range messages {
				if time.Since(m.Added) > (60*time.Duration(i)+60)*time.Second {
					m.Content = ""
				}
				messages[i] = m
			}*/
			if !display.Protect && display.List > 0 {
				if display.Clip {
					unprotected = fmt.Sprintf("%sUnprotected%s", invert, reset)
				} else {
					unprotected = fmt.Sprintf("Unprotected")
				}
			} else {
				unprotected = ""
			}
			if display.Clip {
				clip = red
				timeout = 2
			}
			timeout--
			if timeout < 0 {
				timeout = 2
				clip = ""
			}
			gr := ""
			if display.GR {
				gr = yellow + "GR" + reset
			}
			vu := 1 + (math.Log10(display.Vu) / 1.75)
			VU := fmt.Sprintf("\r\t            |                   %s|%s  %s", clip, reset, gr)
			VU += fmt.Sprintf("\r\t   %s %.3f %s  |", green, display.Vu, reset)
			n := int(vu * 20)
			for i := 0; i < n; i++ {
				VU += fmt.Sprintf("|")
			}

			fmt.Printf(`		%s	%3s
╭───────────────────────────────────────────────────╮
	%s		%sLoad:%s %v
%s
%s
%s
%s
%s
%s
%s
%s
%s
%s
%s
%s%s
      %sMouse-X:%s %.3g		%sMouse-Y:%s %.3g
╰───────────────────────────────────────────────────╯`,
				paused, timer,
				display.Mode,
				yellow, reset, L,
				messages[0].Content,
				messages[1].Content,
				messages[2].Content,
				messages[3].Content,
				messages[4].Content,
				messages[5].Content,
				messages[6].Content,
				messages[7].Content,
				messages[8].Content,
				messages[9].Content,
				messages[10].Content,
				VU, unprotected,
				//italic, mutes, reset,
				blue, reset, display.MouseX,
				blue, reset, display.MouseY,
			)

			time.Sleep(20 * time.Millisecond)
			if exit {
				close(stop)
				break
			}
		}
	}()
	if !exit {
		fmt.Printf("press enter to quit")
		fmt.Scanln()
		exit = true
		<-stop
	}
	fmt.Printf("info display closed.")
}
