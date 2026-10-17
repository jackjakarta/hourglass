package main

import (
	"fmt"

	"github.com/jackjakarta/hourglass/internal/version"
)

func (a *app) version(args []string) int {
	fs := a.flags("version", "version")
	if code, ok := parse(fs, args); !ok {
		return code
	}
	fmt.Fprintln(a.stdout, version.Get())
	return exitOK
}
