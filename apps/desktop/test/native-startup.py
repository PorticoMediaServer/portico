"""Basic genuine Linux app lifecycle, not screen/media/output acceptance."""
import json
import os
from pathlib import Path
import sys

report = Path(os.environ["PORTICO_SPIKE_REPORT"])
observed = json.loads(report.read_text())
trace = report.with_suffix(".native.log").read_text()
assert observed["platform"] == "linux"
assert observed["ui"]["rootMounted"] is True
assert observed["ui"]["width"] == 1440 and observed["ui"]["height"] == 900
assert observed["ui"]["frames"] >= 60 and observed["ui"]["elapsedMs"] >= 4900
assert "window-state-confirmed 0" in trace
assert trace.index("close-web-requested 0") < trace.index("close-started 0") < trace.index("close-finished 0")
if sys.argv[1] == "unavailable":
    assert observed["player"] is None and observed["audio"] is None
    assert observed["native"]["renderCalls"] == 0
else:
    assert observed["player"]["state"] == "idle"
    assert observed["player"]["nativeSurfaceActive"] is False
print("Linux actual start/run/close PASS: " + sys.argv[1] + "; no screen/hardware qualification")
