#!/usr/bin/env python3
"""Stdlib-only Portico API client for the disposable fixtures.

Cross-checked against the retained P2-53 fixture (`seed_state.py`) for the real
wire conventions: the `{protocolVersion, serverId, result}` response envelope,
`X-Portico-Installation-Id`, `Origin`, and the ordinary first-run setup flow.
No third-party modules, no secret printing, no body echo on failure.
"""

import json
import atexit
import subprocess
import pathlib
import select
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

PLAYBACK_HEADER = "2"
ENVELOPE_KEYS = {"protocolVersion", "serverId", "result"}


class ApiError(Exception):
    def __init__(self, status, code="", path="", retryable=False):
        self.status = status
        self.code = code
        self.path = path
        self.retryable = retryable
        super().__init__("HTTP %s %s %s" % (status, code, path))


def _private_read(path):
    st = os.lstat(path)
    if os.path.islink(path) or not os.path.isfile(path):
        raise SystemExit("private input must be a regular file")
    if st.st_mode & 0o777 != 0o600 or st.st_uid != os.getuid():
        raise SystemExit("private input must be operator-owned mode 0600")
    return open(path, "r", encoding="utf-8").read()


def _private_write(path, text):
    directory = os.path.dirname(os.path.abspath(path)) or "."
    os.makedirs(directory, mode=0o700, exist_ok=True)
    fd = os.open(path, os.O_CREAT | os.O_TRUNC | os.O_WRONLY, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as out:
        out.write(text)
    os.chmod(path, 0o600)


def unwrap(document):
    if isinstance(document, dict) and ENVELOPE_KEYS <= set(document):
        return document["result"]
    return document


class Api:
    def __init__(self, base, token=None, state=None):
        self.base = base.rstrip("/")
        u = urllib.parse.urlsplit(self.base)
        if u.scheme not in ("http", "https") or not u.hostname:
            raise SystemExit("base must be an http(s) origin")
        self.state = state or {}
        self.token = token or self.state.get("accessToken")
        self._state_path = None
        self._session_bridge = None
        self.installation = self.state.get("installationId") or str(uuid.uuid4())

    # -- wire ---------------------------------------------------------------
    def request(self, method, path, body=None, headers=None, expect=(200,),
                allow=(), origin=True, timeout=60):
        url = self.base + path
        data = None
        hdrs = {"Accept": "application/json",
                "X-Portico-Installation-Id": self.installation}
        if origin:
            hdrs["Origin"] = self.base
        if self.token:
            hdrs["Authorization"] = "Bearer " + self.token
        if body is not None:
            data = json.dumps(body).encode()
            hdrs["Content-Type"] = "application/json"
        if headers:
            hdrs.update(headers)
        if "/v1/playback/" in path or path.endswith("/playback-offers"):
            hdrs.setdefault("Portico-Playback", PLAYBACK_HEADER)
        req = urllib.request.Request(url, data=data, headers=hdrs, method=method)
        try:
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                status, raw, out_headers = resp.status, resp.read(), dict(resp.headers)
        except urllib.error.HTTPError as err:
            status, raw, out_headers = err.code, err.read(), dict(err.headers)
        if status in expect:
            if not raw:
                return None
            content_type = out_headers.get("Content-Type", "")
            if content_type.startswith("text/") or content_type.startswith("image/"):
                return raw.decode("utf-8", "replace") if content_type.startswith("text/") else raw
            try:
                return unwrap(json.loads(raw))
            except ValueError:
                raise ApiError(status, "non_json", path)
        code = ""
        retryable = False
        try:
            payload = json.loads(raw)
            err = payload.get("error") if isinstance(payload, dict) else None
            if isinstance(err, dict):
                code = err.get("code", "")
                retryable = bool(err.get("retryable"))
            else:
                code = str(err or "")
        except ValueError:
            pass
        if status in allow:
            raise ApiError(status, code, path, retryable)
        raise ApiError(status, code, path, retryable)

    def read(self, path, attempts=8, expect=(200,)):
        """A read that tolerates a catalogue/scan fence like the retained fixture."""
        last = None
        for attempt in range(attempts):
            try:
                return self.request("GET", path, expect=expect)
            except ApiError as err:
                last = err
                if err.status not in (409, 412, 423, 503, 504) or attempt == attempts - 1:
                    raise
                time.sleep(min(4.0, 0.5 * (attempt + 1)))
        raise last

    def request_raw(self, method, path, headers=None, expect=(200, 206)):
        url = self.base + path
        hdrs = {"Accept": "*/*", "X-Portico-Installation-Id": self.installation,
                "Portico-Playback": PLAYBACK_HEADER}
        if self.token:
            hdrs["Authorization"] = "Bearer " + self.token
        if headers:
            hdrs.update(headers)
        req = urllib.request.Request(url, headers=hdrs, method=method)
        try:
            with urllib.request.urlopen(req, timeout=120) as resp:
                status, raw, out_headers = resp.status, resp.read(), dict(resp.headers)
        except urllib.error.HTTPError as err:
            status, raw, out_headers = err.code, err.read(), dict(err.headers)
        if status not in expect:
            raise ApiError(status, "", path)
        return status, raw, out_headers

    # -- owner session ------------------------------------------------------
    def save_state(self, path):
        _private_write(path, json.dumps(self.state, indent=2) + "\n")
        self._state_path = os.path.abspath(path)

    @classmethod
    def load(cls, base, state_path):
        client = cls(base, state=json.loads(_private_read(state_path)))
        client._state_path = os.path.abspath(state_path)
        return client

    def refresh(self):
        refresh = self.state.get("refreshToken")
        installation = self.state.get("installationId") or self.installation
        if not refresh or not installation:
            raise ApiError(401, "no_refresh_material", "/v1/auth/refresh")
        out = self.request(
            "POST",
            "/v1/auth/refresh",
            {"refreshToken": refresh, "installationId": installation,
             "requestId": str(uuid.uuid4())},
            expect=(200,),
        )
        session = out.get("session", out)
        self.state.update(session)
        self.installation = session.get("installationId") or self.installation
        self.token = session["accessToken"]
        return session


def bootstrap(base, username, password, name):
    """Ordinary first-run setup, then finish onboarding.

    `/v1/setup/app` is the real app route: JSON body, no Origin and no
    Sec-Fetch-Mode. It avoids inventing a trusted browser authority for a
    private container name.
    """
    api = Api(base)
    setup = api.request("POST", "/v1/setup/app", {}, expect=(200,), origin=False)
    token = setup.get("setupToken")
    if not token:
        raise SystemExit("setup route returned no token")
    created = api.request(
        "POST",
        "/v1/setup",
        {"setupToken": token, "requestId": str(uuid.uuid4()), "name": name,
         "authMode": "local", "interactive": True, "username": username,
         "password": password},
        expect=(201,),
        origin=False,
    )
    session = created.get("session", created)
    api.state = dict(session)
    api.state.setdefault("serverId", setup.get("serverId"))
    api.token = session["accessToken"]
    api.installation = session.get("installationId") or api.installation
    state = api.request("GET", "/v1/setup/state", expect=(200,))
    if not state.get("ready"):
        api.request("POST", "/v1/setup/finish", {"revision": state["revision"]}, expect=(200,))
    return api


# -- settings -------------------------------------------------------------
def patch_settings(api, changes, idempotency_key=None):
    """Change owner settings through the complete-document PATCH route."""
    doc = api.request("GET", "/v1/admin/console/settings")
    doc = doc.get("data", doc)
    values = dict(doc["requested"])
    unknown = set(changes) - set(values)
    if unknown:
        raise SystemExit("unknown settings fields: %s" % ", ".join(sorted(unknown)))
    values.update(changes)
    body = {
        "expectedRevision": doc["revision"],
        "idempotencyKey": idempotency_key or uuid.uuid4().hex,
        "values": values,
    }
    out = api.request("PATCH", "/v1/admin/console/settings", body, expect=(200,))
    return out.get("data", out)


# -- libraries ------------------------------------------------------------
def create_library(api, name, kind, path, scan_after=True):
    out = api.request(
        "POST",
        "/v1/libraries",
        {"name": name, "kind": kind, "path": path, "metadataAgent": "local", "scanAfter": scan_after},
        expect=(201, 200),
    )
    return out.get("library", out)


def wait_inventory(api, library_id, deadline=900.0):
    end = time.time() + deadline
    last = None
    while time.time() < end:
        doc = api.read("/v1/libraries/%s/inventory-status" % library_id)
        sources = ((doc or {}).get("inventory") or {}).get("sources") or doc.get("sources") or []
        states = [s.get("status") for s in sources]
        last = states
        if sources and all(s in ("complete", "completed", "complete_with_warnings", "idle") for s in states):
            return doc
        if any(s in ("failed", "error", "cancelled") for s in states):
            raise SystemExit("inventory did not complete: %s" % ", ".join(str(s) for s in states))
        time.sleep(2.0)
    raise SystemExit("inventory deadline reached; last source states %r" % last)


def browse_items(api, library_id, limit=200):
    caps = api.request("GET", "/v1/libraries/%s/browse-capabilities" % library_id)
    pivot = (caps.get("resolvedPivot") or {}).get("id")
    if not pivot:
        pivot = next((p.get("id") for p in caps.get("pivots", []) if p.get("browsable")), None)
    if not pivot:
        raise SystemExit("library offers no browsable pivot")
    doc = api.request("POST", "/v1/libraries/%s/browse" % library_id,
                      {"pivot": pivot, "limit": limit}, expect=(200,))
    entries = doc if isinstance(doc, list) else doc.get("entries", [])
    return [e for e in entries if e.get("id")]


# -- playback -------------------------------------------------------------
class SessionBridge:
    """Keep grant/session JSON on private pipes; client-core owns playback commands."""
    def __init__(self, client):
        node = os.environ.get('PORTICO_FIXTURE_NODE')
        capabilities = os.environ.get('PORTICO_FIXTURE_CAPABILITIES')
        script = os.environ.get('PORTICO_FIXTURE_SESSION_ADAPTER') or str(pathlib.Path(__file__).with_name('session-adapter.ts'))
        if not node or not capabilities or not client._state_path:
            raise SystemExit('sessions require qualified Node24.21.0, private captured capabilities and saved owner state')
        _private_read(capabilities)
        self.process = subprocess.Popen([node, '--experimental-transform-types', '--no-warnings', script,
                                         client.base, client._state_path, capabilities],
                                        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                                        text=True)
        atexit.register(self.close)

    def call(self, command):
        self.process.stdin.write(json.dumps(command) + '\n')
        self.process.stdin.flush()
        if not select.select([self.process.stdout], [], [], 150)[0]:
            raise ApiError(504, "canonical_session_adapter_timeout")
        line = self.process.stdout.readline(4*1024*1024)
        if not line:
            raise ApiError(500, 'canonical_session_adapter_exited')
        response = json.loads(line)
        if not response.get('ok'):
            raise ApiError(response.get('status', 500), response.get('code', 'canonical_session_adapter_failed'))
        return response.get('result')

    def close(self):
        if self.process.poll() is None:
            self.process.stdin.close()
            try:
                self.process.wait(timeout=30)
            except subprocess.TimeoutExpired:
                self.process.terminate()
                try:
                    self.process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    self.process.kill()
                    self.process.wait()
        if self.process.returncode:
            raise ApiError(500, 'canonical_adapter_cleanup_failed')


def session_command(client, command):
    if client._session_bridge is None:
        client._session_bridge = SessionBridge(client)
    return client._session_bridge.call(command)


def start_session(api, item_id, quality=None, subtitles=None, position=0):
    options = {'state': 'playing'}
    if position:
        options['startPositionMs'] = position
    else:
        options['startFrom'] = 'beginning'
    if quality:
        options['quality'] = quality
    if subtitles is not None:
        options['subtitles'] = subtitles
    return session_command(api, {'op': 'start', 'itemId': item_id, 'options': options})


def get_session(api, session_id):
    return session_command(api, {'op': 'get', 'id': session_id})


def change_session(api, session, change):
    return session_command(api, {'op': 'change', 'id': session['id'], 'change': change})


def stop_session(api, session, position_ms=0):
    return session_command(api, {'op': 'stop', 'id': session['id'], 'positionMs': max(0, int(position_ms))})


def playback_offers(api, item_id):
    out = api.request("GET", "/v1/items/%s/playback-offers" % item_id)
    return out.get("offers", out) if isinstance(out, dict) else out


def diagnostics(api):
    doc = api.request("GET", "/v1/admin/playback/diagnostics")
    return doc.get("diagnostics", doc)


def main(argv):
    import argparse

    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    boot = sub.add_parser("bootstrap")
    boot.add_argument("--base", required=True)
    boot.add_argument("--token-file", required=True)
    boot.add_argument("--password-file", required=True)
    boot.add_argument("--username", default="fixture-owner")
    boot.add_argument("--name", default="Fixture owner")
    args = parser.parse_args(argv)
    if args.command == "bootstrap":
        password = _private_read(args.password_file).strip()
        api = bootstrap(args.base, args.username, password, args.name)
        api.save_state(args.token_file)
        print("owner bootstrapped; state written to a private 0600 file")
        return 0
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
