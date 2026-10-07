package main

import (
	"fmt"
	"os"

	"dinit/internal/agent"
	"dinit/internal/dinit"
	"dinit/internal/upgrade"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const usage = `usage:
  dinit --version
  dinit upgrade [--source <url|path>] [--version X.Y.Z] [--sha256 <hex>]
  dinit agent attach [--tld <tld>]
  dinit agent serve`

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-version", "--version":
			fmt.Printf("dinit %s (%s, %s)\n", version, commit, date)
			return
		case "upgrade":
			os.Exit(upgrade.Run(version, os.Args[2:]))
		case "agent":
			os.Exit(runAgent(os.Args[2:]))
		case "-h", "--help", "help":
			fmt.Println(usage)
			return
		}
	}
	if os.Getpid() != 1 {
		fmt.Fprintln(os.Stderr, "dinit is a guest init: boot it with init="+dinit.InitPath+", do not run it by hand\n\n"+usage)
		os.Exit(1)
	}
	dinit.Run(version)
}

func runAgent(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "attach":
			return agent.Attach(version, args[1:])
		case "serve":
			return agent.Serve(version)
		}
	}
	fmt.Fprintln(os.Stderr, usage)
	return 2
}
