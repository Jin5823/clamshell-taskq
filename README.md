# clamshell-taskq

**Run background tasks on a MacBook that's mostly closed-lid and sleeping.** The laptop wakes itself every 5 minutes, picks up pending work from a Slack channel, runs your command, and goes back to sleep.

("Clamshell" is Apple's term for "laptop with the lid closed" — the mode this tool is built around.)

- **`server`** (long-running, on a 24/7 host) forwards Slack mentions into a single "task" channel.
- **`runner`** (short-lived, on your laptop) is invoked every 5 minutes by `launchd` + `pmset` wake. If the latest task-channel message has no reaction yet, it spawns `$COMMAND` detached and exits.

The Slack channel itself is the queue. Reactions (`⏳`/`✅`/`❌`) are the state. No database. No HTTP between server and runner.

---

## Build

```bash
go build -o bin/server ./cmd/server
go build -o bin/runner ./cmd/runner
```

Cross-compile (Go does this with env vars only — no toolchain install needed):

```bash
# server
GOOS=linux   GOARCH=amd64 go build -o bin/server-linux-amd64       ./cmd/server
GOOS=linux   GOARCH=arm64 go build -o bin/server-linux-arm64       ./cmd/server
GOOS=darwin  GOARCH=arm64 go build -o bin/server-darwin-arm64      ./cmd/server
GOOS=windows GOARCH=amd64 go build -o bin/server-windows-amd64.exe ./cmd/server

# runner (Unix-like only — relies on POSIX session APIs)
GOOS=darwin GOARCH=arm64 go build -o bin/runner-darwin-arm64 ./cmd/runner
GOOS=linux  GOARCH=amd64 go build -o bin/runner-linux-amd64  ./cmd/runner
```

---

## Slack app

At https://api.slack.com/apps:

1. **Create New App** → from scratch.
2. **Socket Mode** → enable.
3. **App-Level Tokens** → create one with scope `connections:write` → `SLACK_APP_TOKEN` (`xapp-...`).
4. **OAuth & Permissions → Bot Token Scopes**:
   - `app_mentions:read`
   - `chat:write`
   - `reactions:write`
   - `channels:history`
5. **Install to Workspace** → copy Bot Token → `SLACK_BOT_TOKEN` (`xoxb-...`).
6. **Event Subscriptions** → subscribe to `app_mention`.
7. Create a channel that will act as the queue (e.g. `#task-queue`). Copy its ID → `SLACK_TASK_CHANNEL` (`C0123...`).
8. Invite the bot into the task channel **and** any channel you want to trigger it from:

   ```
   /invite @your-bot-name
   ```

---

## Server (always-on host)

Drop the binary on a 24/7 host with env vars loaded:

```bash
cp .env.example .env
# fill in SLACK_BOT_TOKEN, SLACK_APP_TOKEN, SLACK_TASK_CHANNEL

set -a; source .env; set +a
./bin/server
```

Socket Mode keeps an outbound WebSocket open, so no inbound port or public URL is needed.

---

## Runner (macOS)

> Linux / Windows: coming.

### 1. Install the binary

```bash
sudo install -m 0755 bin/runner /usr/local/bin/clamshell-runner
```

### 2. Install the sudoers rule

```bash
./scripts/setup-sudoers.sh
```

Prompts for your sudo password **once**, then installs `/etc/sudoers.d/clamshell-pmset` allowing `pmset schedule wake *` without a password. All other `pmset` subcommands still require a password.

### 3. Prepare runner files (not yet active)

```bash
./scripts/setup-launchd-ready.sh
```

Creates `~/.clamshell-taskq/` with a `.env` placeholder and the `run.sh` wrapper, and writes the LaunchAgent plist. **The schedule is not active yet.**

### 4. Copy your `.env` into place

Write a `.env` in the repo (see [`.env.example`](.env.example) for the shape), then copy it into the runner's directory:

```bash
cp .env ~/.clamshell-taskq/.env
chmod 600 ~/.clamshell-taskq/.env
```

The file must define:

```
SLACK_BOT_TOKEN=xoxb-...
SLACK_TASK_CHANNEL=C0123456789
COMMAND="/usr/local/bin/python3 /Users/me/handlers/main.py"
```

### 5. Activate the schedule

```bash
./scripts/setup-launchd-start.sh
```

Loads the LaunchAgent. The plist has `RunAtLoad=true`, so launchd fires the runner immediately, which kicks off the wake chain (each `run.sh` invocation registers the next wake before exiting). From this point the runner repeats every 5 minutes.

### What gets installed where

| Path | Owned by | Purpose |
|---|---|---|
| `/etc/sudoers.d/clamshell-pmset` | `setup-sudoers.sh` | `NOPASSWD` for `pmset schedule wake *` only |
| `~/.clamshell-taskq/.env` | `setup-launchd-ready.sh` | your tokens + `$COMMAND` |
| `~/.clamshell-taskq/run.sh` | `setup-launchd-ready.sh` | wrapper: source env → run runner → re-arm next wake |
| `~/Library/LaunchAgents/com.clamshell-taskq.runner.plist` | `setup-launchd-ready.sh` | `RunAtLoad` + `StartCalendarInterval` at `:00, :05, …, :55` |
| `~/.clamshell-taskq/launchd.{out,err}.log` | launchd | launchd-captured runner output |

### Verify

```bash
pmset -g sched                                 # next wake scheduled?
launchctl list | grep clamshell-taskq.runner   # LaunchAgent registered?
tail -f ~/.clamshell-taskq/launchd.{out,err}.log
```

### Uninstall

```bash
launchctl unload ~/Library/LaunchAgents/com.clamshell-taskq.runner.plist
rm ~/Library/LaunchAgents/com.clamshell-taskq.runner.plist
sudo rm /etc/sudoers.d/clamshell-pmset
sudo pmset schedule cancelall
# rm -rf ~/.clamshell-taskq                    # also wipes env/logs
```

---

## `$COMMAND` contract

The runner only checks the latest message and triggers once. The command itself owns the full processing loop:

1. **List pending messages** in the task channel via `conversations.history` (paginated). Pending = no `⏳`, `✅`, or `❌` reaction.
2. **For each pending message**:
   - Add `⏳` (`hourglass_flowing_sand`) **first** — prevents the next runner cycle from re-triggering.
   - Do the work.
   - On success → add `✅` (`white_check_mark`).
   - On failure → add `❌` (`x`), optionally reply in-thread with the error.

Sketch (Python):

```python
for msg in list_pending(channel):
    add_reaction(msg.ts, "hourglass_flowing_sand")
    try:
        do_work(msg)
        add_reaction(msg.ts, "white_check_mark")
    except Exception as e:
        add_reaction(msg.ts, "x")
        post_thread_reply(msg.ts, f"failed: {e}")
```

`launchd` does not inherit your shell PATH, so use absolute paths for the interpreter and the script. **Wrap the command with `caffeinate -i`** — after a `pmset` wake the Mac is in dark wake with a short idle timer (often under a minute), so without `caffeinate` a long-running command can get cut off when macOS idles back to sleep mid-task.

```
COMMAND="/usr/bin/caffeinate -i /usr/local/bin/python3 /Users/me/handlers/main.py"
```

### Output

| What | Where |
|---|---|
| `launchd`-captured runner output | `~/.clamshell-taskq/launchd.{out,err}.log` |
| Your `$COMMAND`'s stdout/stderr | `~/.clamshell-taskq/logs/<timestamp>.log` |

---

## Honest limits

- **macOS itself does not guarantee** every scheduled wake fires under every combination of (lid closed, on battery, deep sleep). M-series + macOS 26.x have rare firmware sleep hangs reported. Expect **most** 5-minute cycles to fire when closed-lid + sleeping; a missed cycle just means the work waits for the next one.
- Missed wakes are not lost work: the Slack channel is the queue, so the next cycle catches up on whatever piled up.

## License

MIT — see [LICENSE](LICENSE).
