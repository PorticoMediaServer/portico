#!/usr/bin/env python3
"""Keep the account-controller false-positive exception narrowly scoped."""

import json
import os
from pathlib import Path
import secrets
import subprocess
import sys
import tempfile


def main():
    binary = Path(sys.argv[1]).resolve()
    config = Path(".gitleaks.toml").resolve()
    ignore = Path(".gitleaksignore").resolve()
    expression = "this.api," + "proof=this.moveProof;"
    account = "web/src/platform/account-entry.ts"
    helper = "scripts/check-gitleaks-config.py"
    services = "web/test/services-links-browser.mjs"
    with tempfile.TemporaryDirectory(dir=os.environ.get("TMPDIR")) as scratch:
        root = Path(scratch)
        (root / account).parent.mkdir(parents=True)
        # Runtime-generated specimen keys never leave this disposable fixture.
        for name in (account, "other.ts", helper):
            (root / name).parent.mkdir(parents=True, exist_ok=True)
            first_line = 'expression = "' + expression + '"' if name == helper else expression
            (root / name).write_text(
                first_line + '\nconst apiKey="' + secrets.token_urlsafe(32) + '";\n'
            )
        (root / services).parent.mkdir(parents=True, exist_ok=True)
        (root / services).write_text(
            '\n' * 69 + 'const password="' + secrets.token_urlsafe(32) + '";\n'
        )
        report = root / "findings.json"
        command = [
            str(binary), "dir", ".", "--config", str(config), "--redact",
            "--no-banner", "--exit-code=0", "--report-format=json",
            "--report-path=" + str(report),
        ]
        if ignore.exists():
            command += ["--gitleaks-ignore-path", str(ignore)]
        result = subprocess.run(
            command,
            cwd=root, capture_output=True, text=True,
        )
        if result.returncode:
            raise SystemExit("Gitleaks configuration fixture could not run; values withheld.")
        actual = {
            (finding["RuleID"], finding["File"], finding["StartLine"])
            for finding in json.loads(report.read_text())
        }
        expected = {
            ("generic-api-key", account, 2),
            ("generic-api-key", "other.ts", 1),
            ("generic-api-key", "other.ts", 2),
            ("generic-api-key", helper, 2),
            ("generic-api-key", services, 70),
        }
        if actual != expected:
            print(json.dumps({"expectedLocations": sorted(expected), "actualLocations": sorted(actual)}))
            raise SystemExit("Gitleaks exception exceeded its exact expression/path or lost detection.")
    print("Gitleaks configuration: exact account expression ignored; same-file keys and other paths detected.")


if __name__ == "__main__":
    main()
