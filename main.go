package main

import (
	"fmt"
	"os"

	"herdr-smartnav/internal/history"
	"herdr-smartnav/internal/nav"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: herdr-smartnav <daemon|left|right|up|down>")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "daemon":
		if err := history.RunDaemon(); err != nil {
			fmt.Fprintln(os.Stderr, "daemon:", err)
			os.Exit(1)
		}
	case "left", "right", "up", "down":
		if err := nav.RunAction(os.Args[1]); err != nil {
			fmt.Fprintln(os.Stderr, os.Args[1]+":", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "unknown command:", os.Args[1])
		os.Exit(2)
	}
}
