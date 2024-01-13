// produce test listings from README.md

// usage (from project root): `go run test.go > test.syt`

package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"strings"
)

func main() {
	file := "README.md"
	type Result struct {
		Test []string
	}
	tests := Result{}
	// open README.md
	f, err := os.ReadFile(file)
	if err != nil {
		fmt.Println(err)
	}
	// decode
	err = xml.Unmarshal(f, &tests)
	if err != nil {
		fmt.Printf("error: %v", err)
	}

	for _, t := range tests.Test {
		s := strings.ReplaceAll(t, "[name of wav file]", "local")
		fmt.Printf("%v", s)
	}
	fmt.Println("solo 0")
}
