package main

import (
	"os"

	"github.com/sergii/runwitness/internal/baselineintegration"
)

func main() {
	os.Exit(baselineintegration.Main(os.Args[1:]))
}
