package main

import (
	"fmt"
	"runtime"
)

// Populated by the release build. Local builds remain identifiable as dev.
var version = "dev"
var commit = "unknown"

func runVersion(args []string) {
	fs := newFlagSet("version")
	asJSON := fs.Bool("json", false, "Print machine-readable build information")
	if err := fs.Parse(args); err != nil {
		fail(err, wantsJSON(args), "")
	}
	if fs.NArg() != 0 {
		fail(fmt.Errorf("version takes no positional arguments"), *asJSON, "")
	}
	if *asJSON {
		writeJSON(map[string]string{"version": version, "commit": commit, "os": runtime.GOOS, "arch": runtime.GOARCH})
		return
	}
	fmt.Printf("themis %s (%s, %s/%s)\n", version, commit, runtime.GOOS, runtime.GOARCH)
}
