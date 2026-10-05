// Command atto is a terminal coding harness.
package main

import (
	"flag"
	"fmt"
	"os"

	"atto/app"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "history" {
		if err := app.RunHistory(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	showVersion := flag.Bool("version", false, "print version and exit")
	inline := flag.Bool("inline", false, "render inline in the main screen instead of fullscreen")
	prompt := flag.String("p", "", "run a single prompt non-interactively and print the result")
	cont := flag.Bool("c", false, "continue the most recent session in this directory")
	resume := flag.Bool("resume", false, "pick a saved session to resume")
	flag.Parse()
	if *showVersion {
		fmt.Println("atto", app.Version)
		return
	}
	var err error
	if *prompt != "" {
		err = app.RunPrint(*prompt)
	} else {
		err = app.Run(app.Options{Inline: *inline, Continue: *cont, Resume: *resume})
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "atto:", err)
		os.Exit(1)
	}
}
