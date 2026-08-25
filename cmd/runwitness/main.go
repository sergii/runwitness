package main

import (
	"os"

	"github.com/sergii/runwitness/internal/gateintegration"
)

func main() {
	os.Exit(gateintegration.Main(os.Args[1:]))
}
