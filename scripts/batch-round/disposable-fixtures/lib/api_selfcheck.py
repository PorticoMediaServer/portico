#!/usr/bin/env python3
"""Offline wire self-check for lib/api.py.

A loopback mock returns the real `{protocolVersion, serverId, result}` envelope
and the ordinary setup route shapes. It asserts the client's unwrapping,
headers, setup order and error parsing. It never touches a Portico server.
"""

import json
import os
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

sys.path.insert(0, os.path.dirname(__file__))
import api  # noqa: E402

SEEN = []


def envelope(result):
    return {"protocolVersion": "1.0", "serverId": "srv-fixture", "result": result}


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):
        return

    def _reply(self, status, payload):
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _body(self):
        length = int(self.headers.get("Content-Length", "0"))
        return json.loads(self.rfile.read(length) or b"{}") if length else None

    def do_GET(self):
        SEEN.append((self.command, self.path, dict(self.headers)))
        if self.path == "/v1/setup/state":
            return self._reply(200, envelope({"ready": False, "revision": 3}))
        if self.path == "/v1/libraries":
            return self._reply(200, envelope({"items": [{"id": "lib1"}]}))
        if self.path == "/v1/libraries/lib1/browse-capabilities":
            return self._reply(200, envelope({"resolvedPivot": {"id": "movies"}, "pivots": []}))
        if self.path == "/v1/admin/console/settings":
            return self._reply(200, envelope({"scope": {"serverId": "srv-fixture"},
                                              "data": {"revision": 7,
                                                       "requested": {"conversionMode": "all", "memoryCap": "quarter"},
                                                       "effective": {"conversionMode": "all", "memoryCap": "quarter"}}}))
        if self.path == "/v1/admin/playback/diagnostics":
            return self._reply(200, envelope({"diagnostics": {"memorySpills": 2}}))
        return self._reply(404, {"error": {"code": "not_found"}})

    def do_POST(self):
        body = self._body()
        SEEN.append((self.command, self.path, dict(self.headers), body))
        if self.path == "/v1/setup/app":
            assert self.headers.get("Origin") is None, "app setup must not carry Origin"
            assert self.headers.get("X-Portico-Installation-Id"), "installation header missing"
            assert (self.headers.get("Content-Type") or "").startswith("application/json")
            return self._reply(200, envelope({"serverId": "srv-fixture", "setupToken": "setup-token"}))
        if self.path == "/v1/setup":
            assert body["setupToken"] == "setup-token"
            assert body["authMode"] == "local" and body["interactive"] is True
            assert body.get("requestId")
            return self._reply(201, envelope({"session": {
                "accessToken": "token-1", "refreshToken": "refresh-1",
                "installationId": "installation-1", "serverId": "srv-fixture",
                "expiresAt": "2030-01-01T00:00:00Z"}}))
        if self.path == "/v1/setup/finish":
            assert body["revision"] == 3
            return self._reply(200, envelope({}))
        if self.path == "/v1/libraries/lib1/browse":
            assert body["pivot"] == "movies"
            return self._reply(200, envelope({"entries": [{"id": "item1", "title": "Direct", "kind": "movie"}],
                                              "pageInfo": {}}))
        if self.path == "/v1/admin/console/settings":
            assert body["expectedRevision"] == 7
            assert body["values"]["memoryCap"] == "2gb"
            assert body["idempotencyKey"]
            return self._reply(200, envelope({"data": {"effective": {"memoryCap": "2gb"}}}))
        if self.path == "/v1/playback/sessions":
            assert self.headers.get("Portico-Playback") == "2"
            assert self.headers.get("Idempotency-Key")
            assert self.headers.get("Origin")
            return self._reply(201, envelope({"id": "session-1", "revision": "1",
                                              "presentation": {"generation": 1, "mode": "direct"}}))
        return self._reply(400, {"error": {"code": "fixture_refusal", "retryable": True}})

    def do_PATCH(self):
        return self.do_POST()


def main():
    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    server.daemon_threads = True
    threading.Thread(target=server.serve_forever, daemon=True).start()
    base = "http://127.0.0.1:%d" % server.server_address[1]
    try:
        client = api.bootstrap(base, "owner", "long-password", "Fixture owner")
        assert client.token == "token-1"
        assert client.installation == "installation-1"
        assert client.state["serverId"] == "srv-fixture"
        libs = client.request("GET", "/v1/libraries")
        assert libs["items"][0]["id"] == "lib1"
        caps = client.request("GET", "/v1/libraries/lib1/browse-capabilities")
        assert caps["resolvedPivot"]["id"] == "movies"
        entries = api.browse_items(client, "lib1")
        assert entries[0]["id"] == "item1"
        doc = api.patch_settings(client, {"memoryCap": "2gb"}, idempotency_key="op-1")
        assert doc["effective"]["memoryCap"] == "2gb"
        assert api.diagnostics(client)["memorySpills"] == 2
        # A loopback fake response must not stand in for canonical playback.
        # Missing qualified inputs refuses instead of reverting to raw commands.
        try:
            api.start_session(client, "item1", {"mode": "original"})
            raise AssertionError("missing canonical playback inputs accepted")
        except SystemExit:
            pass
        try:
            client.request("POST", "/v1/nowhere", {})
            raise AssertionError("expected an ApiError")
        except api.ApiError as err:
            assert err.code == "fixture_refusal" and err.retryable is True
    finally:
        server.shutdown()
    print("PASS api client envelope, setup order, headers, settings and error parsing")
    return 0


if __name__ == "__main__":
    sys.exit(main())
