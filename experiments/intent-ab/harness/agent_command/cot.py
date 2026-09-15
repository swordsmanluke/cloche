"""Strip leaked chain-of-thought from bonsai completions.

bonsai-8b-16k is a 1-bit reasoning model: it wraps (or is supposed to wrap)
internal reasoning in `<think>`/`<thinking>`/`<reasoning>` tags, but that
reasoning leaks into the answer content it hands back over the
OpenAI-compatible API instead of a separate field. If generation is cut off
by the token cap mid-thought, the closing tag never arrives — the retrieval
spike's "reasons forever, returns empty content" failure mode. Edits must
never be applied against raw text that still contains this.
"""
import re

_THINK_TAGS = ("think", "thinking", "reasoning")

# Matches a complete <tag>...</tag> block for any of _THINK_TAGS, case
# insensitive, spanning newlines.
_CLOSED_BLOCK_RE = re.compile(
    r"<(" + "|".join(_THINK_TAGS) + r")>.*?</\1>",
    re.IGNORECASE | re.DOTALL,
)

# Matches an opening tag with no matching close (generation truncated
# mid-thought) — everything from the opening tag to the end of the string
# is reasoning, since the model never got back to answering.
_UNCLOSED_OPEN_RE = re.compile(
    r"<(" + "|".join(_THINK_TAGS) + r")>.*\Z",
    re.IGNORECASE | re.DOTALL,
)


def strip_chain_of_thought(text: str) -> str:
    """Remove chain-of-thought blocks and return the remaining content, stripped."""
    if not text:
        return ""
    cleaned = _CLOSED_BLOCK_RE.sub("", text)
    cleaned = _UNCLOSED_OPEN_RE.sub("", cleaned)
    return cleaned.strip()
