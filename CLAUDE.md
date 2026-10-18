# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

hourglass is a Go TUI (Bubble Tea v2) plus CLI that manages the user's **real crontab**. Job definitions live in the crontab itself, and SQLite stores only run history. `docs/archived/spec.md` is the original design spec. Its Progress section has per-phase notes on decisions and deviations, so check it before changing behavior that seems deliberate.

## Commands

```sh
go build ./...                      # build
go test ./...                       # all tests
go test ./internal/cronexpr -run TestNext   # one package / one test
go test ./internal/tui -update      # rewrite TUI goldens (internal/tui/testdata)
go test ./internal/crontab -update  # rewrite crontab round-trip goldens (testdata/crontabs)
HOURGLASS_PERF=1 go test ./internal/tui -run TestStartupBudget   # startup perf budget
gofmt -l . && go vet ./...          # CI fails on any gofmt diff
./build.sh 0.1.0                    # release archives for linux/darwin × amd64/arm64 (CGO_ENABLED=0)
```

To try the app without touching your real crontab: `HOURGLASS_CRONTAB_FILE=/tmp/try.crontab go run ./cmd/hourglass`. `HOURGLASS_STATE_DIR` and `HOURGLASS_CONFIG` override the state directory and config file.

CI (`.github/workflows/static-checks.yml`, on PRs to main) runs gofmt, vet, build, test, and a cross-build of the four targets. Pushing a tag `*.*.*` runs `release.yml`, which builds with `build.sh` and publishes a GitHub release. There is no goreleaser. `install.sh` is served from the repo root via GitHub Pages (CNAME).

**Dependency pins:** the module targets Go 1.25. `modernc.org/sqlite` is pinned at v1.59.0 and bubbletea at v2.0.x, because newer versions need Go 1.26. Run `go get` / `go mod tidy` with `GOTOOLCHAIN=go1.25.0`, or tidy will bump them and the `go` line.

## Architecture

Dependency rule: `tui` → `jobs` → (`crontab`, `store`, `runner`, `cronexpr`, `config`, `notify`). Lower packages never import `tui`. `cronexpr` and `crontab` do no I/O except through `crontab.Source`.

- **`cmd/hourglass`**: subcommand dispatch with stdlib `flag`; no arguments opens the TUI. Exit codes: 0 ok, 1 error, 2 usage, 3 the crontab changed outside hourglass (conflict). `exec.go` is the wrapper that cron actually invokes.
- **`internal/cronexpr`**: hourglass's own Vixie/cronie-compatible parser, `Next`/`Between`, English `Describe`, `ParseNatural` (English → expression), presets, and lint. `robfig/cron/v3` is used **only as a test oracle**. It differs from cron on dow `7`, the dom/dow OR rule, and DST, so don't replace the parser with it. DST behavior deliberately matches cron daemons.
- **`internal/crontab`**: a line-preserving parser and model. Every line keeps its raw text, and `Serialize(Parse(x)) == x` must hold for any input (CRLF, tabs, no trailing newline). This is enforced by goldens in `testdata/crontabs/`. Mutations touch only the affected lines. A managed job is a `# hourglass:job id=… name=…` meta line followed by its cron line. A paused job is prefixed `#hourglass:off `. Unknown meta keys are preserved. A wrapped command is `<abs-path>/hourglass exec [--timeout=…] [--no-overlap] <id> -- '<single-quoted cmd>'`, and `%` is escaped to `\%` on write. `Store.Save` is the safe-write path: it compares a hash and returns `ErrConflict` when the crontab changed outside hourglass, then backs up, writes, and reads back to verify.
- **`internal/jobs`**: the `Service` façade used by both the CLI and the TUI. It handles snapshots, create/update/adopt/delete, a session undo stack, conflicts (`*ConflictError` with `Retry`), revisions/restore, export/import (YAML), `doctor --fix`, run planning (manual, cron-env, and user-env runs), and static failure diagnosis (`Diagnose`). Each logical change is one `apply`, which means one backup and one undo step.
- **`internal/runner`**: `Exec` runs the user's command with `$SHELL -c`, records the run, enforces timeout/no-overlap (flock), and builds the cron environment. **`exec` must never refuse to run the command.** Bad flags, an unusable DB, or an unsafe id all still run the command, and errors are logged to `hourglass-errors.log`. Runs started from the TUI are spawned detached (`SpawnLauncher`, `Setsid`) and outlive it.
- **`internal/store`**: SQLite run history (WAL, `BEGIN IMMEDIATE` writes, retention pruning).
- **`internal/notify`**: desktop and webhook notifications. They fire only from `exec` with `--trigger=cron`, after the run is done, and a notification failure never changes the job's exit code.
- **`internal/tui`**: one root model (`app.go`) owns the shared state (snapshot, stats, size, theme) and routes messages to views in `views/*`. Key handling has a fixed priority: force-quit → palette → diff overlay → confirm → run pane → editor → help → ctrl+k → view capture → globals. Views return request messages synchronously from `Update`, not as Cmds, so the very next key sees the result. All I/O comes back as typed messages.

## Testing conventions

- No test may touch the real crontab. `crontab.NewSystemSource` refuses under `go test` unless `HOURGLASS_ALLOW_SYSTEM_CRONTAB=1`. Use a `FileSource` in `t.TempDir()`.
- Views get the time from `tui.Clock`, never `time.Now()`, so goldens are deterministic. `tui.Options` also has injection points (`PathLint`, `RunEditor`, and others) so goldens don't depend on the machine.
- TUI tests wrap the model in a `spy` that publishes each rendered screen, then wait on screen content. Goldens are unstyled screens.
- CLI tests re-run the test binary as hourglass itself (`HOURGLASS_TEST_MAIN=1` in `TestMain`), so tests for concurrency, signals, and spawning use real separate processes. They also write a quiet config so failing runs don't send desktop notifications.
- `runner.InProcessLauncher` runs a real shell against a real DB for runner/TUI tests.
