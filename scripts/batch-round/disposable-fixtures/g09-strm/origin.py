#!/usr/bin/env python3
"""Approved-root HTTP(S) media origin for G09.

Strong ETags, byte ranges, If-Match/If-None-Match, redirects, per-file offline
mode and same-size/same-mtime replacement. A separate control listener mutates
state; every request is logged without query strings so evidence can prove which
origins were actually contacted.
"""

import argparse
import hashlib
import json
import os
import re
import ssl
import threading
import time
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

STATE_LOCK = threading.Lock()


class State:
    def __init__(self, root, log_path, token):
        self.root = root
        self.log_path = log_path
        self.token = token
        self.files = {}
        self.offline = set()
        self.bad_redirect = "http://127.0.0.1:9/unapproved"
        self.lock = threading.Lock()
        self.refresh()

    def refresh(self):
        with self.lock:
            for name in os.listdir(self.root):
                path = os.path.join(self.root, name)
                if not os.path.isfile(path):
                    continue
                st = os.stat(path)
                digest = hashlib.sha256()
                with open(path, "rb") as handle:
                    for chunk in iter(lambda: handle.read(1 << 20), b""):
                        digest.update(chunk)
                self.files[name] = {
                    "path": path,
                    "size": st.st_size,
                    "mtimeNs": st.st_mtime_ns,
                    "etag": '"sha256-%s"' % digest.hexdigest(),
                }

    def entry(self, name):
        with self.lock:
            return self.files.get(name)

    def replace(self, name, source, keep_size, keep_mtime):
        with self.lock:
            old = self.files.get(name)
            if old is None:
                raise ValueError("unknown file")
            data = open(source, "rb").read()
            if keep_size:
                if len(data) > old["size"]:
                    data = data[: old["size"]]
                else:
                    data = data + b"\0" * (old["size"] - len(data))
            target = old["path"]
            tmp = target + ".tmp"
            with open(tmp, "wb") as handle:
                handle.write(data)
            os.replace(tmp, target)
            if keep_mtime:
                os.utime(target, ns=(old["mtimeNs"], old["mtimeNs"]))
            st = os.stat(target)
            self.files[name] = {
                "path": target,
                "size": st.st_size,
                "mtimeNs": st.st_mtime_ns,
                "etag": '"sha256-%s"' % hashlib.sha256(data).hexdigest(),
            }
            return self.files[name]

    def log(self, record):
        with STATE_LOCK:
            with open(self.log_path, "a", encoding="utf-8") as handle:
                handle.write(json.dumps(record) + "\n")


def parse_range(header, size):
    match = re.fullmatch(r"bytes=(\d*)-(\d*)", header or "")
    if not match:
        return None
    first, last = match.groups()
    if first == "" and last == "":
        return None
    if first == "":
        length = int(last)
        if length <= 0:
            return "unsatisfiable"
        return (max(0, size - length), size - 1)
    start = int(first)
    end = int(last) if last else size - 1
    if start >= size:
        return "unsatisfiable"
    return (start, min(end, size - 1))


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    state = None

    def log_message(self, *args):  # keep stdout quiet; JSONL is the receipt
        return

    def _record(self, status, body_len=0, etag=""):
        self.state.log({
            "t": time.time(),
            "client": self.client_address[0],
            "method": self.command,
            "path": urllib.parse.urlsplit(self.path).path,
            "range": self.headers.get("Range", ""),
            "status": status,
            "bytes": body_len,
            "etag": etag,
            "userAgent": self.headers.get("User-Agent", ""),
        })

    def _error(self, status, code, retry=False):
        body = json.dumps({"error": {"code": code, "retryable": retry}}).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        if retry:
            self.send_header("Retry-After", "2")
        self.end_headers()
        self.wfile.write(body)
        self._record(status, len(body))

    def _serve(self, name, head_only=False):
        entry = self.state.entry(name)
        if entry is None:
            return self._error(404, "not_found")
        if name in self.state.offline:
            return self._error(503, "source_unavailable", retry=True)
        etag = entry["etag"]
        if self.headers.get("If-Match") and self.headers["If-Match"] != etag:
            return self._error(412, "representation_changed")
        if self.headers.get("If-None-Match") == etag:
            self.send_response(304)
            self.send_header("ETag", etag)
            self.end_headers()
            return self._record(304, 0, etag)
        size = entry["size"]
        selected = parse_range(self.headers.get("Range"), size)
        if selected == "unsatisfiable":
            self.send_response(416)
            self.send_header("Content-Range", "bytes */%d" % size)
            self.send_header("Content-Length", "0")
            self.end_headers()
            return self._record(416, 0, etag)
        start, end = selected if selected else (0, size - 1)
        length = max(0, end - start + 1)
        status = 206 if selected else 200
        self.send_response(status)
        self.send_header("Accept-Ranges", "bytes")
        self.send_header("ETag", etag)
        self.send_header("Last-Modified", time.strftime(
            "%a, %d %b %Y %H:%M:%S GMT", time.gmtime(entry["mtimeNs"] / 1e9)))
        self.send_header("Content-Type", "application/octet-stream")
        self.send_header("Content-Length", str(length))
        if selected:
            self.send_header("Content-Range", "bytes %d-%d/%d" % (start, end, size))
        self.end_headers()
        if not head_only and length:
            with open(entry["path"], "rb") as handle:
                handle.seek(start)
                remaining = length
                while remaining:
                    chunk = handle.read(min(64 << 10, remaining))
                    if not chunk:
                        break
                    self.wfile.write(chunk)
                    remaining -= len(chunk)
        self._record(status, length, etag)

    def do_HEAD(self):
        path = urllib.parse.urlsplit(self.path).path
        if path.startswith("/media/"):
            return self._serve(path[len("/media/"):], head_only=True)
        return self._error(404, "not_found")

    def do_GET(self):
        path = urllib.parse.urlsplit(self.path).path
        if path.startswith("/media/"):
            return self._serve(path[len("/media/"):])
        if path == "/redirect/approved":
            self.send_response(302)
            self.send_header("Location", "/media/direct.mp4")
            self.send_header("Content-Length", "0")
            self.end_headers()
            return self._record(302)
        if path == "/redirect/unapproved":
            self.send_response(302)
            self.send_header("Location", self.state.bad_redirect)
            self.send_header("Content-Length", "0")
            self.end_headers()
            return self._record(302)
        if path == "/redirect/metadata":
            self.send_response(302)
            self.send_header("Location", "http://169.254.169.254/latest/meta-data/")
            self.send_header("Content-Length", "0")
            self.end_headers()
            return self._record(302)
        return self._error(404, "not_found")


class ControlHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    state = None

    def log_message(self, *args):
        return

    def _json(self, status, payload):
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _authorized(self):
        if self.headers.get("X-Fixture-Token") != self.state.token:
            self._json(403, {"error": "forbidden"})
            return False
        return True

    def do_GET(self):
        if not self._authorized():
            return
        if urllib.parse.urlsplit(self.path).path != "/state":
            return self._json(404, {"error": "not_found"})
        with self.state.lock:
            public = {name: {"size": e["size"], "mtimeNs": e["mtimeNs"], "etag": e["etag"],
                             "offline": name in self.state.offline}
                      for name, e in self.state.files.items()}
        return self._json(200, {"files": public, "badRedirect": self.state.bad_redirect})

    def do_POST(self):
        if not self._authorized():
            return
        length = int(self.headers.get("Content-Length", "0"))
        body = json.loads(self.rfile.read(length) or b"{}")
        path = urllib.parse.urlsplit(self.path).path
        if path == "/refresh":
            self.state.refresh()
            return self._json(200, {"ok": True})
        if path == "/replace":
            try:
                entry = self.state.replace(body["name"], body["source"],
                                           bool(body.get("keepSize", True)),
                                           bool(body.get("keepMtime", True)))
            except (KeyError, OSError, ValueError) as err:
                return self._json(400, {"error": str(err)})
            return self._json(200, {"etag": entry["etag"], "size": entry["size"],
                                    "mtimeNs": entry["mtimeNs"]})
        if path == "/offline":
            name = body.get("name")
            with self.state.lock:
                if body.get("offline", True):
                    self.state.offline.add(name)
                else:
                    self.state.offline.discard(name)
            return self._json(200, {"ok": True, "offline": sorted(self.state.offline)})
        if path == "/redirect":
            with self.state.lock:
                self.state.bad_redirect = body["target"]
            return self._json(200, {"ok": True})
        return self._json(404, {"error": "not_found"})


def serve_in_thread(handler, port, cert=None, key=None):
    server = ThreadingHTTPServer(("0.0.0.0", port), handler)
    server.daemon_threads = True
    if cert:
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(cert, key)
        server.socket = context.wrap_socket(server.socket, server_side=True)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", required=True)
    parser.add_argument("--port", type=int, default=8080)
    parser.add_argument("--control-port", type=int, default=8081)
    parser.add_argument("--log", required=True)
    parser.add_argument("--token-file", required=True)
    parser.add_argument("--tls-cert")
    parser.add_argument("--tls-key")
    args = parser.parse_args()
    token = open(args.token_file, "r", encoding="utf-8").read().strip()
    if os.path.exists(args.log):
        raise SystemExit("refusing to append to an existing origin log")
    os.makedirs(os.path.dirname(os.path.abspath(args.log)), mode=0o700, exist_ok=True)
    fd = os.open(args.log, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    os.close(fd)
    state = State(args.root, args.log, token)
    Handler.state = state
    ControlHandler.state = state
    serve_in_thread(ControlHandler, args.control_port)
    serve_in_thread(Handler, args.port, args.tls_cert, args.tls_key)
    print("media origin on %s, control on %s" % (args.port, args.control_port), flush=True)
    while True:
        time.sleep(3600)


if __name__ == "__main__":
    main()
