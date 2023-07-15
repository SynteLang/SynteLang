// Syntə functions
// pretty-prints functions saved in functions.json to stdout
// OR
// with -u flag, processes usage stats and prints to stdout

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
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

type operation struct {
	Op string
	Opd string
}
type listing []operation

type fn struct {
	Comment string
	Body listing
}

func main() {

	var functions map[string]fn

	file := "functions.json"

	Json, err := os.ReadFile(file)
	err2 := json.Unmarshal(Json, &functions)
	if err != nil || err2 != nil {
		fmt.Printf("error loading %s: %v %v", file, err, err2)
	}
	if len(os.Args) > 1 && os.Args[1] == "-u" {
		u := loadUsage()
		for name, n := range u {
			// for each function in usage list
			if _, in := functions[name]; !in {
				continue
			}
			// for each operator in function count usage
			ops := map[string]int{}
			for _, f := range functions[name].Body {
				ops[f.Op]++
			}
			// multiply each op count by function count
			// and add to op count
			for op := range ops {
				u[op] += ops[op] * n
			}
			// delete function from usage list
			delete(u, name)
		}
		fmt.Println(sortUsage(u))
		return
	}
	fmt.Printf("\n%s%sfunctions%s\n", yellow, italic, reset)
	fmt.Println()

	for k, v := range functions {
		listing := v.Body
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

func loadUsage() map[string]int {
	u := map[string]int{}
	f, err := os.Open("usage.txt")
	if err != nil {
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
		n, err := strconv.Atoi(s.Text())
		if err != nil {
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

func (p pairs) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }
func (p pairs) Len() int           { return len(p) }
func (p pairs) Less(i, j int) bool { return p[i].Value > p[j].Value }
func sortUsage(u map[string]int) string {
	p := make(pairs, len(u))
	i := 0
	for k, v := range u {
		p[i] = pair{k, v}
		i++
	}
	sort.Sort(p)
	data := ""
	for i, s := range p {
		data += fmt.Sprintf("%d %d %s\n", i, s.Value, s.Key)
	}
	return data
}
