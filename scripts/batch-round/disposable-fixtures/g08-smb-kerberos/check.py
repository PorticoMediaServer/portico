#!/usr/bin/env python3
"""Drive the current Portico saved-source candidate path against the fixture DC.

This runs inside the private fixture network. It records the exact API outcome;
a policy refusal is evidence of the boundary, not a Kerberos pass. Kerberos
transport itself is exercised by run.sh through the pinned rclone in the client
container and through the production child environment.
"""

import argparse
import json
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "lib"))
import api  # noqa: E402


def config_for(spn):
    lines = [
        "[smbfixture]",
        "type = smb",
        "host = dc1.portico.test",
        "port = 445",
        "user = fixture",
        "pass = " + open(os.environ["G08_OBSCURED_PASS"], "r", encoding="utf-8").read().strip(),
        "domain = PORTICO",
        "use_kerberos = true",
        "idle_timeout = 5s",
    ]
    if spn:
        lines.append("spn = " + spn)
    return "\n".join(lines) + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True)
    parser.add_argument("--state", required=True)
    parser.add_argument("--password-file", required=True)
    parser.add_argument("--username", default="fixture-owner")
    parser.add_argument("--out", required=True)
    parser.add_argument("--execute", action="store_true")
    args = parser.parse_args()
    if not args.execute:
        print("PLAN: would bootstrap and exercise the candidate source path")
        return 0
    if os.path.exists(args.state):
        client = api.Api.load(args.base, args.state)
        try:
            client.request("GET", "/v1/server")
        except api.ApiError as err:
            if err.status != 401:
                raise
            try:
                client.refresh()
                client.save_state(args.state)
            except api.ApiError:
                os.unlink(args.state)
                client = None
    else:
        client = None
    if client is None:
        password = open(args.password_file, "r", encoding="utf-8").read().strip()
        client = api.bootstrap(args.base, args.username, password, "G08 fixture owner")
        client.save_state(args.state)

    results = []
    for label, spn in (("implicit", None), ("explicit", "cifs/smb-alias.portico.test")):
        entry = {"case": label, "spn": spn}
        try:
            out = client.request("POST", "/v1/storage/sources/rclone/test",
                                 {"remote": "smbfixture:media", "config": config_for(spn)},
                                 expect=(200,))
            entry["outcome"] = "accepted"
            entry["version"] = (out or {}).get("version")
        except api.ApiError as err:
            entry["outcome"] = "refused"
            entry["status"] = err.status
            entry["code"] = err.code
            if err.status == 404:
                entry["outcome"] = "route_missing"
        results.append(entry)

    receipt = {"phase": "g08-candidate", "base": client.base, "results": results,
               "route": "POST /v1/storage/sources/rclone/test "
                        "(server/internal/httpapi/remote_rclone_connection.go:43, registered foundation.go:705)",
               "note": "A refusal here is the private-endpoint approval boundary; "
                       "it is not a Kerberos result."}
    fd = os.open(args.out, os.O_CREAT | os.O_TRUNC | os.O_WRONLY, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as handle:
        json.dump(receipt, handle, indent=2)
        handle.write("\n")
    print(json.dumps([{k: v for k, v in r.items() if k != "spn"} for r in results]))
    return 0


if __name__ == "__main__":
    sys.exit(main())
