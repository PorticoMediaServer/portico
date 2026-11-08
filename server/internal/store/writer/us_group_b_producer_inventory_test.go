package writer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// Schema group B (CHANGE-015) adds a NOT NULL authorization_session_families.security_kind
// with no default (0 ordinary, 1 credential), identity_devices.retired_ms, restrictive
// foreign keys from security fences and credential secrets, and the refresh-family
// uniqueness rule. The public schema is not embedded yet, so this census only pins every
// producer that must choose a concrete security_kind when the embed lands.
//
// v1-native means the statement runs against the store's own schema opener
// (server/internal/store/...). Legacy means the phase-1a persistence baseline: identity,
// access, persistence, testauth, the baseline-schema script and their fixtures. Legacy
// producers stay unchanged; they are listed so cutover work knows they exist and so a new
// unlisted producer cannot appear unnoticed.

const (
	usGroupBSelf = "server/internal/store/writer/us_group_b_producer_inventory_test.go"

	usGroupBV1Native = "v1-native"
	usGroupBLegacy   = "legacy"

	usGroupBOrdinary          = "ordinary"
	usGroupBCredential        = "credential"
	usGroupBMixed             = "ordinary+credential"
	usGroupBGeneratedEvidence = "generated-evidence"

	// PrefixBytes keeps each allowlist entry readable while still naming the exact
	// statement: 160 bytes is unique across the census at this head.
	usGroupBPrefixBytes = 160
)

var (
	usGroupBTables = map[string]bool{
		"authorization_session_families": true,
		"api_credentials":                true,
		"identity_devices":               true,
	}

	usGroupBInsertSQL = regexp.MustCompile(`(?i)\b(?:INSERT\s+(?:OR\s+\w+\s+)?INTO|REPLACE\s+INTO)\s+["` + "`" + `\[]?([A-Za-z_]+)`)
	usGroupBDeleteSQL = regexp.MustCompile(`(?i)\bDELETE\s+FROM\s+["` + "`" + `\[]?([A-Za-z_]+)`)
)

// usGroupBProducer is one expected statement site. File is repository-relative, and
// Prefix is the whitespace-collapsed SQL from the first keyword, truncated to
// usGroupBPrefixBytes. Lines in comments are the census head, not an assertion.
type usGroupBProducer struct {
	File      string
	Op        string
	Table     string
	Class     string
	Purpose   string
	Fixture   string
	Prefix    string
	Note      string
	PendingPR string
}

var usGroupBProducers = []usGroupBProducer{
	{File: "server/internal/access/access_test.go", Op: "insert", Table: "identity_devices", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy access fixture device", Prefix: `INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen,last_profile_id,ip) VALUES ('d-a','member','i-a','`},                                                                                  // line 1250
	{File: "server/internal/access/access_test.go", Op: "insert", Table: "identity_devices", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy access fixture count device", Prefix: `INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen,last_profile_id,ip) VALUES ('count-device','member`},                                                                            // line 1355
	{File: "server/internal/access/access_test.go", Op: "insert", Table: "authorization_session_families", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy access fixture member family", Prefix: `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,revoked) VALUES(?,'s`},                                                             // line 1372
	{File: "server/internal/access/credentials.go", Op: "insert", Table: "api_credentials", Class: usGroupBLegacy, Purpose: usGroupBCredential, Fixture: "", Note: "Legacy top-level API credential issue", Prefix: `INSERT INTO api_credentials(id,kind,account_id,profile_id,name,scopes,secret_hash,hint,family_id,device_id,created_ms,expires_ms,secret) VALUES(?,?,?,?,?,?,?,?,`},                                                                        // line 279
	{File: "server/internal/access/credentials.go", Op: "delete", Table: "api_credentials", Class: usGroupBLegacy, Purpose: usGroupBCredential, Fixture: "", Note: "Legacy expired/revoked API credential sweep", Prefix: `DELETE FROM api_credentials WHERE id IN (SELECT id FROM api_credentials WHERE (revoked_ms>0 AND revoked_ms<?) OR (expires_ms>0 AND expires_ms<?) LIMIT 128)`},                                                                       // line 454
	{File: "server/internal/access/integration_credentials.go", Op: "insert", Table: "api_credentials", Class: usGroupBLegacy, Purpose: usGroupBCredential, Fixture: "", Note: "Legacy integration credential issue", Prefix: `INSERT INTO api_credentials(id,kind,account_id,profile_id,name,scopes,secret_hash,hint,family_id,device_id,created_ms,expires_ms,parent_id,secret) VALUES(?,?,?,`},                                                              // line 123
	{File: "server/internal/catalog/play_history_facts_test.go", Op: "insert", Table: "identity_devices", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy catalog play-history fixture device", Prefix: `INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('dev','a','install','Device','not-an-app',`},                                                       // line 20
	{File: "server/internal/catalog/play_history_facts_test.go", Op: "insert", Table: "authorization_session_families", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy catalog play-history fixture family", Prefix: `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation) VALUES('family','se`},                                         // line 21
	{File: "server/internal/dbwork/authority_test.go", Op: "insert", Table: "authorization_session_families", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy dbwork authority fixture family", Prefix: `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id) VALUES(?,?,?,?)`},                                                                                                                         // line 29
	{File: "server/internal/dbwork/authority_test.go", Op: "delete", Table: "api_credentials", Class: usGroupBLegacy, Purpose: usGroupBCredential, Fixture: "", Note: "Legacy dbwork authority fixture credential cleanup", Prefix: `DELETE FROM api_credentials WHERE id=?`},                                                                                                                                                                                  // line 33
	{File: "server/internal/dbwork/authority_test.go", Op: "insert", Table: "authorization_session_families", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy dbwork minimal fixture family", Prefix: `INSERT INTO authorization_session_families(id) VALUES('family')`},                                                                                                                                                          // line 117
	{File: "server/internal/hosted/trust_revocation_test.go", Op: "insert", Table: "authorization_session_families", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy hosted trust fixture family", Prefix: `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,revoked) VALUES(?,?,`},                                                    // line 55
	{File: "server/internal/httpapi/playback_v1_queue_review_test.go", Op: "delete", Table: "identity_devices", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy playback review fixture device cleanup", Prefix: `DELETE FROM identity_devices WHERE id=?`},                                                                                                                                                                       // line 202
	{File: "server/internal/httpapi/restriction_matrix_test.go", Op: "insert", Table: "identity_devices", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy restriction matrix fixture device", Prefix: `INSERT INTO identity_devices(id,authority,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('matrix-device','local',?,'matri`},                                                         // line 1127
	{File: "server/internal/identity/api_credentials.go", Op: "insert", Table: "identity_devices", Class: usGroupBLegacy, Purpose: usGroupBCredential, Fixture: "", Note: "Legacy credential device birth", Prefix: `INSERT INTO identity_devices(id,authority,account_id,installation_id,name,platform,app,app_version,ip,first_seen,last_seen,remember_account) VALUES(?,?,?,?,?,?,`},                                                                        // line 163
	{File: "server/internal/identity/api_credentials.go", Op: "insert", Table: "authorization_session_families", Class: usGroupBLegacy, Purpose: usGroupBCredential, Fixture: "", Note: "Legacy credential family birth", Prefix: `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,revoked) VALUES(?,?,`},                                                          // line 168
	{File: "server/internal/identity/api_credentials.go", Op: "delete", Table: "identity_devices", Class: usGroupBLegacy, Purpose: usGroupBCredential, Fixture: "", Note: "Legacy credential device cleanup", Prefix: `DELETE FROM identity_devices WHERE id=? AND platform=?`},                                                                                                                                                                                // line 217
	{File: "server/internal/identity/credential_security_test.go", Op: "insert", Table: "identity_devices", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy credential-security fixture device", Prefix: `INSERT INTO identity_devices(id,authority,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('device-phone','local','account'`},                                                      // line 356
	{File: "server/internal/identity/device_contract_test.go", Op: "insert", Table: "authorization_session_families", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy expired-family fixture copy (INSERT-SELECT)", Prefix: `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,revoked) SELECT 'exp`},                                   // line 57
	{File: "server/internal/identity/devices.go", Op: "insert", Table: "identity_devices", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy interactive device birth (upsert)", Prefix: `INSERT INTO identity_devices(id,authority,account_id,installation_id,name,platform,app,app_version,ip,first_seen,last_seen) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CON`},                                                                        // line 147
	{File: "server/internal/identity/devices.go", Op: "delete", Table: "identity_devices", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy device removal", Prefix: `DELETE FROM identity_devices WHERE id=? AND authority=? AND account_id=?`},                                                                                                                                                                                   // line 502
	{File: "server/internal/identity/families.go", Op: "insert", Table: "authorization_session_families", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy ordinary family birth", Prefix: `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,revoked) VALUES(?,?,`},                                                                     // line 168
	{File: "server/internal/identity/list_limits_test.go", Op: "insert", Table: "identity_devices", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy list-limit fixture device", Prefix: `INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES(?,'account',?,'Device','mac','portico','1'`},                                                                       // line 29
	{File: "server/internal/identity/list_limits_test.go", Op: "insert", Table: "authorization_session_families", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy list-limit fixture family", Prefix: `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation) VALUES(?,'server','`},                                                         // line 33
	{File: "server/internal/identity/member_erasure.go", Op: "delete", Table: "identity_devices", Class: usGroupBLegacy, Purpose: usGroupBMixed, Fixture: "", Note: "Legacy member-erasure device sweep (includes credential devices)", Prefix: `DELETE FROM identity_devices WHERE authority='local' AND account_id=?`},                                                                                                                                       // line 191
	{File: "server/internal/identity/profile_purge_test.go", Op: "insert", Table: "authorization_session_families", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy profile-purge fixture family", Prefix: `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation) VALUES(?,'srv',?,?,`},                                                    // line 737
	{File: "server/internal/identity/prune_sessions.go", Op: "delete", Table: "authorization_session_families", Class: usGroupBLegacy, Purpose: usGroupBMixed, Fixture: "", Note: "Legacy session prune family delete", Prefix: `DELETE FROM authorization_session_families WHERE id=?`},                                                                                                                                                                       // line 132
	{File: "server/internal/identity/service.go", Op: "delete", Table: "identity_devices", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy retired-device cleanup with live-family guard", Prefix: `DELETE FROM identity_devices WHERE id=? AND NOT EXISTS(SELECT 1 FROM identity_device_families b JOIN authorization_session_families f ON f.id=b.family_id WHERE `},                                                            // line 286
	{File: "server/internal/mediaanalysis/target_test.go", Op: "insert", Table: "authorization_session_families", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy media-analysis fixture family", Prefix: `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,revoked) VALUES('fam`},                                                     // line 34
	{File: "server/internal/persistence/owner_completion_schema_test.go", Op: "insert", Table: "authorization_session_families", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy owner-completion schema fixture family", Prefix: `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation) VALUES('family','se`},                             // line 32
	{File: "server/internal/persistence/owner_completion_schema_test.go", Op: "delete", Table: "authorization_session_families", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy owner-completion fixture cleanup", Prefix: `DELETE FROM authorization_session_families WHERE id='family'`},                                                                                                                                       // line 89
	{File: "server/internal/persistence/trigger_plan_test.go", Op: "delete", Table: "identity_devices", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy trigger/plan statement key", Prefix: `DELETE FROM identity_devices WHERE authority='local' AND account_id='a'`},                                                                                                                                                           // line 25
	{File: "server/internal/playbackdisc/family_continuation_test.go", Op: "insert", Table: "authorization_session_families", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy playback continuance fixture family", Prefix: `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,revoked) VALUES(?,?,`},                                   // line 22
	{File: "server/internal/playbackdisc/family_continuation_test.go", Op: "insert", Table: "identity_devices", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy playback continuance fixture device", Prefix: `INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES(?,?,'disc-fixture-installation','Fixture',`},                                                 // line 25
	{File: "server/internal/playbackdisc/family_continuation_test.go", Op: "insert", Table: "authorization_session_families", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy playback continuance second-family copy (INSERT-SELECT)", Prefix: `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,revoked) SELECT 'ano`},               // line 94
	{File: "server/internal/playbackv1/query_capture_test.go", Op: "insert", Table: "identity_devices", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy playback query fixture device", Prefix: `INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('shuffle_device','account','installation',`},                                                               // line 130
	{File: "server/internal/playbackv1/queue_sweep_race_test.go", Op: "insert", Table: "identity_devices", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy queue-sweep fixture device", Prefix: `INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('device','account','installation','Fixture`},                                                               // line 14
	{File: "server/internal/reading/session_limit_test.go", Op: "insert", Table: "authorization_session_families", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy reading session-limit fixture family", Prefix: `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation) VALUES(?,?,?,?,?,?,`},                                             // line 30
	{File: "server/internal/store/internal/readfixture/source.go", Op: "insert", Table: "identity_devices", Class: usGroupBV1Native, Purpose: usGroupBOrdinary, Fixture: "P06", Note: "V1 readfixture device", Prefix: `INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('d','a','i','fixture','test','test','1','2`},                                                                     // line 206
	{File: "server/internal/store/internal/readfixture/source.go", Op: "insert", Table: "authorization_session_families", Class: usGroupBV1Native, Purpose: usGroupBOrdinary, Fixture: "P06", Note: "V1 readfixture ordinary family (INSERT-SELECT)", Prefix: `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,device_id,command_me`},                              // line 207
	{File: "server/internal/store/read/rb_filter_personal_cursor_fence_test.go", Op: "insert", Table: "identity_devices", Class: usGroupBV1Native, Purpose: usGroupBOrdinary, Fixture: "P06", Note: "V1 RB personal-fence fixture device", Prefix: `INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('device-q','b','installation-q','fixture',`},                                         // line 28
	{File: "server/internal/store/read/rb_filter_personal_cursor_fence_test.go", Op: "insert", Table: "authorization_session_families", Class: usGroupBV1Native, Purpose: usGroupBOrdinary, Fixture: "P06", Note: "V1 RB personal-fence fixture ordinary family (INSERT-SELECT)", Prefix: `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,device_id,command_me`},  // line 29
	{File: "server/internal/store/read/rs_detail_composed_test.go", Op: "insert", Table: "identity_devices", Class: usGroupBV1Native, Purpose: usGroupBOrdinary, Fixture: "P06", Note: "V1 RS detail-composed profile-q fixture device", Prefix: `INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('dq','b','iq','fixture-b','test','test','1`},                                           // line 170
	{File: "server/internal/store/read/rs_detail_composed_test.go", Op: "insert", Table: "authorization_session_families", Class: usGroupBV1Native, Purpose: usGroupBOrdinary, Fixture: "P06", Note: "V1 RS detail-composed profile-q fixture family", Prefix: `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,device_id,command_me`},                             // line 171
	{File: "server/internal/store/stmt/plan/autoindex_fences_test.go", Op: "insert", Table: "identity_devices", Class: usGroupBV1Native, Purpose: usGroupBOrdinary, Fixture: "P06", Note: "V1 plan autoindex fence fixture device", Prefix: `INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('d','a','i','fixture','test','test','1','2`},                                                // line 96
	{File: "server/internal/store/stmt/plan/autoindex_fences_test.go", Op: "insert", Table: "authorization_session_families", Class: usGroupBV1Native, Purpose: usGroupBOrdinary, Fixture: "P06", Note: "V1 plan autoindex fence fixture ordinary family (INSERT-SELECT)", Prefix: `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,device_id,command_me`},         // line 97
	{File: "server/internal/store/writer/crash_security_linux_test.go", Op: "insert", Table: "authorization_session_families", Class: usGroupBV1Native, Purpose: usGroupBOrdinary, Fixture: "P05", Note: "V1 WC security crash fixture ordinary family copy (INSERT-SELECT)", Prefix: `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,command_member_incar`},      // line 204
	{File: "server/internal/store/writer/us_authority_fixture_test.go", Op: "insert", Table: "identity_devices", Class: usGroupBV1Native, Purpose: usGroupBOrdinary, Fixture: "P05", Note: "V1 US owner-authority fixture device", Prefix: `INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('issuer-device','issuer',?,'Issuer','test'`},                                                 // line 83
	{File: "server/internal/store/writer/us_authority_fixture_test.go", Op: "insert", Table: "authorization_session_families", Class: usGroupBV1Native, Purpose: usGroupBOrdinary, Fixture: "P05", Note: "V1 US owner-authority fixture ordinary family (security_kind explicit)", Prefix: `INSERT INTO authorization_session_families(security_kind,id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,device`}, // line 86
	{File: "server/internal/store/writer/us_authority_test.go", Op: "insert", Table: "api_credentials", Class: usGroupBV1Native, Purpose: usGroupBCredential, Fixture: "P05", Note: "V1 US owner-authority credential marker fixture", Prefix: `INSERT INTO api_credentials(id,kind,account_id,profile_id,name,scopes,secret_hash,hint,family_id,device_id,created_ms) VALUES('credential','key','issuer','issue`},                                             // line 87
	{File: "server/internal/store/writer/us_authority_test.go", Op: "delete", Table: "api_credentials", Class: usGroupBV1Native, Purpose: usGroupBCredential, Fixture: "P05", Note: "V1 US owner-authority credential marker drain", Prefix: `DELETE FROM api_credentials`},                                                                                                                                                                                    // line 95
	{File: "server/internal/store/writer/us_pin_fences_test.go", Op: "insert", Table: "authorization_session_families", Class: usGroupBV1Native, Purpose: usGroupBOrdinary, Fixture: "P05", Note: "V1 US PIN fence fixture ordinary family (security_kind=0)", Prefix: `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,pin_gate,command_mem`},                     // line 57
	{File: "server/internal/store/writer/wc_progress_fixture.go", Op: "insert", Table: "authorization_session_families", Class: usGroupBV1Native, Purpose: usGroupBOrdinary, Fixture: "P05", Note: "V1 WC progress fixture ordinary family birth", Prefix: `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,command_member_incar`},                                 // line 38
	{File: "server/internal/store/writer/wc_security_008_native_test.go", Op: "insert", Table: "authorization_session_families", Class: usGroupBV1Native, Purpose: usGroupBOrdinary, Fixture: "P05", Note: "V1 WC security native fixture ordinary family", Prefix: `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,command_member_incar`},                        // line 17
	{File: "server/internal/testauth/session.go", Op: "insert", Table: "identity_devices", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy generic test-session device", Prefix: `INSERT INTO identity_devices(id,authority,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES(?,?,?,?,?,'test','portico','1','`},                                                                              // line 22
	{File: "server/internal/testauth/session.go", Op: "insert", Table: "authorization_session_families", Class: usGroupBLegacy, Purpose: usGroupBOrdinary, Fixture: "", Note: "Legacy generic test-session family", Prefix: `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation) VALUES(?,?,?,?,?,?,`},                                                                // line 23
	// Pending fixtures live only on unmerged branches. They are matched by file until
	// their PR merges; the merging task promotes them to exact op/table/prefix entries.
	{File: "server/internal/store/writer/us_family_birth_test.go", Class: usGroupBV1Native, Purpose: usGroupBMixed, Fixture: "P05", PendingPR: "#1809", Note: "Family birth fixtures: ordinary family plus credential marker rows"},
	{File: "server/internal/store/writer/us_family_fence_drain_test.go", Class: usGroupBV1Native, Purpose: usGroupBOrdinary, Fixture: "P05", PendingPR: "#1810", Note: "Family fence/drain fixtures: ordinary family and device rows with drain deletes"},
	{File: "server/internal/store/writer/us_credential_secret_drain_test.go", Class: usGroupBV1Native, Purpose: usGroupBCredential, Fixture: "P05", PendingPR: "#1812", Note: "Credential secret drain fixtures: credential row and its family"},
	{File: "server/internal/store/writer/us_refresh_receipt_compat_test.go", Class: usGroupBV1Native, Purpose: usGroupBOrdinary, Fixture: "P05", PendingPR: "#1813", Note: "Refresh receipt compatibility fixtures: two ordinary families, kind 0 at embed"},
}

// usGroupBOtherSources are the non-Go sites this census tracks. The Python baseline
// script is legacy and stays unchanged. The generated WC progress capture is evidence
// that names a producer; it is not itself a producer.
type usGroupBOtherSource struct {
	File  string
	Op    string
	Table string
	Class string
	Note  string
}

var usGroupBOtherSources = []usGroupBOtherSource{
	{
		File:  "server/scripts/check_p10_sqlite.py",
		Op:    "insert",
		Table: "authorization_session_families",
		Class: usGroupBLegacy,
		Note:  "Python baseline-schema test fixture; leave unchanged at the v1 embed",
	},
	{
		File:  "server/internal/store/writer/testdata/wc-progress-composition.json",
		Op:    "insert",
		Table: "authorization_session_families",
		Class: usGroupBGeneratedEvidence,
		Note:  "Generated WC progress VM capture; points at wc_progress_fixture.go, not a producer",
	},
}

type usGroupBSQL struct {
	Op    string
	Table string
	Text  string
}

type usGroupBSite struct {
	File   string
	Op     string
	Table  string
	Class  string
	Prefix string
	Line   int
}

// usGroupBServerRoot is the server module directory: server/internal/store/writer -> server.
func usGroupBServerRoot() string {
	return filepath.Join("..", "..", "..")
}

func usGroupBClassOf(file string) string {
	if strings.HasPrefix(file, "server/internal/store/") {
		return usGroupBV1Native
	}
	return usGroupBLegacy
}

func usGroupBKey(file, op, table, prefix string) string {
	return file + "\x00" + op + "\x00" + table + "\x00" + prefix
}

// usGroupBTruncate keeps whole runes so an allowlist entry copied from a failure
// message stays valid Go source.
func usGroupBTruncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}

// usGroupBStatements finds every insert, insert-select and delete site in one SQL
// string. Each match starts its own prefix, so a multi-statement literal cannot hide
// a second producer.
func usGroupBStatements(sql string) []usGroupBSQL {
	normalized := strings.Join(strings.Fields(sql), " ")
	type hit struct {
		at    int
		op    string
		table string
	}
	var hits []hit
	for _, pattern := range []struct {
		re *regexp.Regexp
		op string
	}{
		{usGroupBInsertSQL, "insert"},
		{usGroupBDeleteSQL, "delete"},
	} {
		for _, match := range pattern.re.FindAllStringSubmatchIndex(normalized, -1) {
			table := strings.ToLower(normalized[match[2]:match[3]])
			if !usGroupBTables[table] {
				continue
			}
			hits = append(hits, hit{at: match[0], op: pattern.op, table: table})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].at < hits[j].at })
	out := make([]usGroupBSQL, 0, len(hits))
	for _, h := range hits {
		out = append(out, usGroupBSQL{Op: h.op, Table: h.table, Text: usGroupBTruncate(normalized[h.at:], usGroupBPrefixBytes)})
	}
	return out
}

// usGroupBScanGo reads every server Go source, production and test, including files
// behind inactive build tags, and returns each statement site against a group B table.
func usGroupBScanGo(t *testing.T, root string) []usGroupBSite {
	t.Helper()
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	repositoryRoot := filepath.Dir(absoluteRoot)
	var found []usGroupBSite
	err = filepath.WalkDir(absoluteRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		files := token.NewFileSet()
		file, err := parser.ParseFile(files, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		relative, err := filepath.Rel(repositoryRoot, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == usGroupBSelf {
			return nil
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			text, err := strconv.Unquote(literal.Value)
			if err != nil {
				return true
			}
			for _, statement := range usGroupBStatements(text) {
				found = append(found, usGroupBSite{
					File:   relative,
					Op:     statement.Op,
					Table:  statement.Table,
					Class:  usGroupBClassOf(relative),
					Prefix: statement.Text,
					Line:   files.Position(literal.Pos()).Line,
				})
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestUSGroupBProducerInventory(t *testing.T) {
	found := usGroupBScanGo(t, usGroupBServerRoot())
	expected := make(map[string]usGroupBProducer, len(usGroupBProducers))
	pending := make(map[string]usGroupBProducer)
	for _, producer := range usGroupBProducers {
		if producer.PendingPR != "" {
			if producer.Op != "" || producer.Table != "" || producer.Prefix != "" {
				t.Fatalf("%s: pending entries match by file; leave op, table and prefix empty", producer.File)
			}
			if _, duplicate := pending[producer.File]; duplicate {
				t.Fatalf("duplicate pending census entry for %s", producer.File)
			}
			if producer.Class != usGroupBV1Native || producer.Fixture != "P05" {
				t.Fatalf("%s: pending fixtures are v1-native P05 entries", producer.File)
			}
			if producer.Note == "" || producer.PendingPR[0] != '#' {
				t.Fatalf("%s: pending entry needs a note and a PR number", producer.File)
			}
			pending[producer.File] = producer
			continue
		}
		key := usGroupBKey(producer.File, producer.Op, producer.Table, producer.Prefix)
		if _, duplicate := expected[key]; duplicate {
			t.Fatalf("duplicate census entry for %s %s %s", producer.File, producer.Op, producer.Table)
		}
		switch producer.Class {
		case usGroupBV1Native, usGroupBLegacy:
		default:
			t.Fatalf("%s: unknown class %q", producer.File, producer.Class)
		}
		if producer.Class != usGroupBClassOf(producer.File) {
			t.Fatalf("%s: class %q does not match its schema opener", producer.File, producer.Class)
		}
		switch producer.Purpose {
		case usGroupBOrdinary, usGroupBCredential, usGroupBMixed:
		default:
			t.Fatalf("%s: purpose must be ordinary, credential or ordinary+credential; got %q", producer.File, producer.Purpose)
		}
		if producer.Note == "" {
			t.Fatalf("%s: missing note", producer.File)
		}
		if producer.Class == usGroupBV1Native && producer.Fixture != "P05" && producer.Fixture != "P06" {
			t.Fatalf("%s: every v1-native fixture belongs to P05 or P06; got %q", producer.File, producer.Fixture)
		}
		if producer.Class == usGroupBLegacy && producer.Fixture != "" {
			t.Fatalf("%s: legacy site must not claim a fixture task", producer.File)
		}
		expected[key] = producer
	}

	seen := make(map[string][]usGroupBSite, len(expected))
	pendingSeen := make(map[string]int, len(pending))
	for _, site := range found {
		key := usGroupBKey(site.File, site.Op, site.Table, site.Prefix)
		if _, listed := expected[key]; listed {
			seen[key] = append(seen[key], site)
			continue
		}
		if _, waiting := pending[site.File]; waiting {
			pendingSeen[site.File]++
			continue
		}
		t.Errorf("unlisted %s producer at %s:%d (%s %s); add it to usGroupBProducers with class, purpose and fixture task\nSQL: %s",
			site.Class, site.File, site.Line, site.Op, site.Table, site.Prefix)
	}
	for file, producer := range pending {
		t.Logf("pending producer file %s (%s): %d target-table sites at this head", file, producer.PendingPR, pendingSeen[file])
	}
	for _, producer := range usGroupBProducers {
		if producer.PendingPR != "" {
			continue
		}
		key := usGroupBKey(producer.File, producer.Op, producer.Table, producer.Prefix)
		sites := seen[key]
		switch len(sites) {
		case 0:
			t.Errorf("census entry no longer matches a source statement: %s %s %s\nSQL: %s\nUpdate the entry to the current statement.",
				producer.File, producer.Op, producer.Table, producer.Prefix)
		case 1:
		default:
			lines := make([]string, len(sites))
			for i, site := range sites {
				lines[i] = fmt.Sprint(site.Line)
			}
			t.Errorf("%s %s %s matches %d sites (lines %s); give each site its own entry",
				producer.File, producer.Op, producer.Table, len(sites), strings.Join(lines, ", "))
		}
	}

	v1Native, legacy := 0, 0
	for _, site := range found {
		if site.Class == usGroupBV1Native {
			v1Native++
		} else {
			legacy++
		}
	}
	t.Logf("group B producer census: %d Go sites (%d v1-native, %d legacy)", len(found), v1Native, legacy)
}

// TestUSGroupBProducerInventoryOtherSources pins the two non-Go sites: the legacy
// baseline-schema script is unchanged, and the generated capture names a listed
// producer rather than acting as one.
func TestUSGroupBProducerInventoryOtherSources(t *testing.T) {
	root := usGroupBServerRoot()
	scriptPath := filepath.Join(root, "scripts", "check_p10_sqlite.py")
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(script), "INSERT INTO authorization_session_families "); got != 1 {
		t.Errorf("baseline-schema script has %d family insert sites, want 1; the legacy script stays unchanged", got)
	}
	for _, statement := range []string{
		"INSERT INTO api_credentials",
		"DELETE FROM api_credentials",
		"INSERT INTO identity_devices",
		"DELETE FROM identity_devices",
		"DELETE FROM authorization_session_families",
	} {
		if strings.Contains(string(script), statement) {
			t.Errorf("baseline-schema script gained %q; the legacy script stays unchanged", statement)
		}
	}
	declaredScript := false
	declaredCapture := false
	for _, other := range usGroupBOtherSources {
		switch {
		case other.File == "server/scripts/check_p10_sqlite.py":
			declaredScript = other.Op == "insert" && other.Table == "authorization_session_families" && other.Class == usGroupBLegacy
		case other.File == "server/internal/store/writer/testdata/wc-progress-composition.json":
			declaredCapture = other.Class == usGroupBGeneratedEvidence
		}
	}
	if !declaredScript {
		t.Error("scripts/check_p10_sqlite.py is not declared as the legacy baseline fixture")
	}
	if !declaredCapture {
		t.Error("the WC progress capture is not declared as generated evidence")
	}

	raw, err := os.ReadFile(filepath.Join("testdata", "wc-progress-composition.json"))
	if err != nil {
		t.Fatal(err)
	}
	var capture struct {
		Manifest []struct {
			ID        string   `json:"id"`
			SQL       string   `json:"sql"`
			CallSites []string `json:"callSites"`
		} `json:"manifest"`
	}
	if err := json.Unmarshal(raw, &capture); err != nil {
		t.Fatal(err)
	}
	producers := make(map[string]bool)
	for _, producer := range usGroupBProducers {
		if producer.Class == usGroupBV1Native {
			producers[producer.File] = true
		}
	}
	writers := 0
	for _, entry := range capture.Manifest {
		if len(usGroupBStatements(entry.SQL)) == 0 {
			continue
		}
		writers++
		if len(entry.CallSites) == 0 {
			t.Errorf("capture %s has no call site", entry.ID)
		}
		for _, callSite := range entry.CallSites {
			file := "server/internal/" + callSite
			if !producers[file] {
				t.Errorf("capture %s points at %s, which is not a listed v1-native producer", entry.ID, file)
			}
		}
	}
	if writers != 1 {
		t.Errorf("capture holds %d target-table writers, want the single wc.fixture.family family insert", writers)
	}
}

// Schema SHA guards live outside the producer fixtures. The embed owner (P08) applies
// the hash-only guard edits after P05 and P06, so a producer task must not touch these
// files. The accepted prefixes cover the retained schema012+013 pin (9d9aa39d), the
// adopted CHANGE-014 schema carried by PR #1803 (782904d7), and the group B proposal
// after the embed (44411dfe), so the census stays true across both embeds.
var usGroupBSchemaSHAPrefixes = []string{"9d9aa39d", "782904d7", "44411dfe"}

var usGroupBSchemaGuardReference = regexp.MustCompile(`progressCrashSchema|requires schema[0-9]{3}`)

// usGroupBSchemaGuardSelf are this census's own files: they quote the SHAs to document
// them and are not guard sites.
var usGroupBSchemaGuardSelf = map[string]bool{
	"server/internal/store/writer/us_group_b_producer_inventory_test.go": true,
	"server/internal/store/writer/US_GROUP_B_PRODUCER_INVENTORY.md":      true,
}

type usGroupBSchemaGuard struct {
	File       string
	Literals   int
	References bool
	Guards     string
	HashOnly   bool
}

var usGroupBSchemaGuards = []usGroupBSchemaGuard{
	{File: "server/internal/store/schema/schema.go", Literals: 1, Guards: "embedded schema.sql SHA256 constant", HashOnly: true},
	{File: "server/internal/store/schema/SHA256SUMS", Literals: 1, Guards: "schema.sql checksum line", HashOnly: true},
	{File: "server/internal/store/schema/INT-LARGE-INDEXES.json", Literals: 1, Guards: "ddl_sha256 in the large-index contract; the index set also moves with group B foreign keys", HashOnly: false},
	{File: "server/internal/store/schema/README.md", Literals: 1, Guards: "prose pin of the embedded schema", HashOnly: false},
	{File: "server/internal/store/writer/QUALIFICATION_POPULATION.md", Literals: 1, Guards: "canonical-movies-v1 shape pin", HashOnly: false},
	{File: "server/internal/store/writer/qualification_population_seed_test.go", Literals: 1, Guards: "qualification population shape guard", HashOnly: true},
	{File: "server/internal/store/writer/scan_run_lifecycle_plan_test.go", Literals: 1, Guards: "run/lifecycle plan capture guard", HashOnly: true},
	{File: "server/internal/store/writer/scan_fresh_plan_test.go", Literals: 1, Guards: "scan fresh plan capture guard", HashOnly: true},
	{File: "server/internal/store/writer/crash_progress_linux_test.go", Literals: 1, References: true, Guards: "progressCrashSchema010 constant and its registry message", HashOnly: false},
	{File: "server/internal/store/writer/crash_reclaim_linux_test.go", References: true, Guards: "reclaim crash registry uses progressCrashSchema010 and its schema message", HashOnly: false},
	{File: "server/internal/store/writer/crash_security_linux_test.go", References: true, Guards: "security crash registry uses progressCrashSchema010 and its schema message", HashOnly: false},
	{File: "server/internal/store/stmt/plan/testdata/canonical.json", Literals: 1, Guards: "canonical plan evidence sha256", HashOnly: false},
	{File: "server/internal/store/stmt/plan/testdata/issuer-source-octets.json", Literals: 1, Guards: "issuer source octet evidence sha256", HashOnly: false},
	{File: "server/internal/store/stmt/plan/testdata/issuer-library-root-octets.json", Literals: 1, Guards: "issuer library-root octet evidence sha256", HashOnly: false},
	{File: "server/internal/store/stmt/plan/testdata/n1-record-octets.json", Literals: 1, Guards: "N1 record octet evidence sha256", HashOnly: false},
	{File: "server/internal/store/stmt/plan/testdata/ia-audio-octets.json", Literals: 1, Guards: "IA audio octet evidence sha256", HashOnly: false},
}

type usGroupBSchemaGuardFound struct {
	Literals   int
	References bool
}

// usGroupBSchemaGuardScan reads every text file under server and docs and returns each
// site that carries a known schema SHA or a writer crash schema guard reference.
func usGroupBSchemaGuardScan(t *testing.T, root string) map[string]usGroupBSchemaGuardFound {
	t.Helper()
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	repositoryRoot := filepath.Dir(absoluteRoot)
	found := make(map[string]usGroupBSchemaGuardFound)
	for _, directory := range []string{"server", "docs"} {
		err := filepath.WalkDir(filepath.Join(repositoryRoot, directory), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			relative, err := filepath.Rel(repositoryRoot, path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			if usGroupBSchemaGuardSelf[relative] {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Size() > 8<<20 {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if bytes.IndexByte(data, 0) >= 0 {
				return nil
			}
			text := string(data)
			literals := 0
			for _, prefix := range usGroupBSchemaSHAPrefixes {
				literals += strings.Count(text, prefix)
			}
			references := strings.HasSuffix(relative, ".go") && usGroupBSchemaGuardReference.MatchString(text)
			if literals == 0 && !references {
				return nil
			}
			found[relative] = usGroupBSchemaGuardFound{Literals: literals, References: references}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return found
}

// TestUSGroupBProducerInventorySchemaGuards lists the schema SHA guard sites without
// editing them. A new unlisted guard carrying the 014 SHA (or the retained pin) fails,
// so P08 owns the embed edits in one place and producer tasks stay out of these files.
func TestUSGroupBProducerInventorySchemaGuards(t *testing.T) {
	found := usGroupBSchemaGuardScan(t, usGroupBServerRoot())
	expected := make(map[string]usGroupBSchemaGuard, len(usGroupBSchemaGuards))
	for _, guard := range usGroupBSchemaGuards {
		if _, duplicate := expected[guard.File]; duplicate {
			t.Fatalf("duplicate schema guard entry for %s", guard.File)
		}
		if guard.Guards == "" {
			t.Fatalf("%s: describe what the guard protects", guard.File)
		}
		expected[guard.File] = guard
	}
	for file, site := range found {
		want, listed := expected[file]
		if !listed {
			t.Errorf("unlisted schema SHA guard %s (%d schema literals, writer guard reference=%t); add it to usGroupBSchemaGuards and the P08 section of US_GROUP_B_PRODUCER_INVENTORY.md; do not edit it in a producer task",
				file, site.Literals, site.References)
			continue
		}
		if site.Literals != want.Literals {
			t.Errorf("%s: %d schema literals, census says %d", file, site.Literals, want.Literals)
		}
		if site.References != want.References {
			t.Errorf("%s: writer guard reference=%t, census says %t", file, site.References, want.References)
		}
	}
	for file := range expected {
		if _, ok := found[file]; !ok {
			t.Errorf("listed schema guard %s no longer carries a known schema SHA or writer guard reference; update the P08 section", file)
		}
	}
}
