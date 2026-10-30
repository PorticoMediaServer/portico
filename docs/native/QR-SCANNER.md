# Native QR camera producer

`src-next/platform/qr-scanner.ts` exposes one explicit `scanQRCode` call with an
original opening guard, required AbortSignal and localized title/Cancel label.
It returns a raw string (1–8192 UTF-8 bytes) or `undefined` on native Cancel.
It does not trim, normalize, interpret, open, store or log the result. Consumers
must retain their canonical invitation/member-code parsers, issuer proofs and
admission lifetimes. The 43-character member proof is unchanged.

The native methods are `PorticoQRScanner.scan(requestId, title, cancel)` and
`cancel(requestId)`. IDs are 16 random bytes represented as 32 lowercase hex
characters. Only the original ID can cancel or settle its scan. The shared
wrapper limits an opening to 90 seconds and consumes late native answers after
cancellation. No result is delivered after its original guard retires.

Android uses its own nonexported camera Activity, explicit CAMERA permission,
optional hardware declaration and bundled `com.google.zxing:core:3.5.3` QR-only
decoding. One preview frame is decoded at a time on a single worker. Admission
is recorded before Activity launch, so cancellation before Activity creation
also prevents camera/permission opening. Pause releases the camera; background
Stop/Back/Cancel closes the operation. The React host's Pause alone is expected
while the app's scanner Activity is visible. Televisions do not export this
module. No external scanner application, Play Services model or download is used.

The iOS-only module uses explicit AVFoundation video permission and an in-app
QR metadata preview. A pending permission callback retains its original ticket;
Cancel/background/bridge invalidation retires it before it can present. Camera
session work is serialized, stopped at retirement and never adds an audio input.
The file is in the iOS target only; no tvOS camera or permission is added.

Both module instances become irrevocably closed synchronously at bridge
invalidation. Queued starts and permission/presentation/camera/result admission
recheck that terminal state. Android also revokes its captured ID admission
synchronously before main-thread camera cleanup; iOS capture work reads the
original pending ticket plus the atomic module flag. Deferred cleanup retains
only the captured original pending ticket and never selects a successor.

Neither implementation saves camera images or makes a network request. Native
failures are a closed code set: qr_busy, qr_cancelled, qr_permission_denied,
qr_camera_unavailable, qr_too_large, qr_invalid_request. Their error messages
contain no raw result. Native cancellation may resolve null; the wrapper maps
its own aborted opening to qr_cancelled.

This source adds a real Android Maven dependency. Release's next authorized
checkpoint must resolve its actual runtime classpath and licence inventory;
this source change does not regenerate an inventory or alter a frozen cohort.
No compile, test, device or camera-runtime acceptance is asserted here.
