# Secure field metadata adapter (source-only)

The M0-005 command was `adb -s emulator-5554 shell uiautomator dump /sdcard/smoke-secure.xml`.
It returned 137. The private script used capture_output=True/check=True, then raised before
persisting stdout/stderr. Exact stderr is therefore unavailable; do not infer an idle timeout,
path error, resource kill or permission cause. Originals remain private and unchanged.

Installed agent-device 0.21.25 evidence:
- dist/src/sdk-contracts.d.ts RawSnapshotNode declares focused/password/editable booleans.
  Absent native facts mean unavailable, not false. Selection offsets are explicitly not proof
  of value equality.
- dist/src/mechanics.js maps native Android password/focused facts through passiveFacts into
  JSON nodes. The CLI snapshot --json envelope is success/data; retained nonsecret snapshots
  contain data.nodes, appBundleId, refsGeneration and these native boolean keys.
- README: refs belong only to the latest output; focus/keyboard actions require fresh snapshots.

secure-field.mjs accepts that exact CLI envelope and the caller's current snapshot generation.
It refuses failed/truncated/stale/foreign/ambiguous captures, missing native flags, blocked or
unhittable fields and unusable bounds. It never reads labels, values, placeholders, masks or
selection offsets. It returns only ref/generation/bounds; it neither launches a device nor
loads/enters a secret. The JSON capture is private0600 and must occur after focus/keyboard.
Never reuse the returned ref after another action; obtain and validate a fresh capture.

This is a metadata selector, NOT proof of exact password replacement or login success. No
credential driver has been wired to it. Exact replacement must use the installed supported
fill operation on a validated fresh ref, with raw output private; no mask/length proof is added.
If any native fact is absent, STOP. Do not fall back to a raw XML dump, label-only targeting,
blind input, guessed flags or another snapshot engine. Device validation remains pending a
separately authorized ZERO-credential metadata-only window; no such run was performed here.

If Release needs runtime confirmation, proposed scope is one stopped/wiped named Phone boot,
normal local sign-in page with ZERO credential-file loading/typing/submission, focus empty
fields, private snapshot --json captures before/after keyboard, retain structural booleans only,
then normal close/shutdown/qemu-gone/named wipe. APK and window require explicit Release pin.
