package main

import (
	"os"

	"github.com/sergii/runwitness/internal/mcpintegration"
	"github.com/sergii/runwitness/internal/queryregressiongateintegration"
)

func main() {
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "mcp" {
		os.Exit(mcpintegration.Main())
	}
	os.Exit(queryregressiongateintegration.Main(args))
}
