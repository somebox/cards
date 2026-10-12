package main

import (
	"flag"
	"fmt"
)

// parseWithDir parses fs over args for a command that takes one optional
// positional directory, accepting flags on either side of it. Go's flag
// package stops at the first non-flag argument, so a plain fs.Parse would
// leave `cards demo ./x --seed=false` with --seed unparsed and silently
// ignored. Anything after the directory that is not a flag is rejected rather
// than dropped.
func parseWithDir(fs *flag.FlagSet, args []string) (string, error) {
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() == 0 {
		return "", nil
	}
	dir := fs.Arg(0)
	if err := fs.Parse(fs.Args()[1:]); err != nil {
		return "", err
	}
	if fs.NArg() > 0 {
		return "", fmt.Errorf("unexpected argument %q (takes one directory)", fs.Arg(0))
	}
	return dir, nil
}
