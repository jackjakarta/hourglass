# Contributing to hourglass

Thanks for your interest in contributing! hourglass is a keyboard-first terminal
UI and CLI for the cron jobs on your own machine, built in Go on
[Bubble Tea](https://github.com/charmbracelet/bubbletea). It edits your **real
user crontab**, so please read the safety notes below before testing changes.

## Getting started

You'll need [Go](https://go.dev/dl/) 1.25 installed (see `go.mod`).

```sh
git clone https://github.com/jackjakarta/hourglass.git
cd hourglass
go build ./...                 # build everything
go run ./cmd/hourglass         # run the TUI from source
```

To try your changes without touching your real crontab, point hourglass at a
file, and optionally at a scratch state directory and config:

```sh
HOURGLASS_CRONTAB_FILE=/tmp/try.crontab \
HOURGLASS_STATE_DIR=/tmp/hourglass-state \
HOURGLASS_CONFIG=/tmp/hourglass.yaml \
  go run ./cmd/hourglass
```

**Dependency pins:** `modernc.org/sqlite` is pinned at v1.59.0 and bubbletea at
v2.0.x because newer versions need Go 1.26. Run `go get` / `go mod tidy` with
`GOTOOLCHAIN=go1.25.0`, or tidy will bump them and the `go` line.

## Development workflow

1. Fork the repo and create a branch off `main`.
2. Make your change.
3. Run the checks below and make sure they all pass.
4. Open a pull request against `main` with a clear description of what and why.

## Checks

CI (`.github/workflows/static-checks.yml`) runs gofmt, vet, build, tests, and a
cross-build of the release targets on every pull request to `main`. Run them
locally before opening a PR:

```sh
gofmt -l .        # must print nothing (gofmt -w . to fix)
go vet ./...      # vet
go build ./...    # build
go test ./...     # tests
```

Useful variations:

```sh
go test ./internal/cronexpr -run TestNext   # one package / one test
go test ./internal/tui -update              # rewrite TUI goldens
go test ./internal/crontab -update          # rewrite crontab round-trip goldens
HOURGLASS_PERF=1 go test ./internal/tui -run TestStartupBudget   # startup perf budget
```

If you regenerate goldens, review the diff in `internal/tui/testdata` or
`testdata/crontabs` before committing. Please add or update tests for behavior
changes.

### Testing rules

- **No test may touch the real crontab.** `crontab.NewSystemSource` refuses to
  run under `go test`. Use a `FileSource` in `t.TempDir()`.
- TUI views get the time from `tui.Clock`, never `time.Now()`, so goldens stay
  deterministic. Use the injection points on `tui.Options` instead of depending
  on the machine.
- The crontab parser must round-trip any input byte for byte
  (`Serialize(Parse(x)) == x`). Add a golden in `testdata/crontabs/` for new
  edge cases.

## Code style

- Keep code gofmt-clean (`gofmt -w .`).
- Follow the existing conventions; the architecture and package dependency
  rules are documented in [`CLAUDE.md`](CLAUDE.md), and the original design
  (with per-phase notes on deliberate decisions) is in
  [`docs/archived/spec.md`](docs/archived/spec.md). Both are worth a read before
  larger changes.
- Match the surrounding code's naming, comment density, and idioms.
- `hourglass exec` must never refuse to run the user's command. Errors are
  logged, not fatal.

## Commit messages

This project uses [Conventional Commits](https://www.conventionalcommits.org/)
(`feat:`, `fix:`, `chore:`, …), matching the existing git history.

## Reporting issues

Open an issue describing the problem, what you expected, and steps to reproduce.
For crontab parsing bugs, a minimal crontab snippet that triggers the issue
helps a lot (strip anything private). For security-sensitive reports, please
avoid filing a public issue with exploit details.
