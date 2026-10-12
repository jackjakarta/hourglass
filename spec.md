# gocron — Specification

> **A beautiful, safe, keyboard-first home for your cron jobs.**

gocron is a terminal UI (TUI) written in Go for creating, inspecting, and managing cron jobs on your own machine. It manages your **real user crontab**, so jobs keep running whether or not gocron is open. On top of that it adds what cron has always lacked: run history, logs, readable schedules, a timeline, linting, and a safety net for every edit.

This document is the source of truth for the project. It is built in phases across multiple sessions. **At the start of each session, read this file and the [Progress](#progress) section first. Update the Progress section at the end of each session.**

---

## Progress

| Phase | Title | Status |
|---|---|---|
| 1 | Foundations (core packages + minimal CLI) | ✅ done |
| 2 | TUI shell + Jobs view | ✅ done |
| 3 | Job editor + schedule builder | ✅ done |
| 4 | Runs: exec wrapper, history, logs | ✅ done |
| 5 | Timeline + power tools | ✅ done |
| 6 | Polish, notifications, release | ☐ not started |

Session notes (append newest first):
- _2026-10-11_: **Phase 5 done** (branch `feat/phase-5`). New: `cronexpr.(*Expr).Between` / `crontab.Job.Between` (bounded `iter.Seq`; `Next` shares the walk), cached `time.LoadLocation`; `jobs` `Revisions`/`Restore`/`Replace`/`Disk`/`ValidateEdit`/`Summarize`/`Export`/`PlanImport`/`Import`, `Snapshot.Content`, `crontab.BackupTime`; `tui/views/timeline`, `tui/views/crontab`, `components.Palette`/`FuzzyMatch`/`UnifiedDiff`/`DiffLines`; CLI `export`, `import`. Deps: `aymanbagabas/go-udiff v0.4.1` (was indirect), `gopkg.in/yaml.v3 v3.0.1`.
  - **Timeline:** day mode spans `[now−3h, now+24h)`; the 3h lead-in shows only ✓/✗ marks from `stats[id].Recent` (+ running). Week mode starts at today's midnight, day columns. Lanes: enabled managed jobs, then unmanaged entries (dimmed); paused jobs are left out. `Fires > cols` → band with a count; no fire in the window → "(next: …)"; @reboot → "at boot". Density = distinct jobs per column. Congestion = distinct jobs per minute ≥ `ui.congestion_threshold`, reported as the first minute "(and N more minutes)". Rebuilt only on snapshot/stats/size/mode change or a new minute. Keys: `↑↓` lane, `←→`/`h l` scroll (6h or a day; never before now), `.` now, `d`/`w`, `↵` opens the job (`jobs.Reveal`).
  - **Palette** (`ctrl+k` anywhere, even while a filter is typing; `:` only from the globals, so it stays text in filters). Items are built when it opens and run closures, never replayed keys: actions on the selected job, tabs, new/undo/filter/tag/sort, history filters, timeline day/week, crontab edit/revisions, help/quit, then "Go to job · X" for every job and "History · X" for managed ones. Hints come from the key map. Key order: ForceQuit → palette → diff overlay → confirm → pane → editor → help → ctrl+k → view capture → globals.
  - **Jobs list:** `s` cycles next → name → crontab (initial from `ui.default_sort`, passed as `tui.Options.UI`); the filter line shows `sort: name` when not next. `#` opens a tag picker (the palette component) for an exact tag filter, ANDed with `/`; `esc` clears detail, then text, then tag. `y`/`Y` copy command/schedule with `tea.SetClipboard` (OSC 52).
  - **Crontab view:** preview (left, 55%) + revisions list (right) at ≥ 120 columns; below that the list, with `↵` opening the preview full-screen. A revision row is `B_i` (content before save i) with `Summarize(B_i, next newer or current)`: "what changed after this point". Its preview is the diff current → revision ("RESTORING WOULD"), `d` toggles full text. `R` restores through `jobs.Restore` (an `apply`, so `u` undoes and Reapply means restore again); revisions with problems restore anyway and the confirm lists them. Revisions load on tab entry, after a mutation and after an outside reload; a load is dropped if `Snapshot.Content` changed meanwhile.
  - **`E` ($EDITOR round-trip):** `$VISUAL`, `$EDITOR`, `vi` on a 0600 temp copy (`tui.Options.RunEditor` lets tests replace `tea.ExecProcess`). Unchanged → "No changes". `jobs.ValidateEdit` rejects only problems the original didn't have, matched by (code, line text), never line number; rejected edits get a confirm with up to 8 `line N: …` lines, **Edit again** (safe, reopens the same file) / Discard. Saving uses `jobs.Replace(base, next)`: a conflict there has **no Retry** (a whole-file replace must not be replayed over someone else's change); the temp file is kept and the toast names it.
  - **Conflict dialogs** (mutation and editor) gained a third button, **View diff** (`d`): `components.Confirm.Extra/OnExtra`, laid out `[Extra] Safe Action`, focus still starts on Safe. The overlay diffs gocron's loaded copy against `Service.Disk`; `esc` returns to the dialog.
  - **export/import:** YAML `version: 1`, `jobs: [{id, name, desc, tags, schedule, command, enabled, wrapped, timeout, no_overlap, tz}]` with a two-line comment header (no timestamp, so re-exports are byte-identical). Env lines aren't exported; `tz` is informational and import warns when a job lands under a different CRON_TZ. Import merges by id (user's choice): new ids are created with that id, differing jobs updated (wrapped lines keep their binary path), identical ones untouched, nothing deleted; one `apply` = one backup and one undo step. `ParseExport` uses `KnownFields(true)`, version 1 only, ids must pass `runner.ValidID` and be unique. Every job is linted with `LintDraft` before anything is written. Exit 1 on bad input, 3 on conflict. `import -` reads stdin (`app.stdin`).
  - **Help overlay** now takes a max width and wraps its six groups onto rows (fits 100×32).
  - **Tests:** the TUI harness waits for the daemon probe before snapshotting (a pre-existing golden flake). Crontab goldens seed fixed-name backups; backups made during a test use the real clock.
  - **Not done:** CLI `enable`/`disable`/`rm` from §7 still don't exist. `y` only works in the Jobs view (not Timeline/Crontab). The palette has no match highlighting.
- _2026-10-11_: **Phase 4 done** (branch `feat/phase-4`). New packages: `config` loading (`config.Load`, TOML via `BurntSushi/toml v1.6.0`), `store` (SQLite run history), `runner` (`Exec`, cron env, launchers), `lockfile` (flock). New: `jobs.Diagnose`/`Plan`/`Launch`/`Stats`/`History`, `tui/views/runpane`, `tui/views/history`, CLI `exec`, `run`, `logs`. New doctor checks: config, state directory, run history, run tracking, state seen by cron.
  - **Deps:** `modernc.org/sqlite v1.59.0` is pinned because v1.60+ needs Go 1.26. Run `go get`/`go mod tidy` with `GOTOOLCHAIN=go1.25.0`, or tidy will jump to the newest sqlite and bump the `go` line.
  - **Wrapping is on.**
    - `WrapNew` and `adoptWraps` are gone. New and adopted jobs follow `[defaults] wrap/no_overlap/timeout`.
    - `jobs.CheckBin` refuses to wrap with a binary not named `gocron`, or one under `os.TempDir()` (a `go run` build). The editor greys out the Options row and shows the reason. `add` exits 1 and suggests `--raw`.
    - `add`: config defaults apply unless a flag is set (`fs.Visit`). `--timeout` or `--no-overlap` turns tracking on even if `wrap = false`.
    - `cmd/gocron` `app.bin` lets tests inject the path.
  - **SQLite:**
    - Each connection uses `_busy_timeout=5000&_txlock=immediate&_sync=NORMAL`.
    - Schema creation and the one-time switch to WAL run under a flock on `locks/.db.lock`. Switching the journal mode can fail with BUSY even when a busy timeout is set.
    - Writes are `BEGIN IMMEDIATE`. `INSERT … RETURNING` and `DELETE … RETURNING log_path` are used for prune.
    - A newer `schema_version` is best-effort, not an error.
  - **exec never refuses to run the command.**
    - Arguments are parsed leniently: unknown flags and a bad `--timeout` are logged to `gocron-errors.log` (rotated at 1 MiB).
    - An id that isn't path-safe runs unrecorded. The id check is `[A-Za-z0-9][A-Za-z0-9_-]*`, not just base32, because fixtures and hand edits use ids like `k3f9a2`.
    - If the DB is unusable, the job runs unrecorded with its output on stdout, like plain cron.
    - Signals are caught first thing, with `signal.Notify` and never `Ignore`, which the child would inherit. SIGPIPE is caught so a closed `--tee` reader can't kill exec.
    - Exit codes: timeout 124, a shell that won't start 127 (status `error`), a signal 128+n. Stopping a run (`x`) records `failed`/143. `StatusLost = "unknown"` is only computed at read time, through an injectable `Alive`.
    - Hidden flags: `--no-record`, and `--status-fd=N`. On the status fd, exec writes `ready`, `start <id>` and `done <status> <exit> <ms>`.
  - **Runs from the TUI outlive it** (user's choice).
    - `runner.SpawnLauncher` starts `gocron exec --tee --status-fd=3` with `Setsid`. It passes `GOCRON_STATE_DIR` and `GOCRON_CONFIG` explicitly, and holds back a stop until exec says `ready`.
    - Tests use `runner.InProcessLauncher`, which runs a real shell and a real DB, or a fake built with `runner.NewHandle`.
    - CLI tests run the test binary as gocron itself (`GOCRON_TEST_MAIN=1` in `TestMain`), so the concurrency, SIGPIPE and spawn tests use separate processes.
  - **Environments** (`jobs.Plan`):
    - Run now (`RunManual`) uses the user's env plus the crontab's env lines, `SHELL` from the crontab (default `/bin/sh`, never the user's zsh), from `$HOME`.
    - Test (`RunCronEnv`) uses `env -i` style: HOME, LOGNAME, USER, `SHELL=/bin/sh`, `PATH=/usr/bin:/bin`, then the crontab's lines in order (`Snapshot.EnvAt`).
    - Compare (`RunUserEnv`) uses the user's env untouched.
    - Unmanaged entries and unsaved drafts run unrecorded. Managed jobs that aren't wrapped still record their manual and test runs.
  - **Deviation from §4.3:** a failed test is diagnosed **statically** (`jobs.Diagnose`), because rerunning could repeat side effects (user's choice).
    - It checks every program in the pipeline against both PATHs (exit 127 or "not found"), looks for `$VAR`s set only in the user's env, DISPLAY/DBUS for desktop tools, ssh-agent, and bashisms under `/bin/sh`. Single-quoted text is ignored.
    - The pane offers **c** to rerun the job in the user's shell and compare, opt-in.
  - **TUI:**
    - The run pane (`views/runpane`) is a modal sized to its content. Output passes through `components.Lines` (ANSI stripped, `\r` keeps the last frame, runes split across chunks are buffered, 1000-line ring) and is coalesced into 100 ms batches.
    - `esc` hides the pane while the run continues; a toast reports how it ended. `r` on a running job reopens its pane. The pane takes keys before the editor, so `^t` stacks on it.
    - Stats reload every 5 s and when a run ends. The History feed reloads every 5 s while it's open. A `historyMsg` carries its filter and is dropped if stale.
    - The list has a last-result glyph column and running rows (`◐ running 0:41`). The detail's RECENT section shows the run strip (Mono uses `■×!·?◐`), a sparkline of the last 10 durations, the average, and the last run. NEXT shrinks to fit.
    - Keys: `h` opens the job's history. History: `f` failures, `c` cycles the trigger, `/` filters (job, id, first output line), `↵` opens the log, `o` opens `$PAGER` (default `less -R`). Log viewer: `/` search, `n/N`, `g/G`.
  - **doctor:** `State` holds the paths, config errors and warnings, and home. "State seen by cron" resolves the paths with the crontab's env lines and `$HOME` only (`config.ResolveEnv`), and warns when they differ from the shell's.
  - **Not done:** `exec` doesn't fire notifications (Phase 6). The TUI shows the trigger as plain colored text rather than a badge. The History job filter only comes from `h`; there's no picker yet (the Phase 5 palette could add one).
- _2026-10-11_: **Phase 3 done** (branch `feat/phase-3`). New: `cronexpr` presets (`Preset`, `DetectPreset`, `FieldAt`, `FieldHelp`); `jobs.Draft`, `Service.Create`/`Update`, `Snapshot.Env` + `CronEnv(line)`, `jobs.LintDraft`/`LintPath`/`PathCache`; `tui/views/editor`; CLI `gocron add`. `crontab.ShellWords` and `crontab.CommandName` are exported.
  - **No wrapping yet** (`jobs.WrapNew = false`, next to `adoptWraps`). New jobs are plain lines. The editor shows the Options row dimmed, with a note. An already-wrapped job keeps its wrapping, and its Timeout and Skip options stay editable (unchecking Track runs unwraps it). `gocron add --timeout/--no-overlap` exits 2 until then. **Phase 4: flip `WrapNew` and `adoptWraps`.** The editor, `add`, and the help text follow automatically. Then add a config `[defaults]` lookup.
  - `^t` in the editor shows a "coming in a later release" toast. Phase 4 should handle `editor.TestMsg` (it carries the draft).
  - **Presets:**
    - Detection is semantic (it compares Field bits), so `mon-fri`, `1,2,3,4,5`, `@daily`, and `7` = Sunday all match.
    - Weekly/Monthly require the other day field to be `*` because of the OR rule.
    - When the schedule is opened in a preset, its text is kept as written (`@daily` stays `@daily`) until the user touches a preset input. Only then is the canonical form written.
    - In Custom, a matching preset "lights up" (underlined + "matches X" hint), but the editor stays in Custom so typing isn't interrupted.
  - `Service.Update` re-reads the job from the crontab at save time and overlays only the draft's fields. So `Enabled`, `desc`, the binary path, and unknown meta keys survive, including on Reapply after a conflict. A draft with no changes writes nothing ("Nothing changed").
  - **Editor save conflicts:** the choices are **Keep editing** (default: reload, the draft stays) and **Reapply**. External-change reloads are paused while the editor has unsaved edits, so a later save gets a conflict instead of a silent reload (§6.5).
  - **Lints:**
    - `jobs.Issue` = `cronexpr.Issue` + `Field` (schedule/command/tags), so the editor can focus the broken field.
    - Deviation from §6.3: a bare `~`/`$HOME` is **info**, because cron sets HOME and sh expands `~`. `~` inside quotes is a **warn**, because it isn't expanded there.
    - A `\` right before `%` warns: the Phase 1 edge.
    - The empty-command error only shows after a save attempt.
    - Only the first program word is PATH-checked, and builtins are skipped.
  - **PATH lint:** debounced 250 ms (`seq`-guarded tick, then a background Cmd). `PathCache` lives as long as one editor, so reopening sees newly installed programs. `tui.Options.PathLint` lets tests stub it, so TUI goldens never depend on this machine's PATH. `Options.UserPATH` is the user's PATH for the "it's in /usr/local/bin" hint.
  - **Layout:** the editor is a centered modal (≤ 96 wide) when the terminal is ≥ 92 columns and tall enough. Otherwise it fills the body and scrolls to keep the focused row visible. The editor captures every key except `ctrl+c`.
  - A new job with no name gets the program's name (`crontab.CommandName`), in both the editor and `add`. `desc` has no editor field yet; it's preserved on edit.
- _2026-10-11_: **Phase 2 done** (branch `feat/phase-2`). Packages: `jobs` (Service façade), `tui` (root app, `theme`, `keys`, `components`, `views/jobs`); `cronexpr.Expr.Short`, `crontab.Adopt`/`DefaultAdoptName`, `crontab.Job.Describe`/`Raw`, `doctor.DetectDaemon`.
  - Stack: **Go 1.25**, `charm.land/bubbletea/v2 v2.0.9`, `lipgloss/v2 v2.0.6`, `bubbles/v2 v2.2.1`, `x/exp/teatest/v2`. v2.1.0 of bubbletea needs Go 1.26, so it was skipped. lipgloss v2 has no `AdaptiveColor`: the theme uses `LightDark(isDark)` from `tea.BackgroundColorMsg` and starts dark.
  - **Adopt doesn't wrap yet** (`jobs.adoptWraps = false`): `gocron exec` arrives in Phase 4, and a wrapped line would fail until then. Phase 4 should flip this to follow `defaults.wrap`. `crontab.Adopt` already supports `wrap=true`.
  - Undo: `jobs.Service` keeps a session undo stack (max 50) of the crontab before each change. Undo saves it through the safe path, so undo itself makes a backup. It refuses when the crontab changed since the change (`ErrUndoStale`), and an external reload clears the stack.
  - Conflicts: a save that hits `ErrConflict` returns `*jobs.ConflictError` with a `Retry` that reloads and replays the change. The TUI offers **Reapply / Discard** (Discard is the default). "View diff" waits for the Crontab view (Phase 5).
  - Views return requests (`ToggleMsg`, `DeleteMsg`, …) from `Update` synchronously instead of as Cmds, so a key typed right after (e.g. in the confirm dialog) sees the result.
  - TUI tests wrap the model in a `spy` that publishes each rendered screen, then wait on screen content. The renderer's output stream is cursor-addressed diffs, which aren't reliable to match. Goldens are unstyled screens in `internal/tui/testdata`; `go test ./internal/tui -update` rewrites them.
  - Not done in Phase 2 (by plan): `s` sort cycling and `#` tag filter (Phase 5), the last-result column and run strip (Phase 4), mouse (Phase 6). `/` filter is in.
- _2026-10-11_: **Phase 1 done** (branch `feat/phase-1`). Packages: `cronexpr`, `crontab`, `config` (paths), `doctor`, `humanize` (relative times, for CLI + TUI), `version`; CLI `list`, `describe`, `doctor`, `version`.
  - Decision: `cronexpr` has its **own Vixie/cronie-compatible parser and `Next()`**; `robfig/cron/v3` is a test-only oracle. Reasons: robfig rejects dow `7`, treats `*/N` in dom/dow differently for the OR rule, accepts `?`/`@every`, and doesn't follow cron's DST behavior.
  - DST rules implemented (match cron daemons): a fixed-time job (minute and hour both non-`*`) in a spring-forward gap fires once right after the jump; in a fall-back overlap it fires only in the first pass. Wildcard jobs skip gap minutes and fire in both overlap passes.
  - `N/step` follows cronie (`N`-max; dow max is 7, so `1/2` in dow = Mon, Wed, Fri, Sun).
  - `crontab.Store.Save` appends a final newline if missing (crontab(1) rejects an unterminated last line). Saving an unchanged crontab is a no-op (no backup).
  - Known edge: cron cannot express a literal backslash immediately before `%`; gocron's `\%` escaping round-trips in gocron but cron would read it differently. Worth a Phase 3 lint.
  - `crontab.Job` already carries the §5 fields + `Bin`, `TZ`, `Expr`, and `NextRuns()` (honors a preceding `CRON_TZ=` line). Mutations exist: `SetEnabled`, `RemoveJob`, `AddJob`, `UpdateJob` (minimal-diff tested). `Adopt`/`Undo` are still Phase 2.
  - Short schedule phrasing for list rows ("Sundays at 04:00") isn't built yet; `Describe` gives the full form.
- _2026-10-11_: Spec written. No code yet.

---

## 1. Goals & principles

### Who it's for
A single developer or sysadmin managing their own cron jobs on a laptop, workstation, or server. That may be over SSH. There are no accounts, no remote hosts, and no daemon of our own.

### The questions gocron must answer instantly
1. **What jobs do I have, and what do they do?** Schedules are shown in plain English, not `*/15 9-17 * * 1-5`.
2. **When does each one run next?** A ticking countdown and the next N runs.
3. **Did it run? Did it work?** Run history, exit codes, durations, and logs.
4. **Why did it fail?** The log viewer, plus a cron-environment test run.
5. **Am I about to break something?** Linting before saving, backups and undo after.

### Principles
- **The crontab is the source of truth.** Job definitions and metadata live *in the crontab itself*, so there is no separate job database that can drift from it. SQLite stores only run history.
- **Never lose a user's crontab.** gocron backs it up before every write, refuses to blindly overwrite external edits, and lets you undo any change. Lines gocron doesn't own are preserved byte-for-byte.
- **Coexist politely.** People still use `crontab -e`. gocron's format is readable, hand-editable, and tolerant of manual changes.
- **Keyboard-first, mouse-friendly.** Every action has a key, everything is reachable from the command palette, and the mouse works where it's natural.
- **Delight is a feature.** The UI should feel alive and calm: live countdowns, smooth state changes, thoughtful empty states, and no flicker.

### Non-goals (for now)
Remote hosts over SSH, multi-user or auth, systemd timers as a backend, a web UI, and editing system crontabs (`/etc/crontab`, `/etc/cron.d`). Several of these are listed in the [Backlog](#13-backlog--later-ideas).

---

## 2. Tech stack

| Concern | Choice | Notes |
|---|---|---|
| Language | Go 1.25+ | Single static binary. |
| TUI framework | `charm.land/bubbletea/v2` v2.0.9 | Pinned in Phase 2 (needs Go 1.25). |
| Styling | `charm.land/lipgloss/v2` v2.0.6 | `LightDark` colors, borders, layout, layer compositing for overlays. |
| Components | `charm.land/bubbles/v2` v2.2.1 | key, help (footer); more as needed (viewport, spinner, textinput). |
| Forms | Hand-rolled on top of bubbles (`huh` only if it doesn't fight the design) | The schedule builder needs custom rendering. |
| Cron parsing | Our own `cronexpr` (Vixie/cronie semantics); `robfig/cron/v3` only as a test oracle | 5-field, names, dow 7, descriptors (`@daily`…), `CRON_TZ=` prefix, cron-daemon DST rules. |
| Human descriptions | Our own `cronexpr.Describe` | Phrasing we control, table-tested. |
| Run storage | `modernc.org/sqlite` v1.59.0 | Pure Go (no cgo), WAL mode, `busy_timeout=5000`. Pinned: v1.60+ needs Go 1.26. |
| Config | TOML (`BurntSushi/toml` v1.6.0) | |
| CLI | stdlib `flag` with small subcommand dispatch | No cobra; keep the binary lean. |
| TUI tests | `charmbracelet/x/exp/teatest/v2` | Golden snapshots. |
| Release | goreleaser, vhs for demo GIFs | Phase 6. |

**Name note:** `go-co-op/gocron` is an unrelated Go scheduling library. Our binary name doesn't collide in practice. The module path is something like `github.com/<user>/gocron`.

### Filesystem layout (XDG)
| Path | Purpose |
|---|---|
| `$XDG_CONFIG_HOME/gocron/config.toml` (default `~/.config/gocron/`) | User config |
| `$XDG_STATE_HOME/gocron/runs.db` (default `~/.local/state/gocron/`) | Run history (SQLite) |
| `$XDG_STATE_HOME/gocron/logs/<job-id>/<run-id>.log` | Captured job output |
| `$XDG_STATE_HOME/gocron/backups/crontab-<RFC3339-ish timestamp>` | Crontab backups / revisions |
| `$XDG_STATE_HOME/gocron/locks/<job-id>.lock` | Overlap-prevention locks |

### Environment variables
| Var | Effect |
|---|---|
| `GOCRON_CRONTAB_FILE=<path>` | Use a plain file instead of the real crontab (`crontab -l` / `crontab -`). **All tests and day-to-day development use this.** |
| `GOCRON_STATE_DIR=<path>` | Override the state dir (tests). |
| `GOCRON_CONFIG=<path>` | Override the config file. |
| `NO_COLOR` | Honored: forces the monochrome theme. |

---

## 3. Crontab format (the core contract)

This is the most important section. Everything else builds on it.

### 3.1 Line kinds
The parser classifies every line of the crontab into exactly one kind and keeps the **original raw text** for all of them:

| Kind | Example | Owned by gocron? |
|---|---|---|
| `Blank` | `` | no |
| `Comment` | `# my notes` | no |
| `Env` | `MAILTO=me@x.com`, `PATH=/usr/local/bin:/usr/bin:/bin`, `CRON_TZ=Europe/Berlin` | no (read for display and test-runs) |
| `Entry` (unmanaged) | `*/5 * * * * ~/bin/sync.sh` | no. Shown as **unmanaged**, can be adopted |
| `JobMeta` | `# gocron:job id=k3f9a2 name="Nightly backup"` | yes |
| `JobLine` | the cron line directly after a `JobMeta` (enabled or `#gocron:off`) | yes |

A managed job is a **`JobMeta` + `JobLine` pair**. If a `JobMeta` isn't followed by a valid cron line (someone hand-edited it), treat the meta as an orphan comment: preserve it, and flag it in `doctor` and in the UI as a warning.

### 3.2 Managed job example
```crontab
# gocron:job id=k3f9a2 name="Nightly backup" tags=backup,db desc="Dump app DB to /bk"
30 2 * * * /home/jack/.local/bin/gocron exec --timeout=30m --no-overlap k3f9a2 -- 'pg_dump app | gzip > /bk/app-$(date +\%F).sql.gz'
```

Disabled (paused) job: the cron line is prefixed with `#gocron:off ` and the meta line is unchanged:
```crontab
# gocron:job id=k3f9a2 name="Nightly backup" tags=backup,db
#gocron:off 30 2 * * * /home/jack/.local/bin/gocron exec k3f9a2 -- '...'
```

Raw (unwrapped) managed job, with no run tracking:
```crontab
# gocron:job id=p7m2qd name="Say hi"
0 9 * * 1-5 notify-send "hi"
```

### 3.3 Meta line grammar
```
meta     := "# gocron:job" (SP pair)*
pair     := key "=" value
key      := [a-z][a-z0-9_]*
value    := bare | quoted
bare     := [^\s"]+
quoted   := Go-style double-quoted string (strconv.Quote / strconv.Unquote)
```
- Known keys: `id` (required), `name`, `tags` (comma-separated), `desc`.
- **Unknown keys are preserved** on rewrite, for forward compatibility.
- `id`: 6 chars, lowercase base32 (`a-z2-7`), random, unique within the crontab.
- When serializing, a value is quoted only if it contains whitespace, `"`, `=`, `#`, or is empty.

### 3.4 Wrapped command grammar
```
<schedule> <abs-path-to-gocron> exec [--timeout=<dur>] [--no-overlap] <id> -- <shell-quoted-command>
```
- **Runtime options live on the exec line** (`--timeout`, `--no-overlap`) because `exec` must work without reading the crontab. Descriptive metadata lives in the meta line.
- The user's command is stored as **one single-quoted shell word** (each `'` becomes `'\''`). `exec` runs it with `$SHELL -c` (default `/bin/sh`). This is essential: without the quoting, pipes and redirects in the user's command would apply to `gocron exec` itself.
- **`%` escaping:** cron turns an unescaped `%` into a newline. When serializing any job line, gocron escapes `%` → `\%`, and when parsing it unescapes `\%` → `%`. The user always sees and edits the plain command.
- A line is recognized as wrapped if the first command word's basename is `gocron` and the next word is `exec`. The binary path may differ from the current one. `doctor` detects stale paths and offers to rewrite them.

### 3.5 Schedules
- Supported: 5-field expressions with lists, ranges, steps, and names (`JAN`, `MON`); descriptors `@yearly @annually @monthly @weekly @daily @midnight @hourly @reboot`.
- `@reboot` has no next-run time. It is displayed as "At boot".
- Vixie semantics: if both day-of-month and day-of-week are restricted, the job runs when **either** matches. Tests must cover this. Describe it as "on the 1st and on Mondays".
- `CRON_TZ=` (a crontab-level env line, or a per-expression prefix) is shown in the UI. Note that Debian's cron ignores it but cronie respects it; `doctor` reports which daemon is in use.
- Times are displayed in local time by default.

### 3.6 Reading and writing safely
`Source` interface with two implementations:
```go
type Source interface {
    Read(ctx) (content []byte, err error)   // empty + nil if user has no crontab
    Write(ctx, content []byte) error
    Name() string                           // "user crontab" | "file: /path"
}
```
- `SystemSource`: `crontab -l` (treat "no crontab for <user>" as empty), `crontab -` (stdin).
- `FileSource`: plain file read/write (atomic: temp file + rename).

**Safe-write protocol** (`crontab.Store.Save`):
1. Re-read the current content and compute its SHA-256. If it differs from the hash captured at load time, **abort with `ErrConflict`**. The UI then offers: *Reload* (discard my change), *Reapply* (reload, replay my operation on the fresh state), or *View diff*.
2. Write a backup of the current content to `backups/`.
3. Serialize the new state and write it via `Source.Write`.
4. Read it back and verify it's byte-identical to what was written. On mismatch, surface an error with the backup path.
5. Update the in-memory hash.
6. Prune backups beyond `backups.keep` (default 50).

**Round-trip invariant:** `Serialize(Parse(x)) == x` for any input, including CRLF, tabs, a missing trailing newline, and trailing whitespace. Golden tests enforce it.

Mutations operate on the parsed model and touch only the affected lines, so unrelated lines never change.

### 3.7 Adoption
"Adopt" turns an unmanaged `Entry` into a managed job. gocron inserts a `JobMeta` above it, with a name defaulting to a short form of the command, and optionally (the default is yes) rewrites the command into wrapped form. Comments directly above the entry are kept. If the comment immediately above is a single line, it's offered as the default `name`.

---

## 4. Execution wrapper & run history

### 4.1 `gocron exec`
```
gocron exec [--timeout=DUR] [--no-overlap] [--trigger=cron|manual|test] [--tee] <id> -- <command>
```
Behavior:
1. Open `runs.db` (create and migrate if needed). Insert a run row with `status=running` and the pid.
2. If `--no-overlap` is set: try a non-blocking `flock` on `locks/<id>.lock`. If it's held, record `status=skipped` and exit 0.
3. Run `$SHELL -c <command>` (default `/bin/sh`) in its own process group. Capture combined stdout and stderr to `logs/<id>/<run-id>.log`, capped at `runs.max_log_bytes` (default 1 MiB). After the cap, keep counting bytes, set `truncated=1`, and append a final note.
4. `--tee` also streams the output to gocron's own stdout (used by "Run now" in the TUI). **By default output is not echoed**, so cron doesn't send an email for every run. The config option `runs.mail_output = "never" | "on_failure" | "always"` controls echoing to stdout so `MAILTO` users can keep cron's email.
5. On timeout: SIGTERM the process group, wait 10s, then SIGKILL. `status=timeout`.
6. Record `ended_at`, `exit_code`, and `status` (`success` | `failed` | `timeout` | `skipped` | `error`).
7. Prune runs and logs beyond `runs.retention` per job (default 100).
8. Fire notifications (Phase 6).
9. **Exit with the child's exit code.** If gocron itself fails (e.g. the DB is locked or corrupt), it must **still run the command**. The wrapper failing must never stop the job from running. Log the error to `state/gocron-errors.log`.

Stale `running` rows (whose pid is dead) are shown as `status=unknown` ("lost").

### 4.2 Schema
```sql
CREATE TABLE schema_version (version INTEGER NOT NULL);
CREATE TABLE runs (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  job_id      TEXT    NOT NULL,
  trigger     TEXT    NOT NULL,            -- cron | manual | test
  status      TEXT    NOT NULL,            -- running | success | failed | timeout | skipped | error
  started_at  INTEGER NOT NULL,            -- unix ms
  ended_at    INTEGER,
  exit_code   INTEGER,
  pid         INTEGER,
  log_path    TEXT,
  log_bytes   INTEGER DEFAULT 0,
  truncated   INTEGER DEFAULT 0
);
CREATE INDEX runs_job_started ON runs(job_id, started_at DESC);
CREATE INDEX runs_started     ON runs(started_at DESC);
```

### 4.3 Cron-environment test run (signature feature)
"Works in my shell, fails in cron" is the #1 cron pain. The **Test** action (`t`) runs the job's command the way cron would:
- `env -i` with only `HOME`, `LOGNAME`, `USER`, `SHELL=/bin/sh`, `PATH=/usr/bin:/bin`
- plus any `Env` lines from the crontab (e.g. a custom `PATH`), applied in order
- cwd = `$HOME`, no TTY, stdin = `/dev/null`
- recorded as `trigger=test`, with output streamed live into a pane

When a test run fails, the result pane suggests likely causes, such as a command not found on cron's `PATH` (it lists the binary's real location from the user's `PATH`) or a missing env var. The diagnosis is static, so the job isn't run twice behind the user's back. The pane offers to rerun it in the user's own shell to compare.

---

## 5. Domain model

```go
type Job struct {
    ID        string
    Name      string
    Desc      string
    Tags      []string
    Schedule  string        // as written, e.g. "30 2 * * *" or "@daily"
    Command   string        // user's plain command (unquoted, unescaped)
    Enabled   bool
    Managed   bool          // false = unmanaged crontab entry
    Wrapped   bool          // runs via gocron exec (has history)
    Timeout   time.Duration // 0 = none
    NoOverlap bool
    Line      int           // position in crontab (for stable ordering)
}

type Run struct {
    ID        int64
    JobID     string
    Trigger   string
    Status    string
    StartedAt time.Time
    EndedAt   *time.Time
    ExitCode  *int
    LogPath   string
    LogBytes  int64
    Truncated bool
}

type JobStats struct {     // computed from recent runs
    Last       *Run
    Recent     []Run       // last 20, for the run strip
    SuccessPct float64
    AvgDur     time.Duration
}
```

`internal/jobs.Service` is the façade the TUI and CLI use: `List`, `Get`, `Create`, `Update`, `SetEnabled`, `Delete`, `Adopt`, `Undo`, `RunNow`, `TestRun`, `Stats`. It combines `crontab.Store` and `store.Runs`.

---

## 6. UX & visual design

### 6.1 Design direction
**Calm, luminous, precise.** Dark-first, with a single vivid accent on a quiet neutral base, generous whitespace, and rounded panels. Color carries meaning (status) and is not used for decoration. The terminal's own background is respected; we never paint full-screen backgrounds.

**Default theme "Nightshift"** (lipgloss `LightDark`, dark / light):

| Token | Dark | Light | Use |
|---|---|---|---|
| `accent` | `#5EEAD4` (mint) | `#0D9488` | focus, selection bar, active tab, key hints |
| `accent2` | `#C4B5FD` (lavender) | `#7C3AED` | headings, schedule text, highlights |
| `text` | `#E4E4E7` | `#18181B` | primary text |
| `muted` | `#71717A` | `#71717A` | secondary text, hints |
| `border` | `#3F3F46` | `#D4D4D8` | unfocused panel borders |
| `ok` | `#4ADE80` | `#16A34A` | success |
| `fail` | `#F87171` | `#DC2626` | failure |
| `warn` | `#FBBF24` | `#D97706` | warnings, running, timeout |
| `selBg` | `#27272A` | `#F4F4F5` | selected-row background |

Additional themes in Phase 6: **Daybreak** (light-optimized), **Mono** (no color, used automatically with `NO_COLOR`). All styles come from a `theme.Theme` struct. Views never hardcode colors.

**Glyph vocabulary** (consistent everywhere):
`●` enabled · `○` paused · `◐` running (animated spinner when space allows) · `✓` success · `✗` failed · `⏱` timeout · `↷` skipped · `?` lost/unknown · `⚠` warning · `▌` selection bar.

**Run strip:** the last 20 runs as colored `■` cells (oldest → newest), in the `ok`/`fail`/`warn`/`muted` colors. It's more legible than a duration sparkline at a glance. Duration sparklines (`▁▂▃▅▇`) appear in the detail pane.

**Motion:** countdowns tick every second (a single global `tick` msg, not one per job). Spinners run only for running jobs. Toasts slide in at the bottom right and fade (dim) after 3s. Nothing should flicker: render only on state change or tick.

### 6.2 Layout
- **Header (1 line):** `◷ gocron`, tabs `Jobs  Timeline  History  Crontab` (active tab in accent with an underline), and on the right the cron daemon status (`● cron active` / `✗ cron not running`) and a live clock.
- **Body:** the active view.
- **Footer (1 line):** contextual key hints from `bubbles/help`. `?` expands it to a full help overlay.
- **Responsive:**
  - ≥ 120 cols: split view (list 45% | detail 55%).
  - 80–119 cols: list only, and `enter` opens the detail as a full panel (`esc` goes back).
  - < 80 cols: compact rows (name, next run, and status only).
  - < 60×15: a friendly "make me a bit bigger" screen.

### 6.3 Views

#### 1 · Jobs (home)
```
 ◷ gocron   Jobs  Timeline  History  Crontab                         ● cron active · Sat 14:32:07

 ╭─ Jobs · 6 ─────────────────────────────────────────╮ ╭─ Nightly backup ───────────────────────────────────╮
 │ / filter…                                          │ │ ● enabled · #backup #db · k3f9a2                   │
 │                                                    │ │                                                    │
 │▌● Nightly backup   At 02:30          in 11h 58m  ✓ │ │ SCHEDULE   30 2 * * *                              │
 │ ● Sync photos      Every 15 minutes  in 7m 53s   ✓ │ │            At 02:30, every day                     │
 │ ● Prune docker     Sundays at 04:00  in 1d 13h   ✗ │ │                                                    │
 │ ◐ Rotate logs      Every hour        running 0:41  │ │ COMMAND    pg_dump app | gzip > /bk/app.sql.gz     │
 │ ○ Weekly report    Mondays at 09:00  paused        │ │                                                    │
 │                                                    │ │ NEXT       Sun 12 Oct 02:30   in 11h 58m           │
 │ UNMANAGED · 1                                      │ │            Mon 13 Oct 02:30   in 1d 11h            │
 │ · @reboot ~/bin/start-tmux.sh        a adopt       │ │            Tue 14 Oct 02:30   in 2d 11h            │
 │                                                    │ │                                                    │
 │                                                    │ │ RECENT     ■■■■■■■■■■■■■■■■■■■■  20/20 ok          │
 │                                                    │ │            ▃▃▄▃▃▅▃▃▃▃  avg 1m 12s · 11h ago ✓      │
 │                                                    │ │                                                    │
 │                                                    │ │ ⚠ `pg_dump` is in /usr/local/bin, which isn't on   │
 │                                                    │ │   cron's PATH. Press t to test in cron's env.      │
 ╰────────────────────────────────────────────────────╯ ╰────────────────────────────────────────────────────╯
 ↑↓ move  ↵ open  n new  e edit  space pause  r run  t test  x delete  / filter  ^k commands  ? help
```
- Row: status dot, name (or a truncated command if there's no name), human schedule, next run (relative; absolute when > 7 days), and the last result glyph.
- Sort: by next run (default) or by name or crontab order (`s` cycles through them).
- Unmanaged entries appear in their own section, dimmed, with `a` to adopt.
- Empty state (no crontab): a short welcome explaining that gocron manages your crontab, what a managed block looks like, and `n` to create your first job.

#### 2 · Timeline
```
 ╭─ Timeline · next 24h ───────────────────────────────────────────── d day · w week · ←→ scroll ─╮
 │                 15:00     18:00     21:00     00:00     03:00     06:00     09:00     12:00     │
 │                 ▼ now                                                                          │
 │ Nightly backup                                                  ▮                              │
 │ Sync photos     ╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷╷ │
 │ Rotate logs     ╷    ╷    ╷    ╷    ╷    ╷    ╷    ╷    ╷    ╷    ╷    ╷    ╷    ╷    ╷    ╷  │
 │ Prune docker                                                                 (next: Sun)       │
 │ ────────────────────────────────────────────────────────────────────────────────────────────── │
 │ density         ▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁█▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁▁ │
 │ ⚠ 4 jobs fire at 00:00. Consider staggering them.                                             │
 ╰────────────────────────────────────────────────────────────────────────────────────────────────╯
```
- One lane per enabled job. Day mode covers the next 24h. Week mode covers 7 days with day columns.
- High-frequency jobs (more fires than columns) render as a continuous band with a count label.
- A density row at the bottom, plus congestion warnings (≥ N jobs in the same minute; configurable).
- Past runs from history can be overlaid on the left of "now" (✓/✗ markers). Nice-to-have.
- `enter` on a lane opens that job.

#### 3 · History
A reverse-chronological feed of all runs across jobs: time, job, trigger badge (`cron`/`manual`/`test`), status glyph, duration, exit code, and the first line of output. Filters: job, status (failures only: `f`), trigger. `enter` opens the **log viewer**: a scrollable viewport with `/` search, `n`/`N` for next and previous match, `g`/`G` for top and bottom, a truncation notice, and `o` to open in `$PAGER`.

#### 4 · Crontab
- Left: the raw crontab with light syntax highlighting (schedule fields, comments, env lines, gocron meta dimmed).
- Right: a **revisions** list from the backups (timestamp + a summary like "+1 job, ~1 edited"). Selecting one shows a unified diff against the current crontab. `R` restores it (through the safe-write path, so the restore itself is undoable).
- `E` opens the crontab in `$EDITOR` via a temp file. gocron validates the result before saving and shows parse errors inline.

#### Editor (modal, `n` / `e`)
```
 ╭─ New job ───────────────────────────────────────────────────────────────────────────╮
 │                                                                                     │
 │  Name      Nightly backup                                                           │
 │                                                                                     │
 │  Schedule  ‹ Every N min · Hourly · Daily · Weekdays · Weekly · Monthly · Custom ›  │
 │            Daily at  [02]:[30]                                                      │
 │            30 2 * * *          ✓ At 02:30, every day                                │
 │            next  Sun 02:30 · Mon 02:30 · Tue 02:30 · Wed 02:30 · Thu 02:30          │
 │                                                                                     │
 │  Command   pg_dump app | gzip > /bk/app-$(date +%F).sql.gz                          │
 │  Tags      backup, db                                                               │
 │                                                                                     │
 │  Options   [x] Track runs   Timeout [30m  ]   [x] Skip if still running            │
 │                                                                                     │
 │  ⚠ `%` will be escaped as `\%` in the crontab. You don't need to do anything.       │
 │                                                                                     │
 │                                         esc cancel · ^t test · ^s save              │
 ╰─────────────────────────────────────────────────────────────────────────────────────╯
```
- **Schedule builder:** a preset carousel (`←`/`→`). Each preset has friendly sub-inputs (minutes, time, weekdays as toggleable `M T W T F S S` chips, day of month). Switching to **Custom** shows the raw expression with a **field ruler** underneath (`min hour dom mon dow`), highlighting the field under the cursor and explaining its allowed values. Editing raw text that matches a preset lights up that preset.
- **Live preview:** validity (✓ / ✗ with the error), the English description, and the next 5 runs. It updates on every keystroke.
- **Lints** (non-blocking warnings; errors block saving):
  - an unparsable schedule (error)
  - an empty command (error)
  - `%` in the command (info: auto-escaped)
  - the first command word isn't found on cron's effective `PATH` (warn, with the real location)
  - a relative path, `~`, or reliance on `$HOME`/cwd (warn)
  - interactive or TTY-needing commands like `sudo` or `vim` (warn)
  - very frequent schedules (`* * * * *`) (info)
  - a day-of-month that never or rarely occurs (`30 2 31 2 *` never; 31st skips months) (warn)
  - both dom and dow restricted (info: explains the OR semantics)
- `^t` runs a cron-env test of the draft without saving it.
- `esc` with unsaved changes asks for confirmation.

#### Overlays
- **Command palette** (`ctrl+k` or `:`): a fuzzy list of every action, plus "Go to job…". It shows each action's key binding so people learn the shortcuts.
- **Help** (`?`): grouped key bindings for the current view.
- **Confirm dialogs:** delete, restore, and discarding changes. The default button is the safe one.
- **Toasts:** e.g. `✓ Saved · backup created · u undo`, `✗ Crontab changed outside gocron · r reload`.

### 6.4 Key map (global unless noted)
| Key | Action |
|---|---|
| `1`–`4`, `tab`/`shift+tab` | switch view |
| `↑↓` / `j k`, `g G`, `pgup pgdn` | move |
| `enter` | open / select |
| `esc` | back / close overlay / clear filter |
| `n` | new job |
| `e` | edit job |
| `space` | pause / resume |
| `r` | run now (streams output) |
| `t` | test in cron environment |
| `x` | delete (confirm) |
| `a` | adopt unmanaged entry |
| `y` | copy the command / expression to the clipboard (OSC 52) |
| `/` | filter (fuzzy over name, command, tags) |
| `#` | filter by tag |
| `s` | cycle sort |
| `u` | undo last change |
| `ctrl+k`, `:` | command palette |
| `?` | help |
| `q`, `ctrl+c` | quit (`ctrl+c` always quits immediately) |

Mouse (Phase 6): click rows and tabs, and scroll wheel in lists and the viewport.

### 6.5 Feedback & error principles
- Every write produces a toast. Every failure explains what happened **and what to do next**.
- Never block the UI on I/O. Crontab reads and writes, DB queries, and runs all happen in `tea.Cmd`s, with a spinner where the wait is noticeable.
- External changes: on focus or every 5s, gocron re-hashes the crontab (cheaply). If it changed and there are no unsaved edits, it reloads silently and shows `↻ Crontab reloaded (changed outside gocron)`.

---

## 7. CLI

`gocron` with no arguments launches the TUI. Subcommands are for scripting and cron itself:

| Command | Description |
|---|---|
| `gocron list [--json] [--all]` | Table of jobs (`--all` includes unmanaged entries) |
| `gocron add [--name N] --schedule S [--tag T]… [--timeout D] [--no-overlap] [--raw] -- CMD` | Create a job (name defaults to the program's name; quote a pipeline as one argument) |
| `gocron enable <id>` / `disable <id>` | Resume / pause |
| `gocron rm <id>` | Delete (backup kept) |
| `gocron run <id> [--cron-env]` | Run now in the foreground, streaming output, recorded as `manual`/`test` |
| `gocron logs <id> [-n N] [--run R]` | Print recent run summaries or a run's log |
| `gocron exec …` | Internal wrapper used in crontab lines (see §4.1) |
| `gocron describe "<expr>"` | Print the English description and the next 5 runs (handy on its own) |
| `gocron doctor [--fix]` | Health checks (below) |
| `gocron export [--file F]` / `import F [--dry-run]` | YAML export/import of managed jobs |
| `gocron version` | Version, commit, build date |

Exit codes: 0 ok, 1 error, 2 usage error, 3 conflict (crontab changed externally).

**`doctor` checks:** the `crontab` binary exists; the user is allowed to use cron (cron.allow/deny via `crontab -l` behavior); the cron daemon is active (`systemctl is-active cron|cronie|crond`, otherwise check for a process); which daemon it is and whether it honors `CRON_TZ`; wrapped lines point to the current gocron binary (`--fix` rewrites them); orphaned meta lines; duplicate IDs; DB opens and its schema is current; state dirs are writable; `MAILTO` status (informational).

---

## 8. Configuration

```toml
# ~/.config/gocron/config.toml — every key optional
theme = "nightshift"            # nightshift | daybreak | mono

[ui]
relative_times = true
time_format    = "15:04"        # Go layout
week_starts    = "monday"
default_sort   = "next"         # next | name | crontab
congestion_threshold = 3        # timeline warning: jobs in same minute

[runs]
retention       = 100           # runs kept per job
max_log_bytes   = 1048576
mail_output     = "never"       # never | on_failure | always (echo to stdout → cron mail)

[backups]
keep = 50

[defaults]                      # defaults for new jobs
wrap       = true
no_overlap = true
timeout    = ""                 # e.g. "1h"

[notify]                        # Phase 6
on      = "failure"             # never | failure | always | change (fail→ok, ok→fail)
desktop = true                  # notify-send / osascript
webhook = ""                    # POST JSON {job, status, exit_code, duration, log_tail}
```

---

## 9. Architecture

```
cmd/gocron/main.go         subcommand dispatch (stdlib flag); no args → TUI
internal/
  cronexpr/                Parse, Validate, Describe (English), Next(n), presets, Lint
  crontab/                 line-preserving Parse/Serialize, model (Lines, Jobs), meta grammar,
                           wrapped-command encode/decode, % escaping, Source (System|File),
                           Store (load, hash, safe Save, backups, revisions, diff)
  store/                   SQLite open/migrate, Runs repo (insert/update/query/prune/stats)
  runner/                  Exec (wrapper), RunNow/TestRun (streaming), cron-env builder, flock, timeout
  jobs/                    Service façade: Job model = crontab job + stats
  config/                  load TOML + defaults, XDG paths
  doctor/                  health checks (shared by CLI + TUI header status)
  tui/
    app.go                 root model: router, global keys, tick, toasts, overlays, size
    theme/                 Theme struct, palettes, glyphs
    components/            runstrip, sparkline, timeline, toast, palette, help, confirm, ruler
    views/jobs/            list + detail
    views/editor/          form + schedule builder
    views/timeline/
    views/history/         feed + log viewer
    views/crontab/         raw view + revisions/diff
testdata/crontabs/         golden fixtures
```

**Dependency rule:** `tui` → `jobs` → (`crontab`, `store`, `runner`, `cronexpr`). Lower packages never import `tui`. `cronexpr` and `crontab` have no I/O beyond `Source`, so they're trivially testable.

**TUI architecture:** one root `tea.Model` owns the shared state (the jobs snapshot, stats, size, theme) and routes messages to the active view. Views are sub-models with `Update(msg) (View, tea.Cmd)` and `View() string`. All I/O returns typed messages (`jobsLoadedMsg`, `saveResultMsg`, `runOutputMsg`, …). A single 1s `tickMsg` drives the countdowns.

---

## 10. Phases & acceptance criteria

Every phase ends with `go vet ./...` and `go test ./...` passing, the Progress table updated, and a commit. **No phase may touch the real crontab in tests.** Manual testing uses `GOCRON_CRONTAB_FILE` unless you intentionally try the real thing.

### Phase 1: Foundations
Scope: `git init`, `go mod init`, packages `cronexpr`, `crontab`, `config` (paths only), the CLI skeleton with `list`, `describe`, `doctor` (crontab/daemon checks only), `version`.

Acceptance:
- [x] `cronexpr.Describe` produces natural English for ≥ 40 table-tested expressions (steps, ranges, lists, names, descriptors, dom/dow OR, `@reboot`).
- [x] `cronexpr.Next(expr, from, n)` is correct across DST transitions and the dom/dow OR case (tests).
- [x] `cronexpr.Lint` implements the schedule-level lints from §6.3.
- [x] The crontab parser round-trips every fixture byte-for-byte. There are ≥ 10 fixtures, covering env lines, `CRON_TZ`, comments, `@reboot`, tabs, CRLF, no trailing newline, managed and disabled jobs, orphan meta, and wrapped commands containing quotes and `%`.
- [x] Meta grammar encode/decode, wrapped-command encode/decode, and `%` escaping are each property- or fuzz-tested (`go test -fuzz` target for round-trip).
- [x] `Store.Save` implements the full safe-write protocol. A test proves that an external modification yields `ErrConflict` and that a backup is created on each save.
- [x] `gocron list`, `gocron describe "*/15 9-17 * * 1-5"`, and `gocron doctor` work against `GOCRON_CRONTAB_FILE` and against the real crontab (read-only).

### Phase 2: TUI shell + Jobs view
Scope: theme system, root app model, header/footer/help, responsive layout, the Jobs list + detail pane, pause/resume, delete, undo, adopt, external-change reload, and toasts.

Acceptance:
- [x] `gocron` opens the TUI. The header shows the tabs (other tabs show a tasteful "coming soon"), the daemon status, and the clock.
- [x] The Jobs list shows the human schedule and a ticking next-run countdown. The detail pane shows the schedule, the command, and the next 10 runs.
- [x] `space`, `x`, `a`, and `u` work through the safe-write path, each with a toast. Undo restores the previous revision.
- [x] Layout adapts at the 120/80/60 breakpoints. The empty state and the "too small" screen exist.
- [x] `NO_COLOR` gives the mono theme.
- [x] teatest golden snapshots for the list (wide + narrow), the empty state, and the help overlay.

### Phase 3: Editor
Scope: the new/edit modal, schedule builder (presets + custom with a field ruler), live preview, the full lint list, `^s` save, and unsaved-changes confirmation. Also `gocron add` in the CLI.

Acceptance:
- [x] Every preset produces the correct expression, and typing a matching raw expression lights up its preset.
- [x] The preview updates per keystroke without lag (no I/O in the render path; PATH lookups are cached and debounced).
- [x] Commands containing `'`, `"`, `|`, `>`, `$( )`, and `%` survive a save → reload → edit cycle unchanged (test).
- [x] Saving an edit touches only that job's two lines (diff test).

### Phase 4: Runs
Scope: `store` package, `gocron exec`, run now (`r`) and cron-env test (`t`) with streamed output panes, the History view, the log viewer, run strips and sparklines, last-status glyphs, `gocron run` and `gocron logs`.

Acceptance:
- [x] `exec` records success/failed/timeout/skipped correctly, honors the timeout (process-group kill) and `--no-overlap`, caps logs, prunes by retention, and passes through the exit code. Integration tests use real short-lived shell commands.
- [x] If the DB is unavailable, `exec` still runs the command and exits with its code (test).
- [x] A cron-env test run of `command-only-in-local-bin` fails, and the UI suggests the PATH fix.
- [x] Concurrent `exec`s (10 at once) don't error on DB locking.
- [x] History filters and log search work. teatest snapshots for History and the log viewer.

### Phase 5: Timeline + power tools
Scope: the Timeline view (day/week, density, congestion warnings), command palette, tag filter, sort cycling, the Crontab view (highlighting, revisions, diff, restore, `$EDITOR` round-trip with validation), `export`/`import`, clipboard copy.

Acceptance:
- [x] The timeline renders correctly for high-frequency, sparse, and `@reboot` jobs and when there are no jobs. Congestion warnings fire at the threshold.
- [x] The palette fuzzy-finds every action and job, and executes them.
- [x] Restoring a revision is itself undoable. An invalid `$EDITOR` result is rejected with line-level errors, and nothing is written.
- [x] `export` → `import --dry-run` → `import` round-trips the managed jobs.

### Phase 6: Polish & release
Scope: notifications (desktop + webhook from `exec`), natural-language schedule input in the editor ("every weekday at 9am", "every 15 minutes", "first monday of the month" → with a fallback explanation when unsupported), the Daybreak and Mono themes, mouse support, `doctor --fix`, performance pass, README with vhs GIFs, goreleaser (linux/darwin, amd64/arm64), and an install script / `go install`.

Acceptance:
- [ ] A failing job triggers a desktop notification and a webhook POST (tested with `httptest`). Notification errors never affect the job's exit code.
- [ ] The NL parser handles ≥ 30 phrasings (table tests) and shows the resulting expression before applying it.
- [ ] Startup to first render is < 150 ms with 100 jobs and 10k runs.
- [ ] The README has a hero GIF, an install section, and an explanation of the crontab format.

---

## 11. Testing strategy
- **Unit:** `cronexpr` (table tests: describe, next, lint), `crontab` (golden round-trip, mutations produce minimal diffs, meta/wrap grammar fuzzing), `store` (temp DB), `config`.
- **Integration:** `runner` executes real `/bin/sh` commands with temp state dirs. `jobs.Service` runs against a `FileSource` in `t.TempDir()`.
- **TUI:** `teatest` with fixed terminal sizes and a **frozen clock** (inject a `Clock` interface; never call `time.Now()` directly in views) for deterministic golden output. Update the goldens with `-update`.
- **Safety:** a test helper that fails any test if `SystemSource` is constructed without an explicit opt-in env var.

---

## 12. Edge cases to remember
- The user has no crontab yet (`crontab -l` exits 1 with "no crontab for user").
- The user is denied by `cron.deny`. Show a clear message and stay read-only.
- A crontab that is huge or full of junk: parse errors on individual lines must not prevent loading. Mark those lines invalid and preserve them.
- Duplicate IDs (from copy-pasting) are flagged, and `doctor --fix` re-IDs them.
- The gocron binary has moved or been reinstalled elsewhere: wrapped lines go stale, which `doctor` catches. The header can show a warning badge.
- DST: next-run lists around transitions, and jobs scheduled in the skipped or repeated hour.
- Timezones: `CRON_TZ` vs the system TZ vs display.
- Very long commands or names: truncate with `…` in lists, wrap in the detail pane.
- A job running while it's being edited or deleted: allow it, and note that the current run continues.
- A terminal without true color: lipgloss degrades. Verify the 256-color and 16-color appearance.

---

## 13. Backlog / later ideas
- A read-only view of system cron (`/etc/crontab`, `/etc/cron.d/*`, `cron.{hourly,daily,…}`).
- A systemd user timers backend (same UI, a different `Source`).
- Remote hosts over SSH (`Source` over ssh).
- Job templates (backup, cleanup, healthcheck ping).
- Healthcheck integrations (healthchecks.io / Cronitor ping on success).
- Job chaining / dependencies (likely out of scope for a cron manager).
- An "Explain this job" or "Why did this fail?" assistant using an LLM over the logs.
