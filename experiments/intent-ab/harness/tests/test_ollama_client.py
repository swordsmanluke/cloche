import socket
import unittest

from agent_command.ollama_client import (
    MIN_TEMPERATURE,
    DEFAULT_MAX_TOKENS,
    MAX_TOKENS_CEILING,
    OllamaClient,
    OllamaError,
    clamp_max_tokens,
    clamp_temperature,
)
from tests.fake_ollama import FakeOllamaServer


class TestClamping(unittest.TestCase):
    def test_temperature_zero_is_clamped(self):
        # The retrieval spike's core bonsai finding: temp 0 sends the
        # reasoning model into an infinite loop that returns empty content.
        self.assertEqual(clamp_temperature(0), MIN_TEMPERATURE)

    def test_negative_temperature_is_clamped(self):
        self.assertEqual(clamp_temperature(-1.0), MIN_TEMPERATURE)

    def test_none_temperature_is_clamped(self):
        self.assertEqual(clamp_temperature(None), MIN_TEMPERATURE)

    def test_positive_temperature_passthrough(self):
        self.assertEqual(clamp_temperature(0.7), 0.7)

    def test_max_tokens_default_when_unset(self):
        self.assertEqual(clamp_max_tokens(0), DEFAULT_MAX_TOKENS)
        self.assertEqual(clamp_max_tokens(None), DEFAULT_MAX_TOKENS)

    def test_max_tokens_capped_at_ceiling(self):
        self.assertEqual(clamp_max_tokens(999999), MAX_TOKENS_CEILING)

    def test_max_tokens_passthrough_within_bounds(self):
        self.assertEqual(clamp_max_tokens(500), 500)


class TestOllamaClientOverTheWire(unittest.TestCase):
    def test_complete_extracts_content_and_never_sends_zero_temperature(self):
        with FakeOllamaServer(["the answer"]) as server:
            client = OllamaClient(base_url=server.base_url, temperature=0, timeout_seconds=5)
            content = client.complete("system", "user")
            self.assertEqual(content, "the answer")
            self.assertEqual(len(server.requests), 1)
            self.assertGreater(server.requests[0]["temperature"], 0)

    def test_max_tokens_capped_in_actual_request(self):
        with FakeOllamaServer(["ok"]) as server:
            client = OllamaClient(base_url=server.base_url, max_tokens=999999, timeout_seconds=5)
            client.complete("system", "user")
            self.assertEqual(server.requests[0]["max_tokens"], MAX_TOKENS_CEILING)

    def test_complete_with_retry_retries_once_on_empty(self):
        with FakeOllamaServer(["", "second try content"]) as server:
            client = OllamaClient(base_url=server.base_url, timeout_seconds=5)
            content = client.complete_with_retry("system", "user", lambda c: c == "")
            self.assertEqual(content, "second try content")
            self.assertEqual(len(server.requests), 2)

    def test_complete_with_retry_does_not_retry_twice(self):
        # Both attempts empty: exactly two calls total, not more.
        with FakeOllamaServer(["", ""]) as server:
            client = OllamaClient(base_url=server.base_url, timeout_seconds=5)
            content = client.complete_with_retry("system", "user", lambda c: c == "")
            self.assertEqual(content, "")
            self.assertEqual(len(server.requests), 2)

    def test_unreachable_endpoint_raises_ollama_error(self):
        # Bind then immediately close, so the port is very likely free but
        # nothing accepts connections on it.
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
        sock.close()

        client = OllamaClient(base_url=f"http://127.0.0.1:{port}", timeout_seconds=2)
        with self.assertRaises(OllamaError):
            client.complete("system", "user")


if __name__ == "__main__":
    unittest.main()
