-- US-001 exact DRAFT delta over canonical schema da7841e26dd347f65554466139dd8eae0f181c9250715dc26d462e0e33b3f1c4.
-- P2-US owns this proposal; INT validates/reviews/adopts/embeds it. NOT adopted,
-- NOT a product migration, NOT a runtime enable. Fresh pre-release databases only.
-- No triggers, source-row fan-out, user cap, or FK cascade for receipt custody.

-- Retire a device logically. DELETE would invoke the retained family FK's
-- ON DELETE SET NULL across F families. Re-registration is an explicit fresh
-- proof + revision/fence generation transition, never resurrection by old token.
ALTER TABLE identity_devices ADD COLUMN retired_ms INTEGER NOT NULL DEFAULT 0 CHECK(retired_ms>=0);

-- Explicit immutable birth provenance: 0 ordinary interactive family,
-- 1 API/key/token/app-password/integration credential family. No default:
-- every fresh producer must choose its concrete type. A credential family
-- never becomes ordinary when its credential marker is physically drained.
-- Fresh EMPTY pre-release schema only; this changes the phase-1a parent layout.
ALTER TABLE authorization_session_families ADD COLUMN security_kind INTEGER NOT NULL CHECK(security_kind IN(0,1));


-- Retained original command scope and exact sequence/ACK/reclaim custody.
-- Source account/profile/device deletion cannot cascade away receipt authority.
-- These are exact counters, not a new 128-receipt/32-MiB product quota; W owns
-- admission from its reviewed actual-byte/count budget and bounded reclaim.
-- authenticated/capability/system are CLOSED ingress classes, never caller roles.
CREATE TABLE security_command_owners(
 id TEXT NOT NULL PRIMARY KEY CHECK(length(CAST(id AS BLOB)) BETWEEN 1 AND 128),
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 ingress TEXT NOT NULL CHECK(ingress IN('authenticated','capability','system')),
 server_id TEXT NOT NULL CHECK(length(CAST(server_id AS BLOB)) BETWEEN 1 AND 128),
 issuer_account_id TEXT,
 issuer_member_incarnation TEXT,
 issuer_profile_id TEXT,
 issuer_profile_lifetime TEXT,
 issuer_installation_incarnation TEXT,
 original_family_id TEXT,
 capability_kind TEXT,
 capability_id TEXT,
 capability_digest BLOB,
 last_sequence INTEGER NOT NULL DEFAULT 0 CHECK(last_sequence>=0),
 acknowledged_sequence INTEGER NOT NULL DEFAULT 0 CHECK(acknowledged_sequence>=0 AND acknowledged_sequence<=last_sequence),
 reclaimed_sequence INTEGER NOT NULL DEFAULT 0 CHECK(reclaimed_sequence>=0 AND reclaimed_sequence<=acknowledged_sequence),
 retained_count INTEGER NOT NULL DEFAULT 0 CHECK(retained_count>=0),
 retained_variable_bytes INTEGER NOT NULL DEFAULT 0 CHECK(retained_variable_bytes>=0),
 created_ms INTEGER NOT NULL CHECK(created_ms>=0),
 retired_ms INTEGER NOT NULL DEFAULT 0 CHECK(retired_ms>=0),
 CHECK(last_sequence-acknowledged_sequence<=1),
 CHECK((retained_count=0)=(retained_variable_bytes=0)),
 CHECK((ingress='authenticated' AND issuer_account_id IS NOT NULL AND length(CAST(issuer_account_id AS BLOB)) BETWEEN 1 AND 128
  AND issuer_member_incarnation IS NOT NULL AND length(issuer_member_incarnation)=32 AND issuer_profile_id IS NOT NULL AND length(CAST(issuer_profile_id AS BLOB)) BETWEEN 1 AND 128
  AND issuer_profile_lifetime IS NOT NULL AND length(issuer_profile_lifetime)=32
  AND issuer_installation_incarnation IS NOT NULL AND length(issuer_installation_incarnation)=32
  AND original_family_id IS NOT NULL AND length(CAST(original_family_id AS BLOB)) BETWEEN 1 AND 128
  AND capability_kind IS NULL AND capability_id IS NULL AND capability_digest IS NULL)
 OR (ingress='capability' AND issuer_account_id IS NULL AND issuer_member_incarnation IS NULL AND issuer_profile_id IS NULL
  AND issuer_profile_lifetime IS NULL AND issuer_installation_incarnation IS NULL AND original_family_id IS NULL
  AND capability_kind IS NOT NULL AND capability_kind IN('invitation','setup','quick_connect','factor','reset','hosted_challenge')
  AND capability_id IS NOT NULL AND length(CAST(capability_id AS BLOB)) BETWEEN 1 AND 128 AND capability_digest IS NOT NULL AND length(capability_digest)=32)
 OR (ingress='system' AND issuer_account_id IS NULL AND issuer_member_incarnation IS NULL AND issuer_profile_id IS NULL
  AND issuer_profile_lifetime IS NULL AND issuer_installation_incarnation IS NULL AND original_family_id IS NULL
  AND capability_kind IS NULL AND capability_id IS NULL AND capability_digest IS NULL)),
 UNIQUE(id,command_incarnation)
) STRICT;
CREATE INDEX security_command_owner_retirement ON security_command_owners(retired_ms,id);

-- Immutable accepted result, not a current permission/account/session source.
-- Only the twelve concrete purposes below are admitted, not the reserved US
-- namespace. Policy/PIN native post-scalars and invitation DTO facts are typed;
-- the sole protected body is creation's exact invitation Code (not a generic
-- result/permission document). Prepare its AES-GCM image outside the writer,
-- then verify the actual code_hash/native manifest before accepting it.
-- result_digest binds original command plus complete scalars/cohorts/Code image.
-- Other planned US purposes are NOT native receipt schemas until explicitly
-- documented and added to this same draft packet before adoption.
CREATE TABLE security_command_receipts(
 owner_id TEXT NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 command_sequence INTEGER NOT NULL CHECK(command_sequence>0),
 operation_id BLOB NOT NULL CHECK(length(operation_id)=16),
 request_digest BLOB NOT NULL CHECK(length(request_digest)=32),
 command_kind INTEGER NOT NULL CHECK(command_kind IN(511,512,520,521,522,532,533,540,541,542,550,551,552,553)),
 result_version INTEGER NOT NULL CHECK(result_version=1),
 result_codec TEXT NOT NULL CHECK(result_codec IN('session_renewal_v1','session_refresh_v1','password_replace_v1','credential_issue_v1','credential_revoke_v1','pin_set_v1','pin_clear_v1','member_libraries_v1','profile_libraries_v1','profile_restrictions_v1','invitation_create_v1','invitation_edit_v1','invitation_revoke_v1','invitation_short_issue_v1')),
 applied_ms INTEGER NOT NULL CHECK(applied_ms>=0),
 result_digest BLOB NOT NULL CHECK(length(result_digest)=32),
 target_account_id TEXT,
 target_member_incarnation TEXT,
 target_profile_id TEXT,
 target_profile_lifetime TEXT,
 result_revision INTEGER,
 policy_inherits INTEGER,
 result_changed INTEGER,
 result_pin_revision INTEGER,
 result_pin_required INTEGER,
 result_count INTEGER NOT NULL CHECK(result_count BETWEEN 0 AND 1000),
 result_key_id TEXT,
 result_nonce BLOB,
 result_body BLOB,
 variable_bytes INTEGER GENERATED ALWAYS AS (
  length(CAST(owner_id AS BLOB))+length(command_incarnation)+length(operation_id)+length(request_digest)+length(result_digest)+length(CAST(result_codec AS BLOB))
  +coalesce(length(CAST(target_account_id AS BLOB)),0)+coalesce(length(CAST(target_member_incarnation AS BLOB)),0)
  +coalesce(length(CAST(target_profile_id AS BLOB)),0)+coalesce(length(CAST(target_profile_lifetime AS BLOB)),0)
  +coalesce(length(CAST(result_key_id AS BLOB)),0)+coalesce(length(result_nonce),0)+coalesce(length(result_body),0)) VIRTUAL,
 PRIMARY KEY(owner_id,command_incarnation,command_sequence),
 UNIQUE(owner_id,command_incarnation,operation_id),
 UNIQUE(owner_id,command_incarnation,command_sequence,command_kind),
 FOREIGN KEY(owner_id,command_incarnation) REFERENCES security_command_owners(id,command_incarnation),
 CHECK((command_kind=511 AND result_codec='session_renewal_v1')
  OR (command_kind=512 AND result_codec='session_refresh_v1')
  OR (command_kind=520 AND result_codec='password_replace_v1')
  OR (command_kind=521 AND result_codec='credential_issue_v1')
  OR (command_kind=522 AND result_codec='credential_revoke_v1')
  OR (command_kind=532 AND result_codec='pin_set_v1')
  OR (command_kind=533 AND result_codec='pin_clear_v1')
  OR (command_kind=540 AND result_codec='member_libraries_v1')
  OR (command_kind=541 AND result_codec='profile_libraries_v1')
  OR (command_kind=542 AND result_codec='profile_restrictions_v1')
  OR (command_kind=550 AND result_codec='invitation_create_v1')
  OR (command_kind=551 AND result_codec='invitation_edit_v1')
  OR (command_kind=552 AND result_codec='invitation_revoke_v1')
  OR (command_kind=553 AND result_codec='invitation_short_issue_v1')),
 CHECK((command_kind IN(540,541) AND target_account_id IS NOT NULL AND length(CAST(target_account_id AS BLOB)) BETWEEN 1 AND 128
  AND target_member_incarnation IS NOT NULL AND length(target_member_incarnation)=32
  AND result_revision IS NOT NULL AND result_revision BETWEEN 1 AND 9007199254740991
  AND policy_inherits IS NOT NULL AND policy_inherits IN(0,1)
  AND result_changed IS NOT NULL AND result_changed IN(0,1)
  AND result_pin_revision IS NULL AND result_pin_required IS NULL
  AND result_key_id IS NULL AND result_nonce IS NULL AND result_body IS NULL
  AND ((command_kind=540 AND target_profile_id IS NULL AND target_profile_lifetime IS NULL AND policy_inherits=0)
   OR (command_kind=541 AND target_profile_id IS NOT NULL AND length(CAST(target_profile_id AS BLOB)) BETWEEN 1 AND 128
    AND target_profile_lifetime IS NOT NULL AND length(target_profile_lifetime)=32))
  AND (policy_inherits=0 OR result_count=0))
 OR (command_kind IN(532,533) AND target_account_id IS NOT NULL AND length(CAST(target_account_id AS BLOB)) BETWEEN 1 AND 128
  AND target_member_incarnation IS NOT NULL AND length(target_member_incarnation)=32
  AND target_profile_id IS NOT NULL AND length(CAST(target_profile_id AS BLOB)) BETWEEN 1 AND 128
  AND target_profile_lifetime IS NOT NULL AND length(target_profile_lifetime)=32
  AND result_revision IS NOT NULL AND result_revision BETWEEN 1 AND 9007199254740991
  AND result_pin_revision IS NOT NULL AND result_pin_revision>0
  AND result_pin_required IS NOT NULL AND result_pin_required IN(0,1)
  AND ((command_kind=532 AND result_pin_required=1) OR (command_kind=533 AND result_pin_required=0))
  AND result_changed IS NOT NULL AND result_changed=1 AND policy_inherits IS NULL AND result_count=0
  AND result_key_id IS NULL AND result_nonce IS NULL AND result_body IS NULL)
 OR (command_kind IN(511,512,520,521,522,553) AND target_account_id IS NULL AND target_member_incarnation IS NULL
  AND target_profile_id IS NULL AND target_profile_lifetime IS NULL AND result_revision IS NULL
  AND policy_inherits IS NULL AND result_changed IS NULL AND result_pin_revision IS NULL AND result_pin_required IS NULL
  AND result_count=0 AND result_key_id IS NULL AND result_nonce IS NULL AND result_body IS NULL)
 OR (command_kind=542 AND target_account_id IS NULL AND target_member_incarnation IS NULL
  AND target_profile_id IS NULL AND target_profile_lifetime IS NULL AND result_revision IS NULL
  AND policy_inherits IS NULL AND result_changed IS NULL AND result_pin_revision IS NULL AND result_pin_required IS NULL
  AND result_count<=64 AND result_key_id IS NULL AND result_nonce IS NULL AND result_body IS NULL)
 OR (command_kind IN(550,551,552) AND target_account_id IS NULL AND target_member_incarnation IS NULL
  AND target_profile_id IS NULL AND target_profile_lifetime IS NULL AND result_revision IS NULL
  AND policy_inherits IS NULL AND result_changed IS NULL AND result_pin_revision IS NULL AND result_pin_required IS NULL
  AND result_count<=256
  AND ((command_kind=550 AND result_key_id IS NOT NULL AND length(CAST(result_key_id AS BLOB)) BETWEEN 1 AND 128
    AND result_nonce IS NOT NULL AND length(result_nonce)=12 AND result_body IS NOT NULL AND length(result_body)=59)
   OR (command_kind IN(551,552) AND result_key_id IS NULL AND result_nonce IS NULL AND result_body IS NULL))))
) WITHOUT ROWID, STRICT;

-- Complete ordered policy cohort; no live target reconstruction on replay.
-- Owner/receipt deletion requires explicit bounded child-first reclaim, no CASCADE.
CREATE TABLE security_result_libraries(
 owner_id TEXT NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 command_sequence INTEGER NOT NULL CHECK(command_sequence>0),
 cohort TEXT NOT NULL CHECK(cohort IN('policy','invitation')),
 ordinal INTEGER NOT NULL CHECK((cohort='policy' AND ordinal BETWEEN 0 AND 999) OR (cohort='invitation' AND ordinal BETWEEN 0 AND 255)),
 library_id TEXT NOT NULL CHECK(length(CAST(library_id AS BLOB)) BETWEEN 1 AND 128),
 variable_bytes INTEGER GENERATED ALWAYS AS (length(CAST(owner_id AS BLOB))+length(command_incarnation)+length(CAST(cohort AS BLOB))+length(CAST(library_id AS BLOB))) VIRTUAL,
 PRIMARY KEY(owner_id,command_incarnation,command_sequence,cohort,ordinal),
 UNIQUE(owner_id,command_incarnation,command_sequence,cohort,library_id),
 FOREIGN KEY(owner_id,command_incarnation,command_sequence) REFERENCES security_command_receipts(owner_id,command_incarnation,command_sequence)
) WITHOUT ROWID, STRICT;

-- Full immutable Invitation DTO source facts. Only Code on creation is protected
-- separately in the header. Edit/revoke reconstruct from THIS row and its saved
-- invitation library cohort, never access_invitations or current member limits.
CREATE TABLE security_invitation_results(
 owner_id TEXT NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 command_sequence INTEGER NOT NULL CHECK(command_sequence>0),
 invitation_id TEXT NOT NULL,
 server_id TEXT NOT NULL,
 notes TEXT CHECK(notes IS NULL OR length(notes)<=500),
 role TEXT NOT NULL CHECK(role IN('admin','member')),
 state TEXT NOT NULL CHECK(state IN('pending','accepted','revoked')),
 created_ms INTEGER NOT NULL,
 expires_ms INTEGER NOT NULL,
 accepted_ms INTEGER,
 account_id TEXT,
 revision INTEGER NOT NULL CHECK(revision>0),
 bound_username TEXT NOT NULL,
 max_streams_mode TEXT NOT NULL CHECK(max_streams_mode IN('inherit','unlimited','limit')),
 max_streams INTEGER NOT NULL CHECK(max_streams_mode='limit' AND max_streams BETWEEN 1 AND 1000 OR max_streams_mode<>'limit' AND max_streams=0),
 allow_remote INTEGER NOT NULL CHECK(allow_remote IN(0,1)),
 access_tokens INTEGER NOT NULL CHECK(access_tokens IN(0,1)),
 max_content_rating TEXT NOT NULL CHECK(length(max_content_rating)<=32),
 source_code_hash TEXT NOT NULL,
 variable_bytes INTEGER GENERATED ALWAYS AS (
  length(CAST(owner_id AS BLOB))+length(command_incarnation)+length(CAST(invitation_id AS BLOB))+length(CAST(server_id AS BLOB))
  +coalesce(length(CAST(notes AS BLOB)),0)+length(CAST(role AS BLOB))+length(CAST(state AS BLOB))
  +coalesce(length(CAST(account_id AS BLOB)),0)+length(CAST(bound_username AS BLOB))
  +length(CAST(max_streams_mode AS BLOB))+length(CAST(max_content_rating AS BLOB))+length(CAST(source_code_hash AS BLOB))) VIRTUAL,
 PRIMARY KEY(owner_id,command_incarnation,command_sequence),
 FOREIGN KEY(owner_id,command_incarnation,command_sequence) REFERENCES security_command_receipts(owner_id,command_incarnation,command_sequence)
) WITHOUT ROWID, STRICT;

-- Sparse lifetime-bound deny/reentry facts. These survive source cleanup and
-- never project a library/title fact to users. One scope generation changes;
-- no UPDATE of F session families. Clearing a PIN skips PIN gates because the
-- authoritative profile has no PIN; old tokens cannot revive a later new PIN.
CREATE TABLE identity_security_fences(
 id INTEGER PRIMARY KEY,
 scope_kind TEXT NOT NULL CHECK(scope_kind IN('device','profile_pin','profile_remember','profile_lineage')),
 account_id TEXT NOT NULL CHECK(length(CAST(account_id AS BLOB)) BETWEEN 1 AND 128),
 member_incarnation TEXT NOT NULL CHECK(length(member_incarnation)=32),
 profile_id TEXT NOT NULL DEFAULT '',
 profile_lifetime TEXT NOT NULL DEFAULT '',
 device_id TEXT NOT NULL DEFAULT '',
 lineage_id TEXT NOT NULL DEFAULT '',
 generation INTEGER NOT NULL DEFAULT 1 CHECK(generation>0),
 denied INTEGER NOT NULL DEFAULT 0 CHECK(denied IN(0,1)),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0),
 updated_ms INTEGER NOT NULL CHECK(updated_ms>=0),
 CHECK((scope_kind='device' AND length(CAST(device_id AS BLOB)) BETWEEN 1 AND 128 AND profile_id='' AND profile_lifetime='' AND lineage_id='')
 OR (scope_kind IN('profile_pin','profile_remember') AND length(CAST(profile_id AS BLOB)) BETWEEN 1 AND 128 AND length(profile_lifetime)=32 AND device_id='' AND lineage_id='' AND denied=0)
 OR (scope_kind='profile_lineage' AND length(CAST(profile_id AS BLOB)) BETWEEN 1 AND 128 AND length(profile_lifetime)=32 AND device_id='' AND length(CAST(lineage_id AS BLOB)) BETWEEN 1 AND 128 AND denied=0)),
 UNIQUE(scope_kind,account_id,member_incarnation,profile_id,profile_lifetime,device_id,lineage_id),
 UNIQUE(id,scope_kind)
) STRICT;

-- At most four closed witnesses per family, written at verified issuance or
-- selection/confirmation. A keep-family updates at most its own witness. A
-- generation mismatch denies or narrows to PIN reentry under current source
-- policy; a missing witness is never a grant. FK/indexes bound cleanup probes.
CREATE TABLE authorization_family_security_fences(
 family_id TEXT NOT NULL REFERENCES authorization_session_families(id),
 scope_kind TEXT NOT NULL CHECK(scope_kind IN('device','profile_pin','profile_remember','profile_lineage')),
 fence_id INTEGER NOT NULL,
 confirmed_generation INTEGER NOT NULL CHECK(confirmed_generation>0),
 PRIMARY KEY(family_id,scope_kind),
 FOREIGN KEY(fence_id,scope_kind) REFERENCES identity_security_fences(id,scope_kind)
) WITHOUT ROWID, STRICT;
CREATE INDEX authorization_family_security_fence_parent ON authorization_family_security_fences(fence_id,scope_kind,family_id);

-- Existing access_invitation_page already covers sent+ID. Add ONLY missing
-- state-filtered keysets and expiry discovery. Dynamic visible status is still
-- computed from state+fenced time, never a maintained per-user status copy.
CREATE INDEX access_invitation_state_page ON access_invitations(state,created_ms DESC,id DESC);
CREATE INDEX access_invitation_expiry ON access_invitations(state,expires_ms,id);


-- 520 ordinary LOCAL password replacement has a void public response. Save the
-- exact non-secret outcome, not either password hash, bearer or factor secret.
-- No FK to live account/profile rows: later retirement cannot erase the result.
CREATE TABLE security_password_results(
 owner_id TEXT NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 command_sequence INTEGER NOT NULL CHECK(command_sequence>0),
 command_kind INTEGER NOT NULL DEFAULT 520 CHECK(command_kind=520),
 account_id TEXT NOT NULL,
 member_incarnation TEXT NOT NULL CHECK(length(member_incarnation)=32),
 profile_id TEXT NOT NULL,
 profile_lifetime TEXT NOT NULL CHECK(length(profile_lifetime)=32),
 member_revision INTEGER NOT NULL CHECK(member_revision>0),
 profile_revision INTEGER NOT NULL CHECK(profile_revision>0),
 previous_epoch INTEGER NOT NULL CHECK(previous_epoch BETWEEN 1 AND 9223372036854775806),
 result_epoch INTEGER NOT NULL CHECK(result_epoch=previous_epoch+1),
 variable_bytes INTEGER GENERATED ALWAYS AS (
  length(CAST(owner_id AS BLOB))+length(command_incarnation)+length(CAST(account_id AS BLOB))
  +length(CAST(member_incarnation AS BLOB))+length(CAST(profile_id AS BLOB))+length(CAST(profile_lifetime AS BLOB))) VIRTUAL,
 PRIMARY KEY(owner_id,command_incarnation,command_sequence),
 FOREIGN KEY(owner_id,command_incarnation,command_sequence,command_kind)
  REFERENCES security_command_receipts(owner_id,command_incarnation,command_sequence,command_kind)
) WITHOUT ROWID, STRICT;

-- 521/522 save the COMPLETE native Credential DTO, including captured state,
-- nullable timestamp semantics, CredentialMember names, and ordered scopes.
-- A replay never derives state/expiry from a later clock or current family.
-- The creation Secret is a separate exact prefixed ASCII wire, not JSON or a
-- normalized permission body. Existing Token() emits 43 base64url characters;
-- pk./pat./papp. make plaintext lengths 46/47/48, plus a 16-byte GCM tag.
-- Key ID denotes immutable key custody; secret_aad_digest binds this receipt,
-- kind, scope/lifetimes, native secret_hash and exact source/result manifest.
CREATE TABLE security_credential_results(
 owner_id TEXT NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 command_sequence INTEGER NOT NULL CHECK(command_sequence>0),
 command_kind INTEGER NOT NULL CHECK(command_kind IN(521,522)),
 credential_id TEXT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN('key','token','app_password')),
 name TEXT NOT NULL,
 scope_count INTEGER NOT NULL CHECK(scope_count BETWEEN 1 AND 4),
 hint TEXT NOT NULL,
 created_ms INTEGER NOT NULL CHECK(created_ms>=0),
 expires_ms INTEGER NOT NULL CHECK(expires_ms>=0),
 last_used_ms INTEGER NOT NULL CHECK(last_used_ms>=0),
 revoked_ms INTEGER NOT NULL CHECK(revoked_ms>=0),
 state TEXT NOT NULL CHECK(state IN('active','expired','revoked','ended')),
 ended_reason TEXT NOT NULL CHECK(ended_reason IN('','member_disabled','member_deleted','profile_deleted','permission_removed','unavailable')),
 account_id TEXT NOT NULL,
 member_incarnation TEXT NOT NULL CHECK(length(member_incarnation)=32),
 username TEXT NOT NULL,
 profile_id TEXT NOT NULL,
 profile_lifetime TEXT NOT NULL CHECK(length(profile_lifetime)=32),
 profile_name TEXT NOT NULL,
 family_id TEXT NOT NULL,
 family_incarnation BLOB NOT NULL CHECK(length(family_incarnation)=16),
 device_id TEXT NOT NULL,
 secret_hash TEXT NOT NULL CHECK(length(secret_hash)=43 AND secret_hash NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(secret_hash,43,1) IN('A','E','I','M','Q','U','Y','c','g','k','o','s','w','0','4','8')),
 secret_key_id TEXT,
 secret_nonce BLOB,
 secret_body BLOB,
 secret_aad_digest BLOB,
 variable_bytes INTEGER GENERATED ALWAYS AS (
  length(CAST(owner_id AS BLOB))+length(command_incarnation)+length(CAST(credential_id AS BLOB))+length(CAST(kind AS BLOB))
  +length(CAST(name AS BLOB))+length(CAST(hint AS BLOB))+length(CAST(state AS BLOB))+length(CAST(ended_reason AS BLOB))
  +length(CAST(account_id AS BLOB))+length(CAST(member_incarnation AS BLOB))+length(CAST(username AS BLOB))
  +length(CAST(profile_id AS BLOB))+length(CAST(profile_lifetime AS BLOB))+length(CAST(profile_name AS BLOB))
  +length(CAST(family_id AS BLOB))+length(family_incarnation)+length(CAST(device_id AS BLOB))+length(CAST(secret_hash AS BLOB))
  +coalesce(length(CAST(secret_key_id AS BLOB)),0)+coalesce(length(secret_nonce),0)+coalesce(length(secret_body),0)+coalesce(length(secret_aad_digest),0)) VIRTUAL,
 PRIMARY KEY(owner_id,command_incarnation,command_sequence),
 FOREIGN KEY(owner_id,command_incarnation,command_sequence,command_kind)
  REFERENCES security_command_receipts(owner_id,command_incarnation,command_sequence,command_kind),
 CHECK((state='ended')=(ended_reason<>'')),
 CHECK((kind='app_password' AND hint='') OR (kind IN('key','token') AND length(CAST(hint AS BLOB))=6)),
 CHECK((command_kind=521 AND state='active' AND revoked_ms=0 AND last_used_ms=0
  AND secret_key_id IS NOT NULL AND length(CAST(secret_key_id AS BLOB)) BETWEEN 1 AND 128
  AND secret_nonce IS NOT NULL AND length(secret_nonce)=12
  AND secret_body IS NOT NULL AND length(secret_body)=CASE kind WHEN 'key' THEN 62 WHEN 'token' THEN 63 WHEN 'app_password' THEN 64 END
  AND secret_aad_digest IS NOT NULL AND length(secret_aad_digest)=32)
 OR (command_kind=522 AND secret_key_id IS NULL AND secret_nonce IS NULL AND secret_body IS NULL AND secret_aad_digest IS NULL))
) WITHOUT ROWID, STRICT;

CREATE TABLE security_credential_result_scopes(
 owner_id TEXT NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 command_sequence INTEGER NOT NULL CHECK(command_sequence>0),
 ordinal INTEGER NOT NULL CHECK(ordinal BETWEEN 0 AND 3),
 scope TEXT NOT NULL CHECK(scope IN('read','playback','library','admin')),
 variable_bytes INTEGER GENERATED ALWAYS AS (
  length(CAST(owner_id AS BLOB))+length(command_incarnation)+length(CAST(scope AS BLOB))) VIRTUAL,
 PRIMARY KEY(owner_id,command_incarnation,command_sequence,ordinal),
 UNIQUE(owner_id,command_incarnation,command_sequence,scope),
 FOREIGN KEY(owner_id,command_incarnation,command_sequence)
  REFERENCES security_credential_results(owner_id,command_incarnation,command_sequence)
) WITHOUT ROWID, STRICT;

-- Existing owner key/app-password recovery also needs purpose-specific CURRENT
-- protected source custody, separate from immutable issuance delivery. No token
-- recovery and no plaintext api_credentials.secret write in the new path.
-- Parent cutover must prove the exact digest, scope and still-current credential
-- natively before revealing; the old source permission fields stay authoritative.
-- Restrictive FK requires explicit bounded secret drain before source deletion.
CREATE TABLE security_credential_secrets(
 credential_id TEXT NOT NULL PRIMARY KEY REFERENCES api_credentials(id),
 kind TEXT NOT NULL CHECK(kind IN('key','app_password')),
 account_id TEXT NOT NULL,
 member_incarnation TEXT NOT NULL CHECK(length(member_incarnation)=32),
 profile_id TEXT NOT NULL,
 profile_lifetime TEXT NOT NULL CHECK(length(profile_lifetime)=32),
 family_id TEXT NOT NULL,
 family_incarnation BLOB NOT NULL CHECK(length(family_incarnation)=16),
 secret_hash TEXT NOT NULL CHECK(length(secret_hash)=43 AND secret_hash NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(secret_hash,43,1) IN('A','E','I','M','Q','U','Y','c','g','k','o','s','w','0','4','8')),
 key_id TEXT NOT NULL CHECK(length(CAST(key_id AS BLOB)) BETWEEN 1 AND 128),
 nonce BLOB NOT NULL CHECK(length(nonce)=12),
 body BLOB NOT NULL,
 aad_digest BLOB NOT NULL CHECK(length(aad_digest)=32),
 variable_bytes INTEGER GENERATED ALWAYS AS (
  length(CAST(credential_id AS BLOB))+length(CAST(kind AS BLOB))+length(CAST(account_id AS BLOB))
  +length(CAST(member_incarnation AS BLOB))+length(CAST(profile_id AS BLOB))+length(CAST(profile_lifetime AS BLOB))
  +length(CAST(family_id AS BLOB))+length(family_incarnation)+length(CAST(secret_hash AS BLOB))
  +length(CAST(key_id AS BLOB))+length(nonce)+length(body)+length(aad_digest)) VIRTUAL,
 CHECK(length(body)=CASE kind WHEN 'key' THEN 62 WHEN 'app_password' THEN 64 END)
) STRICT;


-- Legacy refresh logic already retires the one preceding recovery receipt on
-- successor use. Enforce that invariant natively; replace the existing access
-- path in place, without an equivalent duplicate index or a family-wide DELETE.
-- This changes family-parent enforcement and MUST stay after phase-1a lock.
DROP INDEX identity_refresh_receipts_family;
CREATE UNIQUE INDEX identity_refresh_receipts_family ON identity_refresh_receipts(family_id);

-- 511 saves AuthorizationEnvelope(protocolVersion=2.0, access token, expiry,
-- horizon, family, string generation); 512 saves Envelope (same access metadata,
-- refresh token, device/installation and complete Viewer fields). No full JSON
-- envelope is encrypted. The TWO exact 43-character issued token wires have
-- separate 12-byte nonces and 59-byte AES-GCM bodies. Normalized scope/generation
-- remains native. The result codec fixes JSON string generation at the boundary.
-- Retained immutable evidence does NOT extend legacy secret disclosure: renewal
-- requires its exact controller/request proof; refresh requires the still-active
-- successor-recovery window, exact predecessor/request/installation and current
-- family/permission fences BEFORE loading/decrypting any historical token.
CREATE TABLE security_session_rotation_results(
 owner_id TEXT NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 command_sequence INTEGER NOT NULL CHECK(command_sequence>0),
 command_kind INTEGER NOT NULL CHECK(command_kind IN(511,512)),
 wire_variant TEXT NOT NULL CHECK(wire_variant IN('authorization_envelope_v2','envelope_v2')),
 protocol_version TEXT,
 family_id TEXT NOT NULL,
 family_incarnation BLOB NOT NULL CHECK(length(family_incarnation)=16),
 server_id TEXT NOT NULL,
 account_id TEXT NOT NULL,
 member_incarnation TEXT NOT NULL CHECK(length(member_incarnation)=32),
 profile_id TEXT NOT NULL,
 profile_lifetime TEXT NOT NULL CHECK(length(profile_lifetime)=32),
 device_id TEXT NOT NULL,
 installation_id TEXT NOT NULL,
 authority TEXT NOT NULL CHECK(authority='local'),
 role TEXT NOT NULL CHECK(role IN('account','owner','admin','member')),
 epoch INTEGER NOT NULL CHECK(epoch>0),
 authority_generation INTEGER NOT NULL CHECK(authority_generation>0),
 access_predecessor_generation INTEGER NOT NULL CHECK(access_predecessor_generation BETWEEN 1 AND 9223372036854775806),
 generation INTEGER NOT NULL CHECK(generation=access_predecessor_generation+1),
 access_predecessor_hash TEXT NOT NULL CHECK(length(access_predecessor_hash)=43),
 access_hash TEXT NOT NULL CHECK(length(access_hash)=43 AND access_hash<>access_predecessor_hash),
 access_expires_at TEXT NOT NULL,
 authorization_horizon TEXT NOT NULL,
 request_id TEXT NOT NULL,
 controller_id TEXT,
 controller_epoch TEXT,
 refresh_predecessor_generation INTEGER,
 refresh_predecessor_hash TEXT,
 refresh_hash TEXT,
 prior_receipt_hash TEXT,
 key_id TEXT NOT NULL CHECK(length(CAST(key_id AS BLOB)) BETWEEN 1 AND 128),
 access_nonce BLOB NOT NULL CHECK(length(access_nonce)=12),
 access_body BLOB NOT NULL CHECK(length(access_body)=59),
 refresh_nonce BLOB,
 refresh_body BLOB,
 aad_digest BLOB NOT NULL CHECK(length(aad_digest)=32),
 variable_bytes INTEGER GENERATED ALWAYS AS (
  length(CAST(owner_id AS BLOB))+length(command_incarnation)+length(CAST(wire_variant AS BLOB))+coalesce(length(CAST(protocol_version AS BLOB)),0)
  +length(CAST(family_id AS BLOB))+length(family_incarnation)+length(CAST(server_id AS BLOB))+length(CAST(account_id AS BLOB))
  +length(CAST(member_incarnation AS BLOB))+length(CAST(profile_id AS BLOB))+length(CAST(profile_lifetime AS BLOB))
  +length(CAST(device_id AS BLOB))+length(CAST(installation_id AS BLOB))+length(CAST(authority AS BLOB))+length(CAST(role AS BLOB))
  +length(CAST(access_predecessor_hash AS BLOB))+length(CAST(access_hash AS BLOB))+length(CAST(access_expires_at AS BLOB))
  +length(CAST(authorization_horizon AS BLOB))+length(CAST(request_id AS BLOB))+coalesce(length(CAST(controller_id AS BLOB)),0)
  +coalesce(length(CAST(controller_epoch AS BLOB)),0)+coalesce(length(CAST(refresh_predecessor_hash AS BLOB)),0)
  +coalesce(length(CAST(refresh_hash AS BLOB)),0)+coalesce(length(CAST(prior_receipt_hash AS BLOB)),0)
  +length(CAST(key_id AS BLOB))+length(access_nonce)+length(access_body)+coalesce(length(refresh_nonce),0)+coalesce(length(refresh_body),0)+length(aad_digest)) VIRTUAL,
 PRIMARY KEY(owner_id,command_incarnation,command_sequence),
 FOREIGN KEY(owner_id,command_incarnation,command_sequence,command_kind)
  REFERENCES security_command_receipts(owner_id,command_incarnation,command_sequence,command_kind),
 CHECK((command_kind=511 AND wire_variant='authorization_envelope_v2' AND protocol_version IS NOT NULL AND protocol_version='2.0'
  AND controller_id IS NOT NULL AND length(CAST(controller_id AS BLOB)) BETWEEN 1 AND 128
  AND controller_epoch IS NOT NULL AND length(CAST(controller_epoch AS BLOB)) BETWEEN 1 AND 128
  AND refresh_predecessor_generation IS NULL AND refresh_predecessor_hash IS NULL AND refresh_hash IS NULL
  AND prior_receipt_hash IS NULL AND refresh_nonce IS NULL AND refresh_body IS NULL)
 OR (command_kind=512 AND wire_variant='envelope_v2' AND protocol_version IS NULL AND controller_id IS NULL AND controller_epoch IS NULL
  AND refresh_predecessor_generation IS NOT NULL AND refresh_predecessor_generation BETWEEN 1 AND access_predecessor_generation
  AND refresh_predecessor_hash IS NOT NULL AND length(refresh_predecessor_hash)=43
  AND refresh_hash IS NOT NULL AND length(refresh_hash)=43 AND refresh_hash<>refresh_predecessor_hash
  AND refresh_hash<>access_hash AND refresh_hash<>access_predecessor_hash
  AND refresh_predecessor_hash<>access_hash AND refresh_predecessor_hash<>access_predecessor_hash
  AND (prior_receipt_hash IS NULL OR length(prior_receipt_hash)=43 AND prior_receipt_hash<>refresh_predecessor_hash)
  AND refresh_nonce IS NOT NULL AND length(refresh_nonce)=12 AND refresh_nonce<>access_nonce
  AND refresh_body IS NOT NULL AND length(refresh_body)=59))
) WITHOUT ROWID, STRICT;

-- Complete immutable ProfileRestrictions response for concrete purpose 542.
-- Capture the resolved maximum_age; historical replay never re-runs ratings.
-- Labels preserve the JSON-decoded response order, including equal spellings
-- after invalid source UTF-8 is replaced. No UNIQUE(label) or re-sorting.
CREATE TABLE security_restriction_results(
 owner_id TEXT NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 command_sequence INTEGER NOT NULL CHECK(command_sequence>0),
 command_kind INTEGER NOT NULL DEFAULT 542 CHECK(command_kind=542),
 account_id TEXT NOT NULL CHECK(length(CAST(account_id AS BLOB)) BETWEEN 1 AND 128),
 member_incarnation TEXT NOT NULL CHECK(length(member_incarnation)=32),
 profile_id TEXT NOT NULL CHECK(length(CAST(profile_id AS BLOB)) BETWEEN 1 AND 128),
 profile_lifetime TEXT NOT NULL CHECK(length(profile_lifetime)=32),
 revision INTEGER NOT NULL CHECK(revision>=2),
 rating_system TEXT NOT NULL,
 maximum_age_rating TEXT NOT NULL,
 maximum_age INTEGER NOT NULL CHECK(maximum_age>=-1),
 allow_unrated INTEGER NOT NULL CHECK(allow_unrated IN(0,1)),
 allow_downloads INTEGER NOT NULL CHECK(allow_downloads IN(0,1)),
 allow_live_tv INTEGER NOT NULL CHECK(allow_live_tv IN(0,1)),
 allow_dvr INTEGER NOT NULL CHECK(allow_dvr IN(0,1)),
 allow_watch_together INTEGER NOT NULL CHECK(allow_watch_together IN(0,1)),
 variable_bytes INTEGER GENERATED ALWAYS AS (
  length(CAST(owner_id AS BLOB))+length(command_incarnation)+length(CAST(account_id AS BLOB))+length(CAST(member_incarnation AS BLOB))
  +length(CAST(profile_id AS BLOB))+length(CAST(profile_lifetime AS BLOB))+length(CAST(rating_system AS BLOB))+length(CAST(maximum_age_rating AS BLOB))) VIRTUAL,
 PRIMARY KEY(owner_id,command_incarnation,command_sequence),
 FOREIGN KEY(owner_id,command_incarnation,command_sequence,command_kind) REFERENCES security_command_receipts(owner_id,command_incarnation,command_sequence,command_kind)
) WITHOUT ROWID, STRICT;

-- Existing source normalization accepts <=64 labels of <=120 input bytes.
-- JSON's replacement of each invalid byte by U+FFFD can produce <=360 response
-- UTF-8 bytes. This is an encoding bound, not a new product permission cap.
CREATE TABLE security_restriction_result_labels(
 owner_id TEXT NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 command_sequence INTEGER NOT NULL CHECK(command_sequence>0),
 ordinal INTEGER NOT NULL CHECK(ordinal BETWEEN 0 AND 63),
 label TEXT NOT NULL CHECK(length(CAST(label AS BLOB)) BETWEEN 1 AND 360),
 variable_bytes INTEGER GENERATED ALWAYS AS (
  length(CAST(owner_id AS BLOB))+length(command_incarnation)+length(CAST(label AS BLOB))) VIRTUAL,
 PRIMARY KEY(owner_id,command_incarnation,command_sequence,ordinal),
 FOREIGN KEY(owner_id,command_incarnation,command_sequence) REFERENCES security_restriction_results(owner_id,command_incarnation,command_sequence)
) WITHOUT ROWID, STRICT;

-- Concrete 553 output: actual staged serverId, exact expiry and the separately
-- protected first-response ShortCode. Retry still omits ShortCode publicly.
-- Display is 16 Crockford characters in four groups of four: 19 ASCII bytes;
-- 35 encrypted bytes = exact display + GCM's 16-byte tag, not a body quota.
CREATE TABLE security_invitation_short_results(
 owner_id TEXT NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 command_sequence INTEGER NOT NULL CHECK(command_sequence>0),
 command_kind INTEGER NOT NULL DEFAULT 553 CHECK(command_kind=553),
 invitation_id TEXT NOT NULL CHECK(length(CAST(invitation_id AS BLOB)) BETWEEN 1 AND 128),
 invitation_revision INTEGER NOT NULL CHECK(invitation_revision>0),
 invitation_code_hash TEXT NOT NULL CHECK(length(invitation_code_hash)=64 AND invitation_code_hash NOT GLOB '*[^0-9a-f]*'),
 invitation_role TEXT NOT NULL CHECK(invitation_role IN('member','admin')),
 invitation_expires_ms INTEGER NOT NULL CHECK(invitation_expires_ms>issued_ms),
 server_id TEXT NOT NULL CHECK(length(CAST(server_id AS BLOB))=47 AND substr(server_id,1,4)='srv_'
  AND substr(server_id,5) NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(server_id,47,1) IN('A','E','I','M','Q','U','Y','c','g','k','o','s','w','0','4','8')),
 server_public_key BLOB NOT NULL CHECK(length(server_public_key)=32),
 server_key_incarnation TEXT NOT NULL CHECK(length(CAST(server_key_incarnation AS BLOB)) BETWEEN 1 AND 128),
 server_reset_generation INTEGER NOT NULL CHECK(server_reset_generation>=0),
 sign_in_kind TEXT NOT NULL CHECK(sign_in_kind IN('local','portico')),
 kind_revision INTEGER NOT NULL CHECK(kind_revision>0 OR (sign_in_kind='portico' AND kind_revision=0)),
 issued_ms INTEGER NOT NULL CHECK(issued_ms BETWEEN 0 AND 9223372036854175807),
 expires_ms INTEGER NOT NULL CHECK(expires_ms>issued_ms
  AND ((invitation_expires_ms<=issued_ms+600000 AND expires_ms=invitation_expires_ms)
   OR (invitation_expires_ms>issued_ms+600000 AND expires_ms=issued_ms+600000))),
 lookup_hash TEXT NOT NULL CHECK(length(lookup_hash)=64 AND lookup_hash NOT GLOB '*[^0-9a-f]*'),
 binding_hash TEXT NOT NULL CHECK(length(binding_hash)=64 AND binding_hash NOT GLOB '*[^0-9a-f]*'),
 result_key_id TEXT NOT NULL CHECK(length(CAST(result_key_id AS BLOB)) BETWEEN 1 AND 128),
 result_nonce BLOB NOT NULL CHECK(length(result_nonce)=12),
 result_body BLOB NOT NULL CHECK(length(result_body)=35),
 result_aad_digest BLOB NOT NULL CHECK(length(result_aad_digest)=32),
 variable_bytes INTEGER GENERATED ALWAYS AS (
  length(CAST(owner_id AS BLOB))+length(command_incarnation)+length(CAST(invitation_id AS BLOB))+length(CAST(invitation_code_hash AS BLOB))
  +length(CAST(invitation_role AS BLOB))+length(CAST(server_id AS BLOB))+length(server_public_key)+length(CAST(server_key_incarnation AS BLOB))
  +length(CAST(sign_in_kind AS BLOB))+length(CAST(lookup_hash AS BLOB))+length(CAST(binding_hash AS BLOB))
  +length(CAST(result_key_id AS BLOB))+length(result_nonce)+length(result_body)+length(result_aad_digest)) VIRTUAL,
 PRIMARY KEY(owner_id,command_incarnation,command_sequence),
 FOREIGN KEY(owner_id,command_incarnation,command_sequence,command_kind) REFERENCES security_command_receipts(owner_id,command_incarnation,command_sequence,command_kind)
) WITHOUT ROWID, STRICT;
