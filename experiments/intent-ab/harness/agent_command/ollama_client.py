"""Minimal client for host Ollama's OpenAI-compatible chat endpoint.

Bakes in the bonsai-8b-16k handling rules from the retrieval spike
(docs/plans/spikes/2026-09-13-intent-retrieval/README.md):

- Temperature must never be 0 — temp 0 sends the reasoning model into an
  infinite reasoning loop that returns empty content. Any caller-requested
  temperature <= 0 is clamped up to MIN_TEMPERATURE.
- Generation length is capped (`max_tokens`, mapped to Ollama's
  `num_predict`) so a stuck generation can't run unbounded.
- Content that comes back empty (including empty after chain-of-thought is
  stripped elsewhere) is retried exactly once by the caller; this client
  just reports what the endpoint returned.

Uses only the standard library (urllib) — this project's containers get no
guaranteed pip/network access at build time, and the OpenAI-compatible
surface needed here is one POST and one JSON shape.
"""
import json
import urllib.error
import urllib.request

DEFAULT_BASE_URL = "http://host.docker.internal:11434/v1"
DEFAULT_MODEL = "bonsai-8b-16k"
DEFAULT_TEMPERATURE = 0.8  # Ollama's own default; deliberately not 0.
MIN_TEMPERATURE = 0.1
DEFAULT_MAX_TOKENS = 6000  # the spike's num_predict value
MAX_TOKENS_CEILING = 8192
DEFAULT_TIMEOUT_SECONDS = 120


class OllamaError(RuntimeError):
    """Raised when the Ollama endpoint can't be reached or returns garbage."""


def clamp_temperature(temperature: float) -> float:
    if temperature is None or temperature <= 0:
        return MIN_TEMPERATURE
    return temperature


def clamp_max_tokens(max_tokens: int) -> int:
    if max_tokens is None or max_tokens <= 0:
        return DEFAULT_MAX_TOKENS
    return min(max_tokens, MAX_TOKENS_CEILING)


class OllamaClient:
    def __init__(
        self,
        base_url: str = DEFAULT_BASE_URL,
        model: str = DEFAULT_MODEL,
        temperature: float = DEFAULT_TEMPERATURE,
        max_tokens: int = DEFAULT_MAX_TOKENS,
        timeout_seconds: float = DEFAULT_TIMEOUT_SECONDS,
    ):
        self.base_url = base_url.rstrip("/")
        self.model = model
        self.temperature = clamp_temperature(temperature)
        self.max_tokens = clamp_max_tokens(max_tokens)
        self.timeout_seconds = timeout_seconds

    def complete(self, system_prompt: str, user_prompt: str) -> str:
        """Run one chat completion. Returns the raw (un-stripped) content string."""
        payload = {
            "model": self.model,
            "messages": [
                {"role": "system", "content": system_prompt},
                {"role": "user", "content": user_prompt},
            ],
            "temperature": self.temperature,
            "max_tokens": self.max_tokens,
            "stream": False,
        }
        request = urllib.request.Request(
            self.base_url + "/chat/completions",
            data=json.dumps(payload).encode("utf-8"),
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        try:
            with urllib.request.urlopen(request, timeout=self.timeout_seconds) as resp:
                body = json.loads(resp.read().decode("utf-8"))
        except urllib.error.URLError as e:
            raise OllamaError(f"could not reach {self.base_url}: {e}") from e
        except json.JSONDecodeError as e:
            raise OllamaError(f"non-JSON response from {self.base_url}: {e}") from e

        try:
            return body["choices"][0]["message"]["content"] or ""
        except (KeyError, IndexError, TypeError) as e:
            raise OllamaError(f"unexpected response shape from {self.base_url}: {body!r}") from e

    def complete_with_retry(self, system_prompt: str, user_prompt: str, is_empty) -> str:
        """Run complete(), retrying exactly once if is_empty(content) is true.

        `is_empty` is supplied by the caller so the retry decision can be
        based on post-processed content (e.g. after chain-of-thought
        stripping) rather than the raw string.
        """
        content = self.complete(system_prompt, user_prompt)
        if is_empty(content):
            content = self.complete(system_prompt, user_prompt)
        return content
