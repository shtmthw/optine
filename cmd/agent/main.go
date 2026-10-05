package main

import (
	"bufio"
	"log"
	"os"

	"github.com/mattthew/optine/internals/cli"
)

func main() {
	bufioReader := bufio.NewReader(os.Stdin)
	log.Println("optine: type /local to start, /help for commands, /quit to exit")
	for {
		if quit := cli.RunCommand("", false, bufioReader); quit {
			break
		}
	}
}
