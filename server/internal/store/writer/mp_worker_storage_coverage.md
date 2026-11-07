# MP worker storage integration

Final F packet: private commit9f3384b406d3b35571e0faa9666351625eca8904,
delta c295971123cce995fdc641d5057ff2744548279f8be4bd8b2d56fda8e5d67a03.
This source is a local draft against that held packet, not adoption or runtime wiring.

The seven actual worker streams retain independent native incarnations, sequence,
ACK, quota and cleanup after issuer revocation. UO owns user birth, lifetime and
public idempotency; historical root/member/profile copies confer no authority.
Start/job and queue/next migrations are integrated from d50a7331 and3d24ba36;
session/physical adapters were already mapped to F. Version745/749 now use their
own prepared_media_versions root and capped original W payload table. Version
cleanup retains its distinct physical deletion request/execution, never the
already-exited encoding job's execution. Typed original-result validation and
commitments are draft controls; W alone supplies encoded bytes and codec digest.

Every worker result copy requires exact native point byte preflight and admitted
bytes. Stores cross-check native generated debt, repeat owner counters/revision
in the same-turn CAS and leave ACK/COMMIT/Applied to W. Borrowed worker purposes
are rejected. No MP result-child tables or wide version-result SQL are retained.
The domain SQL shape check includes all seven worker catalogs.

Tiny local Go1.26.10 version commitment/purpose, global SQL arity and shared finish
checks passed. TestMPVersionNativeFSQL is prepared but unrun, alongside the other
native/race/Capture suites. It must run on a standard runner with adopted F or
PORTICO_MP_F_PACKET set to the exact delta for the disposable canonical fixture.

Remaining: W opaque current disclosure, canonical classification, sole codec and
immutable envelope construction; UO user command composition and events; real
supervisor/IO bridges; F adoption and complete runner qualification. Read actual
byte publication follow-up1340 remains tracked separately; no new GV seam is
needed. These adapters grant no public command acceptance or qualification.
