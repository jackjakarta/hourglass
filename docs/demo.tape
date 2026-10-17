# The README's hero GIF. Record it from the repository root with
#   vhs docs/demo.tape
# (needs vhs, ttyd and ffmpeg, and hourglass installed at /usr/local/bin/hourglass).
# It works on a copy of docs/demo.crontab and never touches your crontab.

Output docs/demo.gif

Set Shell "bash"
Set FontSize 15
Set Width 1400
Set Height 760
Set Padding 16
Set Theme "Catppuccin Mocha"
Set TypingSpeed 60ms

Hide
Type "export D=$(mktemp -d) && cp docs/demo.crontab $D/crontab"
Enter
Type "export HOURGLASS_CRONTAB_FILE=$D/crontab HOURGLASS_STATE_DIR=$D/state HOURGLASS_CONFIG=$D/config.toml"
Enter
Type `printf '[notify]\non = "never"\n' > $D/config.toml`
Enter
# Some run history, so the run strips and the History view have content.
Type "for i in $(seq 12); do hourglass exec k3f9a2 -- 'echo dumped; sleep 0.2'; hourglass exec s7nc0p -- 'echo synced'; done"
Enter
Type "hourglass exec pdk4r2 -- 'echo docker: command not found; exit 127'; hourglass exec r0tl6s -- 'echo rotated'; clear"
Enter
Sleep 4s
Show

Type "hourglass"
Enter
Sleep 2.5s

# Browse the jobs.
Down
Sleep 800ms
Down
Sleep 1.2s

# A new job, with its schedule written in plain English.
Type "n"
Sleep 800ms
Type "Say good morning"
Tab
Tab
Tab
Sleep 500ms
Type "every weekday at 9am"
Sleep 1.5s
Enter
Sleep 1s
Tab
Tab
Type `notify-send "Good morning"`
Sleep 800ms
Ctrl+S
Sleep 2s

# The timeline, the history, the crontab's revisions.
Type "2"
Sleep 2.5s
Type "3"
Sleep 2.5s
Type "4"
Sleep 2.5s

# The command palette.
Type "1"
Ctrl+K
Sleep 600ms
Type "test"
Sleep 1.5s
Escape
Sleep 500ms
Type "q"
Sleep 500ms
