package runner

import (
	"os"
	"os/user"
	"strings"

	"github.com/jackjakarta/gocron/internal/crontab"
)

// DefaultCronPATH is the PATH cron gives jobs when the crontab sets none.
const DefaultCronPATH = "/usr/bin:/bin"

// Identity is who cron runs jobs as.
type Identity struct {
	Name string // LOGNAME and USER
	Home string
}

// CurrentIdentity is the user running gocron.
func CurrentIdentity() Identity {
	id := Identity{Name: os.Getenv("USER"), Home: os.Getenv("HOME")}
	if u, err := user.Current(); err == nil {
		id.Name, id.Home = u.Username, u.HomeDir
	}
	return id
}

// CronEnv is the environment cron gives a job: nothing inherited, only
// HOME, LOGNAME, USER, SHELL=/bin/sh and PATH=/usr/bin:/bin, then the
// crontab's environment lines in order.
func CronEnv(id Identity, lines []crontab.EnvVar) []string {
	env := []string{
		"HOME=" + id.Home,
		"LOGNAME=" + id.Name,
		"USER=" + id.Name,
		"SHELL=/bin/sh",
		"PATH=" + DefaultCronPATH,
	}
	return Apply(env, lines)
}

// ManualEnv is the environment for "run now": the user's own, with the
// crontab's environment lines on top and cron's shell rather than the
// user's login shell, since the job's command is written for that shell.
func ManualEnv(base []string, lines []crontab.EnvVar) []string {
	return Apply(Set(base, "SHELL", "/bin/sh"), lines)
}

// Apply sets each variable in order; later lines win, as in cron.
func Apply(env []string, lines []crontab.EnvVar) []string {
	for _, l := range lines {
		env = Set(env, l.Name, l.Value)
	}
	return env
}

// Set returns env with key set to value, replacing any earlier entry.
func Set(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); k != key {
			out = append(out, kv)
		}
	}
	return append(out, key+"="+value)
}
