# Android handheld E2E

Dispatch Android JVM tests with `handheld_e2e=true` to run the same disposable,
local-only server and Maestro walk on hosted KVM phone and tablet emulators. The
server, media, and client share the runner; Android reaches it at `10.0.2.2`.
The development-only owner hook avoids entering credentials. Do not upload server
state or the generated credentials. Screenshots, video, JUnit and Maestro logs
are retained for 14 days. Each device has its own server and artifact.

The initial walk captures welcome, local sign-in, Home, search landing, search
results, a title page and playing video. These captures are evidence to review,
not automatic visual acceptance. Phone references are auth#7 phone 4,
phone-app#1 phone 1, phone-app#4 phones 7 and 8, phone-detail#1 phone 1, and
phone-player#1; confirm exact sign-in and player state before comparing. Tablet
references include tablet#1b (landscape Home) and tablet#10 (landscape search);
the emulator's actual dimensions, orientation and state must match before giving
a visual verdict. Record separate works and matches verdicts and the element
checklist with same-sized reference and capture. A missing frame or product
conflict goes to the lead.

The restricted member/PIN is provisioned by the fixture but this walk does not
exercise it. Browse, subtitles, profile/PIN, settings, setup and the other gap
audit candidates remain to be added and verified. Playback currently checks for
a nonzero position, not two distinct positions; it does not yet prove continued
progress. A failed walk is evidence of failure, never an automatic product bug.
Use observable, bounded waits. Keep the first failure; permit at most one fresh
run for diagnosis and report both outcomes. Quarantine only a reproduced
intermittent failure with a linked finding, owner and expiry; it must still run
and report independently and must not mask other tests or become required.
