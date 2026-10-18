# hourglass

A beautiful, safe, keyboard-first home for your cron jobs.

![hourglass demo](docs/demo.gif)

hourglass is a terminal UI for the cron jobs on your own machine. It edits your
**real user crontab**, so jobs keep running whether hourglass is open or not, and
adds what cron never had: run history, logs, plain-English schedules, a
timeline, linting, notifications and a safety net for every edit.

## Features

- **Readable schedules.** `*/15 9-17 * * 1-5` is shown as "Every 15 minutes, between 09:00 and 17:59, Monday through Friday", with a live countdown to the next run.
- **Write schedules in English.** Type "every weekday at 9am" or "on the 1st and 15th at noon" in the editor, or use the preset builder.
- **Run history and logs.** Every run of a tracked job is recorded: when it ran, how long it took, its exit code and its output. Search the logs right in the TUI.
- **"Works in my shell, fails in cron."** `t` runs a job the way cron would (bare environment, cron's `PATH`). When it fails, hourglass points at the likely cause.
- **Never lose your crontab.** hourglass backs up the crontab before every write, refuses to overwrite changes made outside it, and every change can be undone. The Crontab view lists every revision so you can diff and restore it.
- **Timeline.** See the next 24 hours or 7 days at a glance, with warnings when too many jobs start in the same minute.
- **Notifications.** Get a desktop notification or a webhook POST when a job fails (or recovers).
- **Coexists with `crontab -e`.** Lines hourglass doesn't manage are preserved byte for byte, and its own lines are readable and hand-editable.
- Command palette (`ctrl+k`), mouse support, three themes, `NO_COLOR`, and a single static binary.

## Install

```sh
curl -fsSL https://hourglass.jackjakarta.xyz/install.sh | sh
```

Builds are available for Linux and macOS (`amd64` and `arm64`). The script
verifies checksums and installs to `/usr/local/bin` (or `~/.local/bin`).
Useful environment variables:

- `HOURGLASS_VERSION`: install a specific version (e.g. `v0.1.0`). The default is the latest release.
- `HOURGLASS_INSTALL_DIR`: install to a custom directory.

From source (Go 1.25+):

```sh
go install github.com/jackjakarta/hourglass/cmd/hourglass@latest
```

Tracked jobs call hourglass by its full path, so upgrade by installing to the
same place. If the binary moves, `hourglass doctor --fix` updates your jobs.

## Quick start

```sh
hourglass doctor   # check that cron is running and hourglass can use it
hourglass          # open the TUI
```

Press `n` to create a job, `?` for every key, and `ctrl+k` for the command
palette. To try hourglass without touching your real crontab, point it at a file:

```sh
HOURGLASS_CRONTAB_FILE=/tmp/try.crontab hourglass
```

## How hourglass stores jobs

hourglass keeps everything in your crontab. There is no separate job database
that could drift from it. A managed job is a comment line with its metadata
followed by the cron line:

```crontab
# hourglass:job id=k3f9a2 name="Nightly backup" tags=backup,db
30 2 * * * /usr/local/bin/hourglass exec --timeout=30m --no-overlap k3f9a2 -- 'pg_dump app | gzip > /bk/app-$(date +\%F).sql.gz'
```

- **Tracked jobs** run through `hourglass exec`, which records the run and its output, enforces `--timeout`, and skips a run while the previous one is still going (`--no-overlap`). Your command is stored as one single-quoted shell word and runs with `/bin/sh -c`.
- **Paused jobs** keep their line, commented out with a marker: `#hourglass:off 30 2 * * * …`.
- **Untracked jobs** are plain cron lines under a `# hourglass:job` comment.
- **`%` escaping:** cron turns a bare `%` into a newline, so hourglass writes `\%` and shows you the plain command.
- **Unmanaged entries** (anything else in your crontab) are listed separately and left alone. Press `a` to adopt one.

If `exec` can't reach its database, it still runs your command and exits
with its exit code.

## Run history

hourglass's own files follow XDG:

| Path                                | What                             |
| ----------------------------------- | -------------------------------- |
| `~/.config/hourglass/config.toml`   | configuration                    |
| `~/.local/state/hourglass/runs.db`  | run history (SQLite)             |
| `~/.local/state/hourglass/logs/`    | captured output, per job and run |
| `~/.local/state/hourglass/backups/` | crontab revisions                |

By default hourglass keeps the last 100 runs per job and up to 1 MiB of output
per run. From the CLI, `hourglass logs ID` lists recent runs and
`hourglass logs --run N ID` prints one run's output.

## Notifications

When a job started by cron ends, `hourglass exec` can tell you:

```toml
[notify]
on      = "failure"   # never | failure | always | change (broke, or recovered)
desktop = true        # notify-send on Linux, osascript on macOS
webhook = "https://hooks.example.com/cron"
```

The webhook receives a JSON POST:

```json
{
  "job": "Nightly backup",
  "job_id": "k3f9a2",
  "status": "failed",
  "exit_code": 1,
  "duration": "12.34s",
  "duration_ms": 12340,
  "trigger": "cron",
  "started_at": "2026-10-11T02:30:00Z",
  "host": "box",
  "prev_status": "success",
  "log_tail": "pg_dump: connection refused"
}
```

`status` is `success`, `failed`, `timeout` or `error`. A failing notification
never changes the job's exit code; the error goes to
`~/.local/state/hourglass/hourglass-errors.log`. Runs you start yourself (`r`, `t`,
`hourglass run`) don't notify.

## Configuration

Every key is optional.

```toml
# ~/.config/hourglass/config.toml
theme = "nightshift"            # nightshift | daybreak | mono

[ui]
default_sort         = "next"   # next | name | crontab
congestion_threshold = 3        # timeline warning: jobs starting in the same minute
mouse                = true     # false gives your terminal its own text selection back

[runs]
retention     = 100             # runs kept per job
max_log_bytes = 1048576         # output kept per run
mail_output   = "never"         # never | on_failure | always: echo output so cron's MAILTO mails it

[backups]
keep = 50                       # crontab revisions kept

[defaults]                      # for new and adopted jobs
wrap       = true               # track runs
no_overlap = true
timeout    = ""                 # e.g. "1h"

[notify]
on      = "failure"
desktop = true
webhook = ""
```

`NO_COLOR` forces the mono theme. `HOURGLASS_CRONTAB_FILE`, `HOURGLASS_STATE_DIR`
and `HOURGLASS_CONFIG` override the crontab, state directory and config file.

## Keys

| Key                 | Action                                             |
| ------------------- | -------------------------------------------------- |
| `1`–`4`, `tab`      | Jobs, Timeline, History, Crontab                   |
| `↑↓` / `j k`, `g G` | move                                               |
| `↵`                 | open                                               |
| `n` / `e`           | new job / edit                                     |
| `space`             | pause / resume                                     |
| `r` / `t`           | run now / test in cron's environment               |
| `x`                 | delete                                             |
| `a`                 | adopt an unmanaged entry                           |
| `h`                 | the job's run history                              |
| `/` / `#` / `s`     | filter / filter by tag / cycle sort                |
| `y` / `Y`           | copy command / schedule                            |
| `u`                 | undo the last change                               |
| `E` / `R`           | edit the crontab in `$EDITOR` / restore a revision |
| `ctrl+k`            | command palette                                    |
| `?`                 | help                                               |
| `q`, `ctrl+c`       | quit                                               |

In the editor: `tab` moves between fields, `^s` saves, `^t` tests the draft
in cron's environment, and `↵` on the "say it" line applies an English
schedule. The mouse works for tabs, rows, buttons and scrolling.

## CLI

```
hourglass                         open the TUI
hourglass list [--json] [--all]   list jobs
hourglass add --schedule S -- CMD add a job (--name, --tag, --timeout, --no-overlap, --raw)
hourglass enable|disable|rm ID    resume, pause or delete a job
hourglass run [--cron-env] ID     run a job now, streaming its output
hourglass logs [-n N] [--run R] ID
hourglass describe EXPR           describe a schedule ("*/15 9-17 * * 1-5" or "every weekday at 9am")
hourglass doctor [--fix]          check your cron setup (--fix re-IDs duplicates, repoints stale paths)
hourglass export / import F       YAML export and import of managed jobs
hourglass version
```

Exit codes: 0 ok, 1 error, 2 usage, 3 the crontab changed outside hourglass.

## Uninstall

Remove the binary, plus `~/.config/hourglass` and `~/.local/state/hourglass` if you
want the history gone too. Tracked jobs still call `hourglass exec`, so before
uninstalling, delete them or turn **Track runs** off for each one in the
editor.

## Releasing

Push a version tag. GitHub Actions builds every platform and publishes the
release that `install.sh` downloads:

```sh
git tag v0.1.0
git push origin v0.1.0
```

`./build.sh 0.1.0` builds the same archives locally.

## License

[MIT](LICENSE.md)
