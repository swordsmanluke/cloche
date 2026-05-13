FROM cloche-base:latest
USER root
COPY --from=golang:1.25 /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:${PATH}"

# Install Node, gh, and openssh-client.
RUN apt-get update \
    && apt-get install -y --no-install-recommends curl openssh-client gnupg \
    && curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
    && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
       -o /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
       > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends nodejs gh \
    && rm -rf /var/lib/apt/lists/*

RUN npm install -g @anthropic-ai/claude-code

# Optionally bake in a fixed git identity + SSH key so the agent can push
# branches and open PRs as a dedicated bot account instead of the developer's.
# This is only needed for the cloche project's own `vertical` workflow;
# downstream users of cloche don't need it.
#
# Configuration lives in .cloche/credentials/gituser.toml (gitignored), which
# names a key file in the same directory:
#
#   name    = "cloche-bot"
#   email   = "cloche@perfectlunacy.com"
#   ssh_key = "id_ed25519"
#
# When .cloche/credentials/ is empty or gituser.toml is absent, this step
# completes without configuring any identity — the agent uses whatever the
# host wires in via [git] in config.toml at run time. The Makefile creates
# an empty .cloche/credentials/ before `docker build` so the COPY below
# always has a source. See .cloche/setup-credentials.sh for the full schema.
COPY .cloche/credentials/    /tmp/cloche-credentials/
COPY .cloche/setup-credentials.sh /tmp/setup-credentials.sh
RUN bash /tmp/setup-credentials.sh \
    && rm -rf /tmp/cloche-credentials /tmp/setup-credentials.sh

# If a gh token was baked in, log gh in and let it set up the HTTPS git credential
# helper. Failure is non-fatal so images without a token still build (SSH-only
# operation still works via the IdentityFile in ~/.ssh/config).
RUN if [ -f /home/agent/.gh_token ]; then \
      gosu agent gh auth login --with-token < /home/agent/.gh_token \
        && gosu agent gh auth setup-git \
        || echo "warning: gh auth login failed; continuing with SSH-only auth" >&2; \
    fi

USER agent
