// info.go displays information about a running instance of Syntə
// Press enter to quit

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

	type Disp struct { // TODO import this from a types package
		On      bool
		Mode    string // func add fon/foff
		Vu      float64
		Clip    bool
		Load    time.Duration
		Info    string
		MouseX  float64
		MouseY  float64
		Paused  bool
		Mute    []bool
		SR      float64
		GR      bool
		Sync    bool
		v       bool
		Format  int
		Channel string
	}
	var display = Disp{
		SR: 48000,
	}

	type message struct {
		Content string
		Added   time.Time // redundant
	}
	messages := make([]message, 11)

	clip := ""
	paused := ""
	sync := " "
	GRhold := 0

	file := "infodisplay.json"

	var start time.Time
	var timer time.Duration
	var started bool
	var exit bool
	stop := make(chan struct{})
	load := ""
	overload := 0

	go func() { // anonymous to include above variables in scope
		n := 0
		dB := "     "
		for {
			Json, err := os.ReadFile(file)
			json.Unmarshal(Json, &display)
			//if err != nil || err2 != nil {
			if err != nil { // ignore unmarshal errors
				messages[9].Content = fmt.Sprintf("error loading %s: %v\n", file, err)
				//messages[10].Content = "info display out of order"
			}

			if display.Paused {
				paused = green + "paused" + reset
			} else {
				paused = "      "
			}
			if display.On {
				if !started {
					start = time.Now()
					started = true
				}
				timer = time.Since(start).Round(time.Second)
			} else { // timer for continuous play
				started = false
			}

			sync = " "
			if display.Sync {
				sync = fmt.Sprintf("%s●%s", yellow, reset)
			}

			if display.Mode == "on" {
				display.Mode = italic + "funcsave: " + reset + display.Mode
			} else {
				display.Mode = "\t"
			}

			loadColour := ""
			l := float64(display.Load) / (1e9 / display.SR)
			if overload == 0 {
				load = fmt.Sprintf("%0.2f", l)
			}
			if !started {
				load = "0"
			}
			if overload > 0 {
				overload--
			}
			if l > 1 {
				load = "OVLD"
				overload = 50
			}
			if l > 0.9 {
				loadColour = red
			}
			L := loadColour + load + reset

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
			clip = ""
			if display.Clip {
				clip = red
			}
			if display.GR {
				GRhold = 5
			}
			gr := ""
			if GRhold > 0 {
				gr = yellow + "GR" + reset
				GRhold--
			}
			db := math.Log10(display.Vu)
			if math.IsInf(db, -1) {
				db = -6
			}
			if n%10 == 0 {
				dB = fmt.Sprintf("%-+5.3g", math.Round(db*20))
				if db <= -6 {
					dB = "     "
				}
			}
			n++
			vu := 1 + (db / 2.5)
			if vu > 1 {
				vu = 1
			}
			VU := fmt.Sprintf("\r          |                         %s|%s  %s", clip, reset, gr)
			VU += fmt.Sprintf("\r           %s%s%s|", green, dB, reset)
			nn := int(vu*20 - 0.5)
			for i := 0; i < nn; i++ {
				VU += "|"
			}

			soundcard := fmt.Sprintf("%dbit %2gkhz %s", display.Format, display.SR/1000, display.Channel)
			if display.Format == 0 {
				soundcard = "\t\t"
			}

			fmt.Printf("\033[H\033[2J")
			fmt.Printf("%sSyntə info%s %spress enter to quit%s", cyan, reset, italic, reset)
			fmt.Printf(`   %s   %s  %3s
╭───────────────────────────────────────────────────╮
   %sLoad:%s %v      %s     %s
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
%s
      %sMouse-X:%s %.5g		%sMouse-Y:%s %.5g
╰───────────────────────────────────────────────────╯`,
				sync, paused, timer,
				yellow, reset, L, display.Mode, soundcard,
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
				VU,
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
		fmt.Scanln()
		exit = true
		<-stop
	}
	fmt.Printf("info display closed.\n")
}
