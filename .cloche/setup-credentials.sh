#!/usr/bin/env bash
# Image-build helper: configures the agent user's git identity, SSH key,
# and (optionally) gh CLI auth from files staged under /tmp/cloche-credentials/.
#
# Invoked from .cloche/Dockerfile after COPY-ing .cloche/credentials/ into
# /tmp/cloche-credentials/. All files under that path are optional — when none
# are present the script does nothing beyond installing known_hosts so SSH
# operations against github.com still work with whatever identity the host
# wires in at runtime.
#
# Recognized files in /tmp/cloche-credentials/:
#
#   gituser.toml   Identity + key reference. Recognized keys:
#                    name    = "..."      git user.name
#                    email   = "..."      git user.email
#                    ssh_key = "..."      basename of an SSH private key in
#                                         this directory (e.g. "id_ed25519").
#                                         The .pub counterpart is required.
#   <ssh_key>      The private key file referenced by gituser.toml.
#   <ssh_key>.pub  Matching public key.
#   gh_token       GitHub PAT for `gh` CLI HTTPS operations. Optional.
set -euo pipefail

CREDS=/tmp/cloche-credentials

install -d -m 700 -o agent -g agent /home/agent/.ssh
ssh-keyscan -H github.com >> /home/agent/.ssh/known_hosts 2>/dev/null || true
chmod 644 /home/agent/.ssh/known_hosts
chown agent:agent /home/agent/.ssh/known_hosts

toml_value() {
    # $1: key, $2: file. Extracts a quoted string value from a flat TOML file.
    sed -n "s/^[[:space:]]*$1[[:space:]]*=[[:space:]]*\"\\([^\"]*\\)\".*$/\\1/p" "$2" | head -1
}

if [ -f "$CREDS/gituser.toml" ]; then
    name=$(toml_value name    "$CREDS/gituser.toml")
    email=$(toml_value email  "$CREDS/gituser.toml")
    sshkey=$(toml_value ssh_key "$CREDS/gituser.toml")

    [ -n "$name" ]  && gosu agent git config --global user.name  "$name"
    [ -n "$email" ] && gosu agent git config --global user.email "$email"

    if [ -n "$sshkey" ] && [ -f "$CREDS/$sshkey" ]; then
        install -m 600 -o agent -g agent "$CREDS/$sshkey"     /home/agent/.ssh/id_ed25519
        install -m 644 -o agent -g agent "$CREDS/$sshkey.pub" /home/agent/.ssh/id_ed25519.pub
        printf 'Host github.com\n  IdentityFile /home/agent/.ssh/id_ed25519\n  IdentitiesOnly yes\n  User git\n' \
            > /home/agent/.ssh/config
        chmod 600 /home/agent/.ssh/config
        chown agent:agent /home/agent/.ssh/config
    fi
fi

if [ -f "$CREDS/gh_token" ]; then
    install -m 600 -o agent -g agent "$CREDS/gh_token" /home/agent/.gh_token
fi
