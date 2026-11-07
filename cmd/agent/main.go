package main

import (
	"bufio"
	"os"

	"github.com/mattthew/optine/internal/cli"
)

func main() {
	cli.Start(bufio.NewReader(os.Stdin))
}
