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
)

type muteVerb struct {
	mute    []bool
	verbose bool
}

func main() {
	var (
		file = "displaylisting.json"
		exit bool
		stop = make(chan struct{})
	)

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
				fmt.Printf("\n\n\033[H\033[2J")
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
	err2 := json.Unmarshal(Json, &d)
	if err != nil || err2 != nil {
		//fmt.Printf("error loading %s: %v %v\n", file2, err, err2)
		//time.Sleep(2 * time.Second)
	}
	var m []bool
	err2 = json.Unmarshal(d["Mute"], &m)
	if err2 != nil {
		//fmt.Printf("error decoding %s: %v %v\n", file2, err, err2)
		//time.Sleep(2 * time.Second)
	}
	var v bool
	err2 = json.Unmarshal(d["Verbose"], &v)
	if err2 != nil {
		//fmt.Printf("error decoding %s: %v %v\n", file2, err, err2)
		//time.Sleep(2 * time.Second)
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
	err2 := json.Unmarshal(Json, &listing)
	if err != nil {
		//fmt.Printf("error loading %s: %v %v\n", file, err, err2)
		//time.Sleep(2 * time.Second)
	}
	if err2 != nil {
		//fmt.Printf("error decoding %s: %v %v\n", file, err, err2)
		//time.Sleep(2 * time.Second)
	}
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
			fmt.Printf("%s", list[0].Opd)
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
			case "noise", "sino", "saw", "sqr", "pulse":
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
			case "in", "pop", ")", "index", "ifft", "/b", "all":
				//fmt.Printf(" %s|%s  ", y, reset)
				fmt.Printf("\n\t")
			default:
				//fmt.Printf(" %s\u22A2%s  ", y, reset)
				fmt.Printf("%s,%s  ", italic, reset)
			}
		}
	}
}
