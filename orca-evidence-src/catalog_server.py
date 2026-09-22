#!/usr/bin/env python3
"""Serve the OrcaRouter catalog fixture on a loopback port.

This stands in for the live inference origin during UI evidence generation so
the run is deterministic and needs no credential. It answers the same two
routes the real origin does: GET /v1/models (with the optional capability
filter) and POST /v1/chat/completions.
"""

import json
import os
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

CATALOG = json.loads(os.environ["ORCA_CATALOG_JSON"])


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):  # keep the evidence run quiet
        pass

    def _json(self, payload, status=200):
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if not self.path.startswith("/v1/models"):
            self._json({"error": "not found"}, 404)
            return
        if "Authorization" not in self.headers:
            self._json({"error": "missing bearer token"}, 401)
            return
        self._json(CATALOG)

    def do_POST(self):
        if self.path != "/v1/chat/completions":
            self._json({"error": "not found"}, 404)
            return
        length = int(self.headers.get("Content-Length") or 0)
        self.rfile.read(length)
        self._json(
            {
                "id": "evidence",
                "object": "chat.completion",
                "model": "openai/gpt-5.5",
                "choices": [
                    {
                        "index": 0,
                        "message": {"role": "assistant", "content": "ready"},
                        "finish_reason": "stop",
                    }
                ],
            }
        )


if __name__ == "__main__":
    ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), Handler).serve_forever()
