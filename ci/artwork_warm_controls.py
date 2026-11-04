"""Branch-only fixture controls on the exact recorded shard; never merge."""
import json
import os
import subprocess
import sys
from pathlib import Path

root = Path(__file__).resolve().parents[1]
out = root / "artwork-warm-results"
out.mkdir()
plan = Path(os.environ["RUNNER_TEMP"]) / "original-selection/ci-plan.json"
selection = json.loads(plan.read_text())
target = "TestArtworkSlowPublicationDoesNotBlockReadsOrPreviews"
pkg = "github.com/PorticoMediaServer/portico/server/internal/metadata"
path = "server/internal/metadata/artwork_test.go"
refs = {"A": "f615a51b6995ee4290b0858bb8d3fc341ba9f6db", "B": "eb115c57cf084d29f7201c32f0b7fb8bad89b461"}
(out / "source.json").write_text(json.dumps({"refs": refs, "source_run": 41870228994, "shard": 3, "package_jobs": 4}, indent=2))
records = []
for label, variant, cold, mutation in [("baseline-cold", "A", True, False), ("fixed-cold", "B", True, False), ("fixed-writer-mutation", "B", False, True)]:
    source = subprocess.check_output(["git", "show", refs[variant] + ":" + path], cwd=root, text=True)
    anchor = "\t\tgo func(preview bool) {\n"
    assert source.count(anchor) == 1
    source = source.replace(anchor, anchor + '\t\t\tctx := dbwork.WithOwner(ctx, fmt.Sprintf("ARTWORK-%v-%02d", preview, i))\n')
    (root / path).write_text(source)
    env = dict(os.environ, PORTICO_ARTWORK_REJECT_COLD=str(int(cold)), PORTICO_ARTWORK_GATE_MUTATION=str(int(mutation)), PORTICO_ARTWORK_DIAGNOSTIC="1", PORTICO_CI_GO_PACKAGE_JOBS="4")
    if mutation:
        command = ["go", "test", "-json", "-count=1", "-p=1", "-tags", selection["tags"], "-run", "^" + target + "$", "./internal/metadata"]
        result = subprocess.run(command, cwd=root / "server", env=env, capture_output=True, text=True)
        directory = out / label
        directory.mkdir()
        (directory / "metadata.jsonl").write_text(result.stdout)
    else:
        result = subprocess.run([sys.executable, "ci/run.py", "go", "--index", "3", "--plan", str(plan)], cwd=root, env=env, capture_output=True, text=True)
        directory = out / label
        (root / "ci-results").rename(directory)
    (out / (label + ".stdout")).write_text(result.stdout)
    (out / (label + ".stderr")).write_text(result.stderr)
    failed = set()
    terminals = {}
    outputs = []
    for rawfile in directory.rglob("*.jsonl"):
        for line in rawfile.open():
            event = json.loads(line)
            name = event.get("Test")
            if name and event.get("Action") in ("pass", "fail", "skip"):
                terminals[(event["Package"], name)] = event["Action"]
                if event["Action"] == "fail":
                    failed.add(name)
            if name == target and event.get("Output"):
                outputs.append(event["Output"])
    text = "".join(outputs)
    expected = "fail" if cold and variant == "A" or mutation else "pass"
    assert terminals.get((pkg, target)) == expected, (label, terminals.get((pkg, target)), expected)
    assert failed == ({target} if expected == "fail" else set()), (label, failed)
    if not mutation:
        wanted = {(entry["package"], entry["name"]) for entry in selection["shards"][3]}
        assert wanted <= terminals.keys(), (label, "missing parents", sorted(wanted - terminals.keys()))
        assert all(terminals[key] in ("pass", "skip") for key in wanted if key != (pkg, target))
    if cold and variant == "A":
        assert "publication phase attempted a cold foreground reader" in text
    if variant == "B" and cold:
        assert result.returncode == 0, (label, result.returncode)
    if mutation:
        assert "context deadline exceeded" in text or "reads waited for publication" in text
    records.append({"label": label, "variant": variant, "returncode": result.returncode, "expected_artwork": expected, "observed_artwork": terminals[(pkg, target)], "failing_tests": sorted(failed)})
    (out / "controls.json").write_text(json.dumps(records, indent=2))
    print(label, records[-1], flush=True)
