package main

import (
	"fmt"
	"os"

	"dinit/internal/dinit"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-version" || os.Args[1] == "--version") {
		fmt.Printf("dinit %s (%s, %s)\n", version, commit, date)
		return
	}
	if os.Getpid() != 1 {
		fmt.Fprintln(os.Stderr, "dinit is a guest init: boot it with init="+dinit.InitPath+", do not run it by hand")
		os.Exit(1)
	}
	dinit.Run(version)
}
