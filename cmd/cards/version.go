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

// buildMeta resolves the effective version plus the short commit (+dirty)
// and build time from the ldflags var and the embedded VCS build info.
func buildMeta() (v, commit, buildTime string) {
	v = version
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
	return v, commit, buildTime
}

// shortVersion is the compact one-token-ish form shown in the help header
// and the web UI nav: "v0.2.0 (a1b2c3d4e5f6)" or "dev (a1b2c3d4-dirty)".
func shortVersion() string {
	v, commit, _ := buildMeta()
	if commit != "" {
		return v + " (" + commit + ")"
	}
	return v
}

// versionString renders a one-shot version line: version, short commit
// (+dirty), build time, and the toolchain/platform.
func versionString() string {
	v, commit, buildTime := buildMeta()
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
