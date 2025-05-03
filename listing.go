/*	listing.go displays active listings while Syntə is running
	Data transferred via './displaylisting.json' and 'infodisplay.json'
	File emptied on exit. Check '/recordings' folder to see played listings by timestamp
	Press enter to exit
*/

package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"os"
	"time"
)

type muteVerb struct {
	mute    []bool
	verbose bool
}

func listingsDisplay() {
	var (
		file = "displaylisting.json"
		exit bool
		stop = make(chan struct{})
	)
	if _, err := os.Open(file); err != nil {
		fmt.Printf("error: %v\n", err)
		fmt.Println("check you are in the correct directory")
		return
	}

	go func() {
		var (
			t time.Time
			mv muteVerb
		)
		for !exit {
			st, _ := os.Stat(file)
			lt := st.ModTime()
			mvn, ch := mutesOrVerboseChanged(mv)
			if !lt.Equal(t) || ch {
				fmt.Printf("\033[H\033[2J")
				readAndDisplay(file, mvn)
			}
			t = lt
			mv = mvn
			time.Sleep(300 * time.Millisecond)
		}
		close(stop)
	}()
	fmt.Scanln()
	exit = true
	<-stop
	fmt.Printf("display listing closed.\n")
}

func mutesOrVerboseChanged(mv muteVerb) (muteVerb, bool) {
	file2 := "infodisplay.json"
	d := make(map[string]json.RawMessage)
	Json, err := os.ReadFile(file2)
	if err != nil {
		fmt.Printf("error: %v\n", err)
		fmt.Println("check you are in the correct directory")
		return mv, false

	}
	if json.Unmarshal(Json, &d) != nil {
		return mv, false
	}
	var m []bool
	if json.Unmarshal(d["Mute"], &m) != nil {
		return mv, false
	}
	var v bool
	if json.Unmarshal(d["Verbose"], &v) != nil {
		return mv, false
	}
	if slices.Equal(mv.mute, m) && mv.verbose == v {
		return mv, false
	}
	return muteVerb{m, v}, true
}

func readAndDisplay(file string, info muteVerb) {
	var listing [][]struct {
		Op  string
		Opd string
	}

	Json, err := os.ReadFile(file)
	if err != nil {
		return
	}
	json.Unmarshal(Json, &listing) // error unchecked

	fmt.Printf("%sSyntə listings%s %spress enter to quit%s", cyan, reset, italic, reset)

	for i, list := range listing {
		if len(list) < 1 {
			continue
		}
		if list[0].Op == "deleted" {
			continue
		}
		fmt.Printf("\n%d ", i)
		if list[0].Op == "/*" {
			fmt.Printf("%s%s%s", bold, list[0].Opd, reset)
		}
		fmt.Printf("\t")
		m, c, y := magenta, cyan, yellow
		if i < len(info.mute) { // bounds check
			if info.mute[i] {
				m, c, y = italic, italic, italic
			}
		}
		for i, v := range list {
			if i == 0 && list[0].Op == "/*" {
				continue
			}
			if info.verbose {
				fmt.Printf(" %s%d:%s ", italic, i, reset)
			}
			mm := m
			switch v.Op {
			case "noise", "sino", "saw", "sqr", "cv2a":
				mm = y
			}
			fmt.Printf("%s%s%s", mm, v.Op, reset)
			if opd := v.Opd; opd != "" {
				fmt.Printf(" %s%s%s", c, opd, reset)
			}
			if i == len(list)-1 || info.verbose {
				continue
			}
			switch list[i+1].Op {
			case "in", "pop", ")", "index", "ifft", "/b", "/s", "all":
				//fmt.Printf(" %s|%s  ", y, reset)
				fmt.Printf("\n\t")
			default:
				//fmt.Printf(" %s\u22A2%s  ", y, reset)
				fmt.Printf("%s,%s  ", italic, reset)
			}
		}
	}
}
