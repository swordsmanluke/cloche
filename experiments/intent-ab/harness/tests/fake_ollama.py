"""A minimal fake of Ollama's OpenAI-compatible /chat/completions endpoint,
used to test agent_command without a real Ollama + bonsai model available.
"""
import json
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer


class FakeOllamaServer:
    """Serves scripted responses and records every request payload received.

    `responses` is a list of content strings (or exceptions) returned in
    order, one per request; the last one repeats if there are more requests
    than scripted responses.
    """

    def __init__(self, responses):
        self.responses = responses
        self.requests = []
        self._lock = threading.Lock()
        server = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass  # silence default request logging

            def do_POST(self):
                length = int(self.headers.get("Content-Length", 0))
                body = json.loads(self.rfile.read(length))
                with server._lock:
                    server.requests.append(body)
                    index = min(len(server.requests) - 1, len(server.responses) - 1)
                    content = server.responses[index]
                response = {
                    "choices": [{"message": {"role": "assistant", "content": content}}]
                }
                payload = json.dumps(response).encode("utf-8")
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(payload)))
                self.end_headers()
                self.wfile.write(payload)

        self._httpd = HTTPServer(("127.0.0.1", 0), Handler)
        self._thread = threading.Thread(target=self._httpd.serve_forever, daemon=True)

    def __enter__(self):
        self._thread.start()
        return self

    def __exit__(self, *exc_info):
        self._httpd.shutdown()
        self._httpd.server_close()

    @property
    def base_url(self):
        host, port = self._httpd.server_address
        return f"http://{host}:{port}/v1"
