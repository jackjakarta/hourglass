// Package version reports gocron's build information.
package version

import (
	"fmt"
	"runtime/debug"
	"strings"
)

// Set by the release build: -ldflags "-X github.com/jackjakarta/gocron/internal/version.Version=…".
var (
	Version = ""
	Commit  = ""
	Date    = ""
)

// Info is the version, commit, and build date, falling back to what the Go
// toolchain recorded.
type Info struct{ Version, Commit, Date string }

func Get() Info {
	i := Info{Version, Commit, Date}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if i.Version == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			i.Version = bi.Main.Version
		}
		dirty := false
		for _, s := range bi.Settings {
			switch {
			case s.Key == "vcs.revision" && i.Commit == "":
				i.Commit = s.Value
			case s.Key == "vcs.time" && i.Date == "":
				i.Date = s.Value
			case s.Key == "vcs.modified":
				dirty = s.Value == "true"
			}
		}
		if dirty && Commit == "" && i.Commit != "" {
			i.Commit += "-dirty"
		}
	}
	if i.Version == "" {
		i.Version = "dev"
	}
	return i
}

func (i Info) String() string {
	s := "gocron " + i.Version
	if i.Commit != "" {
		c, dirty := strings.CutSuffix(i.Commit, "-dirty")
		if len(c) > 12 {
			c = c[:12]
		}
		if dirty {
			c += "-dirty"
		}
		s += fmt.Sprintf(" (commit %s", c)
		if i.Date != "" {
			s += ", built " + i.Date
		}
		s += ")"
	}
	return s
}
