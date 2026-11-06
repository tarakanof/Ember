# Ember integrations

Ways to drive the Ember Pomodoro timer from outside the macOS menu app. Both
talk to the server's existing HTTP endpoints — nothing here keeps local state.

```
integrations/
├── cli/      ember-pomo — a portable shell controller (curl-based)
└── alfred/   an Alfred workflow that shells out to ember-pomo
```

## cli/ember-pomo

A dependency-light POSIX-shell wrapper over `/v1/pomodoro/*`. Only `curl` is
required (`jq`/`python3` are used for pretty output if present).

```sh
# one-time setup
mkdir -p ~/.config/ember
cp integrations/cli/cli.env.example ~/.config/ember/cli.env
$EDITOR ~/.config/ember/cli.env            # set EMBER_URL + EMBER_TOKEN
ln -s "$PWD/integrations/cli/ember-pomo" /usr/local/bin/ember-pomo   # optional

# use it
ember-pomo start            # begin a focus block
ember-pomo toggle           # play/pause (mirrors the clock's middle button)
ember-pomo stop
ember-pomo skip
ember-pomo status           # live timer JSON
ember-pomo stats            # usage stats JSON
ember-pomo remaining        # "24:47 focus" — handy for tmux/menubar
```

Config precedence: real `EMBER_*` env vars win, else `~/.config/ember/cli.env`
(override the path with `$EMBER_CLI_ENV`). Connection timeouts are tunable via
`EMBER_CONNECT_TIMEOUT` / `EMBER_MAX_TIME`.

The token is a **write-endpoint bearer token**; read commands (`status`,
`stats`, `remaining`) work without it when the server allows open reads.

## alfred/ (Alfred workflow)

A Script Filter (`pomo` keyword) that lists actions with the live timer in the
subtitle and runs `ember-pomo` for the chosen action.

```sh
cd integrations/alfred
./build.sh                  # → ember-pomodoro.alfredworkflow
open ember-pomodoro.alfredworkflow   # imports into Alfred (needs Powerpack)
```

Then, in Alfred → Workflows → Ember Pomodoro → the `[𝓍]` variables pane, set:

| Variable | Value |
|---|---|
| `EMBER_POMO` | absolute path to `ember-pomo` (e.g. `/usr/local/bin/ember-pomo`) |
| `EMBER_URL` | your server URL |
| `EMBER_TOKEN` | your bearer token |

Now type `pomo` in Alfred. Optionally bind a hotkey to the "Pause / resume"
action for a one-key focus toggle.

**Building the workflow by hand instead** (if you'd rather not import the
prebuilt bundle): create a Script Filter with keyword `pomo` running
`/bin/sh "$alfred_workflow_directory/pomo-filter.sh" "$1"` (argv), connect it to
a Run Script action running `"${EMBER_POMO:-ember-pomo}" {query}`, then to a
Post Notification. Copy `pomo-filter.sh` into the workflow folder.

## Security

These tools send your bearer token to the server over HTTP. Keep `cli.env` and
the Alfred variables out of version control (the example file ships with an
empty token on purpose). Prefer HTTPS (`EMBER_URL=https://…`) off-LAN.
