package jobs

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/jackjakarta/gocron/internal/crontab"
	"github.com/jackjakarta/gocron/internal/runner"
	"github.com/jackjakarta/gocron/internal/store"
)

// Diagnose suggests why a run failed, from what's knowable without running
// the job again: the exit code, the output, and lookups in cron's
// environment and the user's. env is the environment the run had; userEnv
// is the user's own.
func Diagnose(cmd string, res runner.Result, output string, env, userEnv []string, cache *PathCache) []string {
	if res.Status == store.StatusSuccess || res.Status == store.StatusSkipped {
		return nil
	}
	var out []string
	words, _ := crontab.ShellWords(cmd)
	cronPATH, userPATH := runner.Lookup(env, "PATH"), runner.Lookup(userEnv, "PATH")
	lower := strings.ToLower(output)

	if res.ExitCode == 127 || strings.Contains(lower, "not found") || strings.Contains(lower, "no such file") {
		for _, prog := range programs(words) {
			if builtins[prog] || strings.ContainsAny(prog, "$<>|&;()`") || strings.Contains(prog, "/") {
				continue
			}
			if cache.Look(prog, cronPATH) != "" {
				continue
			}
			if found := cache.Look(prog, userPATH); found != "" {
				dir := filepath.Dir(found)
				out = append(out, fmt.Sprintf(
					"%s is in %s, which isn't on this run's PATH (%s). Use the full path %s, or add PATH=%s:%s to the top of your crontab.",
					prog, dir, cronPATH, found, dir, cronPATH))
			} else {
				out = append(out, prog+" isn't on this run's PATH or yours. Check the spelling, or use the full path.")
			}
		}
	}
	if res.ExitCode == 126 || strings.Contains(lower, "permission denied") {
		out = append(out, "Something in the command isn't executable or readable by you. Check chmod +x on scripts.")
	}

	// Single-quoted text isn't expanded or parsed by the shell (awk and sed
	// programs live there), so it can't be the problem.
	shellText := singleQuoted.ReplaceAllString(cmd, "''")
	for _, v := range missingVars(shellText, words, env, userEnv) {
		out = append(out, fmt.Sprintf("$%s is set in your shell but not here. Add %s=… to your crontab, or set it in the command.", v, v))
	}
	progs := programs(words)
	uses := func(names ...string) bool {
		return slices.ContainsFunc(progs, func(p string) bool { return slices.Contains(names, filepath.Base(p)) })
	}
	if runner.Lookup(env, "DISPLAY") == "" && runner.Lookup(env, "WAYLAND_DISPLAY") == "" &&
		uses("notify-send", "xdg-open", "zenity", "kdialog", "xmessage", "osascript") {
		out = append(out, "Desktop programs need your session's DISPLAY and DBUS_SESSION_BUS_ADDRESS, which cron jobs don't have. Set them in the crontab or the command.")
	}
	if runner.Lookup(env, "SSH_AUTH_SOCK") == "" && runner.Lookup(userEnv, "SSH_AUTH_SOCK") != "" &&
		uses("ssh", "scp", "sftp", "rsync", "git") {
		out = append(out, "ssh can't reach your ssh-agent from cron. Use a key without a passphrase for this job, or keychain.")
	}
	if shell := runner.Lookup(env, "SHELL"); shell == "" || filepath.Base(shell) == "sh" {
		if b := bashism(shellText); b != "" {
			out = append(out, fmt.Sprintf("%s is bash syntax, but cron runs commands with /bin/sh. Add SHELL=/bin/bash to your crontab, or use sh syntax.", b))
		}
	}
	return out
}

var (
	singleQuoted = regexp.MustCompile(`'[^']*'`)
	varRef       = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)`)
	braceList    = regexp.MustCompile(`\{[^{}\s]*,[^{}\s]*\}`)
	sourceWord   = regexp.MustCompile(`(^|[;&|]\s*)source\s`)
	// assignedVar finds NAME= anywhere a command could assign it.
	assignedVar = regexp.MustCompile(`(?:^|[;&|(\s])(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=`)
)

// missingVars lists variables the command uses that the user's shell sets
// but the run's environment doesn't, leaving out ones the command assigns.
func missingVars(cmd string, words []string, env, userEnv []string) []string {
	assigned := map[string]bool{}
	for _, w := range words {
		if assignment.MatchString(w) {
			name, _, _ := strings.Cut(w, "=")
			assigned[name] = true
		}
	}
	for _, m := range assignedVar.FindAllStringSubmatch(cmd, -1) {
		assigned[m[1]] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range varRef.FindAllStringSubmatch(cmd, -1) {
		v := m[1]
		if seen[v] || assigned[v] {
			continue
		}
		seen[v] = true
		if runner.Lookup(env, v) == "" && runner.Lookup(userEnv, v) != "" {
			out = append(out, v)
		}
	}
	return out
}

// bashism returns the first bash-only construct in cmd, or "".
func bashism(cmd string) string {
	switch {
	case strings.Contains(cmd, "[["):
		return "[["
	case strings.Contains(cmd, "&>"):
		return "&>"
	case sourceWord.MatchString(cmd):
		return "source"
	case braceList.MatchString(cmd):
		return braceList.FindString(cmd)
	}
	return ""
}
