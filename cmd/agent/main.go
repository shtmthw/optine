package main

import (
	"bufio"
	"os"

	"github.com/mattthew/optine/internals/cli"
)

func main() {
	bufioReader := bufio.NewReader(os.Stdin)
	cli.RunCommand("", false, bufioReader)
}
