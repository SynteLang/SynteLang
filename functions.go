// Syntə functions
// displays functions saved in functions.json

package main

import (
	"encoding/json"
	"fmt"
	"os"
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

	var functions map[string][]struct {
		Op  string
		Opd string
		N   int
	}

	file := "functions.json"

	Json, err := os.ReadFile(file)
	err2 := json.Unmarshal(Json, &functions)
	if err != nil || err2 != nil {
		fmt.Printf("error loading %s: %v %v", file, err, err2)
	}
	fmt.Printf("\n%s%sfunctions%s\n", yellow, italic, reset)
	fmt.Println()

	for k, listing := range functions {
		if len(listing) < 1 {
			continue
		}
		fmt.Printf("\t%s%s%s:\n\t\t  ", yellow, k, reset)
		for i, v := range listing {
			fmt.Printf("%s%s%s", magenta, v.Op, reset)
			for n := 0; n < 5-len(v.Op); n++ {
				fmt.Printf(" ")
			}
			if opd := v.Opd; opd != "" {
				if opd[:1] == "-" {
					fmt.Printf("%s%s%s\n", cyan, opd, reset)
				} else {
					fmt.Printf(" %s%s%s\n", cyan, opd, reset)
				}
			} else {
				fmt.Printf("\n")
			}
			if i < len(listing)-1 {
				if l := listing[i+1].Op; l == "in" ||
					l == "noise" ||
					l == "pop" ||
					l == "tap" {
					fmt.Printf("\t\t  ")
				} else {
					fmt.Printf("\t\t\u21AA ")
				}
			}
		}
		fmt.Printf("\n")
		fmt.Printf("\n")
	}
	for k := range functions {
		fmt.Printf("\t%s%s%s ", italic, k, reset)
	}
}
