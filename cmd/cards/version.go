package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// version is the release version, injected at build time via
//
//	go build -ldflags "-X main.version=v1.2.3" ./cmd/cards
//
// For a plain `go build` / `go install` it stays "dev"; the commit and build
// time are then read from the embedded VCS build info instead, so even an
// un-flagged local build reports which revision it came from.
var version = "dev"

// versionString renders a one-shot version line: version, short commit
// (+dirty), build time, and the toolchain/platform.
func versionString() string {
	v := version
	var commit, buildTime string
	dirty := false
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				commit = s.Value
			case "vcs.time":
				buildTime = s.Value
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
		// `go install module@vX.Y.Z` records a clean module version and carries
		// no local VCS info; adopt it. A working-tree `go build` instead has VCS
		// info and only a noisy pseudo-version, so there we keep "dev" + commit.
		if v == "dev" && commit == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			v = bi.Main.Version
		}
	}
	if len(commit) > 12 {
		commit = commit[:12]
	}
	if dirty {
		commit += "-dirty"
	}
	line := "cards " + v
	if commit != "" {
		line += " (" + commit + ")"
	}
	if buildTime != "" {
		line += " built " + buildTime
	}
	return line + "\n  " + runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH
}

func versionCmd() { fmt.Println(versionString()) }
