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

# Bake in cloche-bot credentials so the agent can push branches and open PRs as
# the cloche-bot github user (cloche@perfectlunacy.com), not the developer's own
# identity. The credentials live in .cloche/credentials/ on the host (gitignored,
# clocheignored) and are copied into /home/agent/.ssh/ here. The image stays on
# this host — never push it to a registry.
#
# Files in .cloche/credentials/ (only id_ed25519 is required):
#   id_ed25519       — private SSH key (mode 600)
#   id_ed25519.pub   — public SSH key (mode 644)
#   gh_token         — optional; PAT for `gh` CLI (HTTPS) operations
COPY .cloche/credentials/ /tmp/cloche-credentials/
RUN install -d -m 700 -o agent -g agent /home/agent/.ssh \
    && install -m 600 -o agent -g agent /tmp/cloche-credentials/id_ed25519 /home/agent/.ssh/id_ed25519 \
    && install -m 644 -o agent -g agent /tmp/cloche-credentials/id_ed25519.pub /home/agent/.ssh/id_ed25519.pub \
    && ssh-keyscan -H github.com >> /home/agent/.ssh/known_hosts 2>/dev/null \
    && chmod 644 /home/agent/.ssh/known_hosts \
    && chown agent:agent /home/agent/.ssh/known_hosts \
    && printf 'Host github.com\n  IdentityFile /home/agent/.ssh/id_ed25519\n  IdentitiesOnly yes\n  User git\n' \
       > /home/agent/.ssh/config \
    && chmod 600 /home/agent/.ssh/config \
    && chown agent:agent /home/agent/.ssh/config \
    && if [ -f /tmp/cloche-credentials/gh_token ]; then \
         install -m 600 -o agent -g agent /tmp/cloche-credentials/gh_token /home/agent/.gh_token; \
       fi \
    && rm -rf /tmp/cloche-credentials

# Bot git identity for the agent user. Layer agents inherit this when they commit.
RUN gosu agent git config --global user.email "cloche@perfectlunacy.com" \
    && gosu agent git config --global user.name "cloche-bot"

# If a gh token was baked in, log gh in and let it set up the HTTPS git credential
# helper. Failure is non-fatal so images without a token still build (SSH-only
# operation still works via the IdentityFile in ~/.ssh/config).
RUN if [ -f /home/agent/.gh_token ]; then \
      gosu agent gh auth login --with-token < /home/agent/.gh_token \
        && gosu agent gh auth setup-git \
        || echo "warning: gh auth login failed; continuing with SSH-only auth" >&2; \
    fi

USER agent
