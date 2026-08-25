package main

import (
	"os"

	"github.com/sergii/runwitness/internal/railsintegration"
)

func main() {
	os.Exit(railsintegration.Main(os.Args[1:]))
}
