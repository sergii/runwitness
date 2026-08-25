package main

import (
	"os"

	"github.com/sergii/runwitness/internal/runner"
)

func main() {
	os.Exit(runner.Main(os.Args[1:]))
}
