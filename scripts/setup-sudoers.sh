#!/usr/bin/env bash
# Install narrow-scope sudoers rules for the two things the runner has to do
# without a password: re-arm the next pmset wake, and keep the machine awake
# while a task runs.
#
# Scope:
#   /usr/bin/pmset schedule wake *
#   /usr/bin/pmset -a disablesleep 0
#   /usr/bin/pmset -a disablesleep 1
#
# The two disablesleep values are spelled out instead of wildcarded so the
# rule cannot be used to pass arbitrary arguments to pmset. All other pmset
# subcommands (sleepnow, hibernatemode, ...) still require a password.
#
# Idempotent: re-running overwrites the existing rule.

set -euo pipefail

if [[ "$(uname)" != "Darwin" ]]; then
    echo "error: macOS only" >&2
    exit 1
fi

USER_NAME=$(whoami)
SUDOERS_PATH="/etc/sudoers.d/clamshell-pmset"

echo "==> Installing sudoers rule for user: $USER_NAME"

TMP=$(mktemp)
trap 'rm -f "$TMP"' EXIT

cat > "$TMP" <<EOF
# clamshell-taskq: let $USER_NAME re-arm the next wake and keep the machine
# awake while a task runs, without a password. Only these three exact forms
# are allowed.
$USER_NAME ALL=(root) NOPASSWD: /usr/bin/pmset schedule wake *
$USER_NAME ALL=(root) NOPASSWD: /usr/bin/pmset -a disablesleep 0
$USER_NAME ALL=(root) NOPASSWD: /usr/bin/pmset -a disablesleep 1
EOF

if ! sudo visudo -c -f "$TMP" >/dev/null; then
    echo "error: sudoers snippet failed syntax check" >&2
    exit 1
fi

echo "==> sudo password required once to install $SUDOERS_PATH"
sudo install -m 0440 -o root -g wheel "$TMP" "$SUDOERS_PATH"

# Validate only our installed file (-f path) instead of the whole sudoers
# tree. The whole-tree check can fail on unrelated broken files left by
# other tools and would force us to roll back our valid file.
if ! sudo visudo -c -f "$SUDOERS_PATH" >/dev/null; then
    echo "error: installed sudoers file failed validation — rolling back" >&2
    sudo rm -f "$SUDOERS_PATH"
    exit 1
fi

cat <<EOF

==> Done.
    installed $SUDOERS_PATH

To uninstall:
    sudo rm $SUDOERS_PATH
EOF
