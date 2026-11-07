# Queue, next and reading bounded domain coverage

Storage migration, 2026-11-07: exact F MP-001 is held, not adopted.

Queue worker commands 751–760 (including repeat754, save757, cleanup758,
snapshot759) use only mp_queue_command_receipts. Next762–764 use only
mp_next_command_receipts. The concrete adapters store the original W payload
and digest; they contain no payload serializer or result-child persistence.
The existing pure typed value commitments are validation helpers only.

Store checks immutable command identity, version/count/payload bounds,
incarnation, revision, last=ACK, quota and due time. Native generated debt must
match owner bytes+96+payload bytes. Same-turn CAS repeats incarnation/revision/
last/ACK/rows/bytes, increments owner revision and records reclaim due.
Replay performs a point octet preflight before copying and requires actual
admitted native bytes. Original operation, request and purpose are checked;
W retains sole digest/decode, classification, Applied, ACK and commit authority.
Fresh opaque disclosure and canonical classification must precede these calls.

User births750/761 accept a historical root/member/profile tuple as data,
generate a distinct random worker incarnation, initialize native sequence/ACK
at zero in the same turn, and return original typed effects for UO/W. They
never install the user sequence or create a birth receipt owner/table.
The domain result includes workerIncarnation for the parent's UO/W handoff.

Bounded queue chunk/layout/edit/shuffle/tombstone controls remain. Next selection
and predecessor generation/custody/source fences remain. Independent stop
obligations retain exact original request, generation and supervisor fences;
receipt reclaim cannot release custody and actual physical exit is required.
Reading770/771 personal/history/revision effects remain UO-owned.

Pure controls retain bounds, original-value digest mutation negatives, missing
header rejection, custody drift, request/exit fences and statement arity.
Worker controls add quota/due/debt/incarnation negatives, user/read purpose
rejection, revision/counter CAS shape and historical tuple/fresh-incarnation
checks. Native fixtures prepare F payload SQL and retain the stop-debt reclaim
negative without obsolete children. They explicitly skip absent F unless the
exact packet is supplied through PORTICO_MP_F_PACKET on a standard runner.

Verification here: Go1.26.10, GOMAXPROCS=2, -p1 focused pure tests and package
compilation only. Native/race/Capture remain prepared and unrun. Parent still
owns schema adoption, sole W codec/admission composition, registration,
disclosure and canonical classification, and standard-runner qualification.
