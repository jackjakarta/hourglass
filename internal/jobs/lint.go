package jobs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/jackjakarta/hourglass/internal/cronexpr"
	"github.com/jackjakarta/hourglass/internal/crontab"
)

// Field is the part of a draft an issue is about.
type Field int

const (
	FieldSchedule Field = iota
	FieldCommand
	FieldTags
)

// Issue is a lint finding about a draft. Errors block saving.
type Issue struct {
	Field Field
	cronexpr.Issue
}

// Command lint codes; schedule codes come from cronexpr.
const (
	CodeEmptyCommand  = "empty-command"
	CodeMultiline     = "multiline-command"
	CodeBadTag        = "bad-tag"
	CodePercent       = "percent"
	CodeBackslashPct  = "backslash-percent"
	CodeUnbalanced    = "unbalanced-quote"
	CodeRelativePath  = "relative-path"
	CodeQuotedTilde   = "quoted-tilde"
	CodeHome          = "home"
	CodeInteractive   = "interactive"
	CodeNotOnCronPath = "not-on-cron-path"
	CodeNotFound      = "not-found"
	CodeNotExecutable = "not-executable"
)

// HasErrors reports whether any issue blocks saving.
func HasErrors(issues []Issue) bool {
	for _, is := range issues {
		if is.Severity == cronexpr.Error {
			return true
		}
	}
	return false
}

// LintDraft checks a draft without touching the filesystem, so it can run
// on every keystroke. LintPath adds the checks that need lookups.
func LintDraft(d Draft) []Issue {
	var out []Issue
	add := func(f Field, sev cronexpr.Severity, code, msg string) {
		out = append(out, Issue{f, cronexpr.Issue{Severity: sev, Code: code, Message: msg}})
	}
	for _, is := range cronexpr.Lint(d.Schedule) {
		out = append(out, Issue{FieldSchedule, is})
	}
	for _, t := range d.Tags {
		if t == "" || strings.ContainsAny(t, ", \t") {
			add(FieldTags, cronexpr.Error, CodeBadTag, fmt.Sprintf("Tag %q can't contain spaces or commas.", t))
		}
	}

	cmd := d.Command
	switch {
	case strings.TrimSpace(cmd) == "":
		add(FieldCommand, cronexpr.Error, CodeEmptyCommand, "Enter the command to run.")
		return out
	case strings.ContainsAny(cmd, "\r\n"):
		add(FieldCommand, cronexpr.Error, CodeMultiline, "A cron command must be a single line. Put longer scripts in a file.")
	}
	if strings.Contains(cmd, `\%`) {
		add(FieldCommand, cronexpr.Warn, CodeBackslashPct, "Cron can't run a backslash right before %: it drops the backslash. Move this into a script.")
	} else if strings.Contains(cmd, "%") {
		add(FieldCommand, cronexpr.Info, CodePercent, "% will be escaped as \\% in the crontab. You don't need to do anything.")
	}

	words, err := crontab.ShellWords(cmd)
	if err != nil {
		add(FieldCommand, cronexpr.Warn, CodeUnbalanced, "This has an "+err.Error()+", so the shell will reject it.")
		return out
	}
	if prog := firstProgram(words); strings.Contains(prog, "/") && !strings.HasPrefix(prog, "/") &&
		!strings.HasPrefix(prog, "~") && !strings.HasPrefix(prog, "$") {
		add(FieldCommand, cronexpr.Warn, CodeRelativePath, fmt.Sprintf(
			"%s is a relative path. Cron runs jobs from your home directory, so use an absolute path.", prog))
	}
	if quotedTilde.MatchString(cmd) {
		add(FieldCommand, cronexpr.Warn, CodeQuotedTilde, "~ isn't expanded inside quotes. Use $HOME there instead.")
	} else if usesHome(cmd) {
		add(FieldCommand, cronexpr.Info, CodeHome, "~ and $HOME work in cron, but your shell profile isn't loaded, so its variables and PATH are missing.")
	}
	seen := map[string]bool{}
	for _, p := range programs(words) {
		name := filepath.Base(p)
		msg, ok := interactive[name]
		if !ok || seen[name] {
			continue
		}
		if name == "ssh" && strings.Contains(cmd, "BatchMode") {
			continue
		}
		seen[name] = true
		add(FieldCommand, cronexpr.Warn, CodeInteractive, msg)
	}
	return out
}

var (
	quotedTilde = regexp.MustCompile(`["']~`)
	bareTilde   = regexp.MustCompile(`(^|[\s=:])~`)
)

func usesHome(cmd string) bool {
	return strings.Contains(cmd, "$HOME") || strings.Contains(cmd, "${HOME}") || bareTilde.MatchString(cmd)
}

var interactive = map[string]string{
	"sudo":   "sudo asks for a password, and cron has no terminal to ask on. Use root's crontab, or allow the command with NOPASSWD.",
	"su":     "su asks for a password, and cron has no terminal to ask on. Use that user's crontab instead.",
	"doas":   "doas asks for a password, and cron has no terminal to ask on. Use root's crontab instead.",
	"passwd": "passwd needs a terminal, which cron jobs don't have.",
	"ssh":    "ssh may ask for a password or to trust a host key, and cron has no terminal. Use key auth and -o BatchMode=yes.",
}

func init() {
	for _, p := range []string{"vim", "vi", "nvim", "nano", "emacs", "less", "more", "top", "htop", "man"} {
		interactive[p] = p + " needs a terminal, which cron jobs don't have."
	}
}

var assignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// programs returns the words in command position: the first word of each
// pipeline stage or list element, after any VAR=value assignments.
func programs(words []string) []string {
	var out []string
	start := true
	for _, w := range words {
		switch {
		case w == "|" || w == "||" || w == "&&" || w == ";" || w == "&":
			start = true
		case start && assignment.MatchString(w):
		case start:
			out = append(out, strings.TrimSuffix(w, ";"))
			start = strings.HasSuffix(w, ";")
		}
	}
	return out
}

func firstProgram(words []string) string {
	if p := programs(words); len(p) > 0 {
		return p[0]
	}
	return ""
}

// builtins are shell builtins and keywords: they never need PATH.
var builtins = map[string]bool{}

func init() {
	for _, b := range strings.Fields(`. : [ [[ { ( ! alias bg break cd command continue echo eval exec exit
		export false fg getopts hash if for while until case read readonly return set shift test
		times trap true type ulimit umask unalias unset wait printf local source time`) {
		builtins[b] = true
	}
}

// LintPath checks that the command's program exists where cron will look
// for it. userPATH is the user's own PATH, used to suggest where a
// program cron can't find lives. Lookups go through cache.
func LintPath(cmd string, env CronEnv, userPATH string, cache *PathCache) []Issue {
	words, err := crontab.ShellWords(cmd)
	if err != nil {
		return nil
	}
	prog := firstProgram(words)
	if prog == "" || builtins[prog] || strings.ContainsAny(prog, "<>|&;()`") {
		return nil
	}
	issue := func(code, msg string) []Issue {
		return []Issue{{FieldCommand, cronexpr.Issue{Severity: cronexpr.Warn, Code: code, Message: msg}}}
	}
	if strings.Contains(prog, "/") {
		path := prog
		for _, prefix := range []string{"~/", "$HOME/", "${HOME}/"} {
			if strings.HasPrefix(prog, prefix) {
				home, err := os.UserHomeDir()
				if err != nil {
					return nil
				}
				path = filepath.Join(home, strings.TrimPrefix(prog, prefix))
			}
		}
		if !filepath.IsAbs(path) || strings.Contains(path, "$") {
			return nil
		}
		switch cache.stat(path) {
		case statMissing:
			return issue(CodeNotFound, prog+" doesn't exist.")
		case statNotExec:
			return issue(CodeNotExecutable, prog+" isn't executable. Run chmod +x on it.")
		}
		return nil
	}
	if strings.Contains(prog, "$") || cache.Look(prog, env.PATH) != "" {
		return nil
	}
	if found := cache.Look(prog, userPATH); found != "" {
		return issue(CodeNotOnCronPath, fmt.Sprintf(
			"%s is in %s, which isn't on cron's PATH (%s). Use %s, or add a PATH= line to your crontab.",
			prog, filepath.Dir(found), env.PATH, found))
	}
	return issue(CodeNotFound, fmt.Sprintf("%s isn't on cron's PATH or yours. Check the spelling, or use the full path.", prog))
}

type statResult int

const (
	statExec statResult = iota
	statMissing
	statNotExec
)

// PathCache remembers program lookups, so the editor can lint on every
// pause in typing without hitting the filesystem each time. It is safe
// for concurrent use.
type PathCache struct {
	mu    sync.Mutex
	look  map[string]string
	stats map[string]statResult
}

// NewPathCache returns an empty cache.
func NewPathCache() *PathCache {
	return &PathCache{look: map[string]string{}, stats: map[string]statResult{}}
}

// Look returns where name is found on the colon-separated pathList, or "".
func (c *PathCache) Look(name, pathList string) string {
	key := name + "\x00" + pathList
	c.mu.Lock()
	found, ok := c.look[key]
	c.mu.Unlock()
	if ok {
		return found
	}
	for _, dir := range filepath.SplitList(pathList) {
		if dir == "" {
			continue
		}
		if p := filepath.Join(dir, name); c.stat(p) == statExec {
			found = p
			break
		}
	}
	c.mu.Lock()
	c.look[key] = found
	c.mu.Unlock()
	return found
}

// stat reports whether path is an executable file.
func (c *PathCache) stat(path string) statResult {
	c.mu.Lock()
	r, ok := c.stats[path]
	c.mu.Unlock()
	if ok {
		return r
	}
	fi, err := os.Stat(path)
	switch {
	case err != nil:
		r = statMissing
	case fi.IsDir() || fi.Mode().Perm()&0o111 == 0:
		r = statNotExec
	default:
		r = statExec
	}
	c.mu.Lock()
	c.stats[path] = r
	c.mu.Unlock()
	return r
}
