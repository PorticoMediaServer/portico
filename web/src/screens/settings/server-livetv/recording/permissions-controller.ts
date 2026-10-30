import type {LibraryContentApi} from '@core/library-content.ts';
import {parseRecordingOwners, parseRecordingGrants, recordingOwnerKey, type RecordingOwnerChoice, type RecordingGrant} from '@core/server-admin/recording-policy.ts';
import {OwnerStore, errorFacts, terminal} from '../../../live/owner/lifetime';
import {envelope, text} from '../../../live/owner/checked';
type Change = Readonly<{enabled: boolean}>;
type State = Readonly<{loading: boolean; saving: boolean; owners: readonly RecordingOwnerChoice[]; grants: readonly RecordingGrant[]; cursor: string; edits: Readonly<Record<string, Change>>; error?: unknown; uncertain?: boolean}>;
const empty: State = Object.freeze({loading: false, saving: false, owners: Object.freeze([]), grants: Object.freeze([]), cursor: '', edits: Object.freeze({})});
const checkedOwner = (v: RecordingGrant['owner']) => Object.freeze({authority: v.authority, accountId: text(v.accountId, 256), profileId: text(v.profileId, 256)});
const same = (grant: RecordingGrant, body: RecordingGrant) => recordingOwnerKey(grant.owner) === recordingOwnerKey(body.owner) && grant.enabled === body.enabled && grant.revision > body.revision;
/** Who can record: each current profile's own Recordings switch, fenced by its
 * revision. The permission list names only the profiles that may record. */
export class RecordingPermissionsController extends OwnerStore<State> {
  private pending?: Readonly<RecordingGrant>;
  constructor(private api: LibraryContentApi, private serverId: string, current: () => boolean, reportError: (error: unknown) => void) {super(empty, current, reportError);}
  private grants(raw: unknown) {
    const result = envelope(raw, this.serverId).result;if (!Array.isArray(result) || result.length > 10000) throw {code: 'invalid_response'};
    const grants = parseRecordingGrants(result).map(grant => Object.freeze({...grant, owner: checkedOwner(grant.owner)}));
    if (new Set(grants.map(grant => recordingOwnerKey(grant.owner))).size !== grants.length) throw {code: 'invalid_response'};
    return Object.freeze(grants);
  }
  async load(more = false) {
    if (!this.isActive() || this.snapshot.loading || this.snapshot.saving || this.pending || Object.keys(this.snapshot.edits).length || more && !this.snapshot.cursor) return;
    const attempt = this.begin(), head = this.snapshot;
    this.publish({...head, loading: true, error: undefined});if (!this.valid(attempt)) return;
    try {
      const raw = await this.api.request('/v1/admin/dvr/recording-owners?' + new URLSearchParams({limit: '100', cursor: more ? head.cursor : ''}), 'GET', undefined, attempt.abort.signal);if (!this.valid(attempt)) return;
      const result = envelope(raw, this.serverId).result;
      const page = parseRecordingOwners(result);if (page.items.length > 100 || page.nextCursor.length > 4096 || more && page.nextCursor && page.nextCursor === head.cursor) throw {code: 'invalid_response'};
      const items = page.items.map(row => Object.freeze({owner: checkedOwner(row.owner), accountName: text(row.accountName, 512), profileName: text(row.profileName, 512), canRecord: row.canRecord, revision: row.revision}));
      const owners = more ? [...head.owners, ...items.filter(row => !head.owners.some(prior => recordingOwnerKey(prior.owner) === recordingOwnerKey(row.owner)))] : items;
      if (!this.valid(attempt)) return;
      const grants = this.grants(await this.api.request('/v1/admin/dvr/recording-permissions', 'GET', undefined, attempt.abort.signal));if (!this.valid(attempt)) return;
      this.publish({loading: false, saving: false, owners: Object.freeze(owners), grants, cursor: page.nextCursor, edits: Object.freeze({})});
    } catch (error) {if (this.valid(attempt)) this.fail(error, {...this.snapshot, loading: false, error: errorFacts(error)});}
  }
  edit(owner: RecordingGrant['owner'], patch: Partial<Change>) {
    if (!this.isActive() || this.snapshot.loading || this.snapshot.saving || this.pending) return;
    const key = recordingOwnerKey(owner), grant = this.snapshot.grants.find(row => recordingOwnerKey(row.owner) === key);
    const present = this.snapshot.owners.some(row => recordingOwnerKey(row.owner) === key);
    if (!present && !grant) return;
    const candidate = this.snapshot.owners.find(row => recordingOwnerKey(row.owner) === key);
    const original = {enabled: grant?.enabled ?? candidate?.canRecord ?? false};
    const change = {...(this.snapshot.edits[key] ?? original), ...patch};if (!present && change.enabled) return;
    const edits = {...this.snapshot.edits};if (JSON.stringify(change) === JSON.stringify(original)) delete edits[key];else edits[key] = Object.freeze(change);
    this.publish({...this.snapshot, edits: Object.freeze(edits), error: undefined});
    void this.save();
  }
  discard() {if (this.isActive() && !this.snapshot.saving && !this.pending) this.publish({...this.snapshot, edits: Object.freeze({}), error: undefined});}
  async save() {
    if (!this.isActive() || this.snapshot.loading || this.snapshot.saving || !Object.keys(this.snapshot.edits).length) return;
    const attempt = this.begin();this.publish({...this.snapshot, saving: true, error: undefined});if (!this.valid(attempt)) return;
    try {
      for (const [key, change] of Object.entries(this.snapshot.edits)) {
        const grant = this.snapshot.grants.find(row => recordingOwnerKey(row.owner) === key), candidate = this.snapshot.owners.find(row => recordingOwnerKey(row.owner) === key);
        if (!candidate && (change.enabled || !grant)) throw {code: 'not_permitted'};
        const body = this.pending ?? Object.freeze({owner: checkedOwner((candidate ?? grant)!.owner), ...change, revision: grant?.revision ?? candidate!.revision});
        if (recordingOwnerKey(body.owner) !== key) throw {code: 'revision_mismatch'};
        this.pending = body;
        let receipt: RecordingGrant;
        try {
          if (!this.valid(attempt)) return;
          const raw = await this.api.request('/v1/admin/dvr/recording-permissions', 'PUT', body, attempt.abort.signal);if (!this.valid(attempt)) return;
          receipt = this.grants({protocolVersion: '1.0', serverId: this.serverId, result: [envelope(raw, this.serverId).result]})[0];
          if (!receipt || !same(receipt, body)) throw {code: 'invalid_response'};
        } catch (error) {
          if (!this.valid(attempt) || terminal(error)) throw error;
          const raw = await this.api.request('/v1/admin/dvr/recording-permissions', 'GET', undefined, attempt.abort.signal);if (!this.valid(attempt)) return;
          // The list names only profiles that may record, so a switch turned off
          // landed exactly when the profile is no longer in it.
          const listed = this.grants(raw).find(row => recordingOwnerKey(row.owner) === key);
          if (body.enabled ? !listed || !same(listed, body) : !!listed) throw error;
          receipt = listed ?? Object.freeze({owner: body.owner, enabled: false, revision: body.revision + 1});
        }
        this.pending = undefined;
        const edits = {...this.snapshot.edits};delete edits[key];
        const grants = [...this.snapshot.grants.filter(row => recordingOwnerKey(row.owner) !== key), ...(receipt.enabled ? [receipt] : [])];
        const owners = this.snapshot.owners.map(row => recordingOwnerKey(row.owner) === key ? Object.freeze({...row, canRecord: receipt.enabled, revision: receipt.revision}) : row);
        this.publish({...this.snapshot, owners: Object.freeze(owners), grants: Object.freeze(grants), edits: Object.freeze(edits), uncertain: false});if (!this.valid(attempt)) return;
      }
      this.publish({...this.snapshot, saving: false});
    } catch (error) {if (this.valid(attempt)) this.fail(error, {...this.snapshot, saving: false, uncertain: !!this.pending, error: errorFacts(error)});}
  }
  override dispose() {this.pending = undefined;super.dispose();}
}
