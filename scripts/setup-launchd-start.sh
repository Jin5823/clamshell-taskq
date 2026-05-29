#!/usr/bin/env bash
# Activate the LaunchAgent that scripts/setup-launchd-ready.sh prepared.
#
# Loads the plist into launchd. Because the plist has RunAtLoad=true,
# launchd fires the runner immediately, which kicks off the wake chain
# (each run.sh invocation registers the next wake before exiting).
#
# Re-running is safe: the agent is unloaded first.

set -euo pipefail

if [[ "$(uname)" != "Darwin" ]]; then
    echo "error: macOS only" >&2
    exit 1
fi

HOME_DIR=$HOME
PLIST_PATH="$HOME_DIR/Library/LaunchAgents/com.clamshell-taskq.runner.plist"
ENV_FILE="$HOME_DIR/.clamshell-taskq/.env"
LOG_OUT="$HOME_DIR/.clamshell-taskq/launchd.out.log"
LOG_ERR="$HOME_DIR/.clamshell-taskq/launchd.err.log"

if [[ ! -f "$PLIST_PATH" ]]; then
    cat <<EOF >&2
error: LaunchAgent plist not found at $PLIST_PATH

Run the prep script first:
    ./scripts/setup-launchd-ready.sh
EOF
    exit 1
fi

if [[ ! -f "$ENV_FILE" ]]; then
    cat <<EOF >&2
error: $ENV_FILE not found.

Copy your .env into place first:
    cp .env $ENV_FILE
    chmod 600 $ENV_FILE
EOF
    exit 1
fi

if grep -q 'xoxb-\.\.\.' "$ENV_FILE" 2>/dev/null; then
    cat <<EOF >&2
error: $ENV_FILE still contains placeholder values.

Fill in your real Slack tokens and \$COMMAND first, then re-run this script.
EOF
    exit 1
fi

echo "==> Activating LaunchAgent"

launchctl unload "$PLIST_PATH" 2>/dev/null || true
launchctl load   "$PLIST_PATH"
echo "    loaded $PLIST_PATH"
echo "    runner is firing now (RunAtLoad=true) and will repeat every 5 minutes"

cat <<EOF

==> Done.

Check schedule:      pmset -g sched
Check agent:         launchctl list | grep clamshell-taskq.runner
Tail launchd logs:   tail -f $LOG_OUT $LOG_ERR

To stop:
  launchctl unload "$PLIST_PATH"
EOF
