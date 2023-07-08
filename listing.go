/*	listing.go displays active listings while Syntə is running
	Data transfered via './displaylisting.json'
	File emptied on exit. Check '/recordings' folder to see played listings by timestamp
	Press enter to exit
*/

package main

import (
	"encoding/json"
	"fmt"
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

func main() {

	var listing [][]struct {
		Op  string
		Opd string
	}

	file := "displaylisting.json"
	file2 := "infodisplay.json"
	d := make(map[string]json.RawMessage)

	var exit bool
	stop := make(chan struct{})
	var mute []bool
	var verbose bool

	go func() {
		for {
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
			Json, err = os.ReadFile(file2)
			err2 = json.Unmarshal(Json, &d)
			if err != nil || err2 != nil {
				//fmt.Printf("error loading %s: %v %v\n", file2, err, err2)
				//time.Sleep(2 * time.Second)
			}
			err2 = json.Unmarshal(d["Mute"], &mute)
			if err2 != nil {
				//fmt.Printf("error decoding %s: %v %v\n", file2, err, err2)
				//time.Sleep(2 * time.Second)
			}
			err2 = json.Unmarshal(d["Verbose"], &verbose)
			if err2 != nil {
				//fmt.Printf("error decoding %s: %v %v\n", file2, err, err2)
				//time.Sleep(2 * time.Second)
			}
			fmt.Printf("\033[H\033[2J")
			fmt.Printf("%sSyntə listings%s %spress enter to quit%s", cyan, reset, italic, reset)
			//fmt.Println()

			for i, list := range listing {
				if len(list) < 1 {
					continue
				}
				if list[0].Op == "deleted" {
					continue
				}
				fmt.Printf("\n\n%d:\t", i)
				m, c, y := magenta, cyan, yellow
				if len(mute) >= i+1 { // bounds check
					if mute[i] {
						m, c, y = italic, italic, italic
					}
				}
				for i, v := range list {
					if verbose {
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
					if i == len(list)-1 || verbose {
						continue
					}
					switch list[i+1].Op {
					case "in", "<", "pop", ")", "index", "from", "all", "ifft":
						//fmt.Printf(" %s|%s  ", y, reset)
						fmt.Printf("\n\t")
					default:
						//fmt.Printf(" %s\u22A2%s  ", y, reset)
						fmt.Printf("%s,%s  ", italic, reset)
					}
				}
			}
			time.Sleep(500 * time.Millisecond)
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
	fmt.Printf("display listing closed.\n")
}
