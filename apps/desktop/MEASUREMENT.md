# Actual WebView measurement

This DEV-only transport invokes the canonical `tools/fidelity/measure-screen.mjs`
inside the actual Tauri WebView. It reuses the existing `/dev/kit` public specimen
route through the original app entry and native platform installation. It does
not render a replacement screen, change its DOM, or implement a comparator.

Run the normal Desktop Vite DEV server and a debug native app with an isolated
owned data directory. Configure these process-local environment variables:

- `PORTICO_SPIKE_DATA`: existing isolated native data directory.
- `PORTICO_DESKTOP_MEASURE_REQUEST`: absolute private JSON file containing the
  existing collector arguments `selector`, `expectedViewport: {width,height}`,
  `anchors` and `repeats`.
- `PORTICO_DESKTOP_MEASURE_ROUTE`: existing `/dev/kit?capture=true&family=...&case=...&width=...&height=...`
  route, with exactly those five query keys and matching requested dimensions.
- `PORTICO_DESKTOP_MEASURE_OUTPUT`: absolute, nonexistent private output file
  beside the request. The isolated data directory must be below that same parent.

The native window requests the frame's real client dimensions. The selected root
must actually fill that window. Existing gallery cases are checked before use;
their fallback case cannot stand in for a missing requested specimen. The collector
waits for fonts/images/two animation frames unchanged. Focus is observed, never
modified. Two identical canonical measurements and loaded font faces are required
before the native transport writes once. Private paths never cross IPC.

The canonical collector declares `measurement: {renderer:'dom',coordinateUnit:'css-px'}`
and preserves actual DPR, including Retina DPR2/3. Reference dimensions stay at
their authored logical viewport/DPR1. DOM boxes, Range rectangles and font facts
stay in their observed CSS units; no multiplication, division, zoom or OS/display
change is applied. This supersedes the initial Desktop DPR1-only transport guard.
Failed/unloaded fonts, mount failures, unstable facts and non-full-window roots
remain unmeasured. Wrong logical viewports fail the canonical comparison. A gallery
specimen is component evidence, not full application acceptance.

Use the canonical offline CLI with private reference/adapter/output files outside
public source. Private design, references, measured output and comparisons must
stay on our machines, never public Actions or a shipped bundle. The production
entry omits the collector, and release native builds ignore measurement variables.
Shut down the actual app normally so its original app lifetime retires first;
then stop the owned DEV server and remove only the job's disposable scratch.
