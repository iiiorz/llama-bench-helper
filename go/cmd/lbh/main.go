// Command lbh is the Go beta of llama-bench-helper: a single-binary CLI
// for llama.cpp model download, benchmarking and quality checks.
package main

import (
	"os"

	"github.com/iiiorz/llama-bench-helper/go/internal/lbh"
)

func main() {
	os.Exit(lbh.Run(os.Args[1:]))
}
