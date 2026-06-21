package main

import (
	"fmt"
	"os"
)

func main() {
	if err := Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "agentbox: "+err.Error())
		os.Exit(1)
	}
}
