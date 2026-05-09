#!/usr/bin/env bash
# agent-creds.sh — source this from any host script that needs to push to git
# or use the `gh` CLI. Configures the environment to use the cloche-bot
# credentials baked into .cloche/credentials/ rather than the developer's own.
#
# Usage (at the top of a host script, after `set -euo pipefail`):
#   source "$(dirname "${BASH_SOURCE[0]}")/lib/agent-creds.sh"
#
# What it does:
#   - If .cloche/credentials/id_ed25519 exists, exports GIT_SSH_COMMAND to force
#     `git push` (over an SSH origin URL like git@github.com:...) through the
#     bot's key, overriding whatever the user's ssh-agent would have picked.
#   - If .cloche/credentials/gh_token exists, exports GH_TOKEN so the `gh` CLI
#     authenticates as the bot rather than the developer.
#
# Both files are optional individually — SSH-only and HTTPS+token-only setups
# are both valid. If a script needs to push but neither is present, it'll
# fall through to the developer's credentials with a warning.

# Resolve project dir. CLOCHE_PROJECT_DIR is set by the daemon for workflow
# steps; for ad-hoc invocation we fall back to walking up from the script.
_creds_project_dir="${CLOCHE_PROJECT_DIR:-}"
if [ -z "$_creds_project_dir" ]; then
  _creds_project_dir=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
fi
_creds_dir="${_creds_project_dir}/.cloche/credentials"

if [ -f "$_creds_dir/id_ed25519" ]; then
  export GIT_SSH_COMMAND="ssh -i $_creds_dir/id_ed25519 -o IdentitiesOnly=yes -o UserKnownHostsFile=$_creds_dir/known_hosts -o StrictHostKeyChecking=accept-new"
  # Make sure known_hosts exists so accept-new doesn't error out.
  touch "$_creds_dir/known_hosts"
  chmod 644 "$_creds_dir/known_hosts" 2>/dev/null || true
fi

if [ -f "$_creds_dir/gh_token" ]; then
  GH_TOKEN=$(cat "$_creds_dir/gh_token")
  export GH_TOKEN
fi

if [ -z "${GIT_SSH_COMMAND:-}" ] && [ -z "${GH_TOKEN:-}" ]; then
  echo "warning: no cloche-bot credentials found at $_creds_dir — git/gh operations will run as the current user" >&2
fi

unset _creds_project_dir _creds_dir
