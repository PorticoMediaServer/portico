import type {LibraryContentApi} from '@core/library-content.ts';
import {parseEnvelope, parseChannelMapPage, parseLiveSourceDocument, type ChannelMapEntry} from '@core/administration.ts';
import {channelRequestID} from '@core/live-source-draft.ts';
import {OwnerStore, errorFacts, terminal} from '../../live/owner/lifetime';
export type ChannelOverride = Readonly<{number: string; guideChannelId: string; hidden: boolean}>;
type Change = ChannelOverride & {channelId: string};
type MapSnapshot = Readonly<{loading: boolean; saving: boolean; rows: readonly ChannelMapEntry[]; edits: Readonly<Record<string, ChannelOverride>>; cursor: string; generation?: string; error?: unknown; partial?: {saved: number; total: number}; uncertain?: boolean}>;
const empty: MapSnapshot = Object.freeze({loading: false, saving: false, rows: Object.freeze([]), edits: Object.freeze({}), cursor: ''});
/** Bounded reads and sequential <=200-row writes. An uncertain batch keeps
 * its original revision/body/id; editing cannot overtake that reconciliation. */
export class ChannelMapController extends OwnerStore<MapSnapshot> {
  private queue?: {remaining: readonly Change[]; pending?: Readonly<{expectedRevision: number; operationId: string; overrides: readonly Change[]}>; revision: number; saved: number; total: number};
  constructor(private api: LibraryContentApi, private serverId: string, readonly sourceId: string, current: () => boolean, reportError: (error: unknown) => void) {
    super(empty, current, reportError);if (!/^[a-f0-9]{48}$/.test(sourceId)) throw new Error('live_source_identity_invalid');
  }
  private path(part: string) {return `/v1/admin/live-sources/${encodeURIComponent(this.sourceId)}/${part}`;}
  private page(raw: unknown) {
    const page = parseEnvelope(raw, this.serverId, parseChannelMapPage).result;
    if (page.sourceId !== this.sourceId || !/^[a-f0-9]{48}$/.test(page.generation)) throw {code: 'invalid_response'};return page;
  }
  async load(more = false) {
    if (!this.isActive() || this.snapshot.loading || this.snapshot.saving || this.queue || more && !this.snapshot.cursor) return;
    const head = this.snapshot, attempt = this.begin(), cursor = more ? head.cursor : '';
    this.publish({...head, loading: true, error: undefined});if (!this.valid(attempt)) return;
    try {
      const raw = await this.api.request(this.path('channel-map') + '?' + new URLSearchParams({limit: '100', cursor}), 'GET', undefined, attempt.abort.signal);
      if (!this.valid(attempt)) return;const page = this.page(raw);
      if (head.generation && head.generation !== page.generation && (more || Object.keys(head.edits).length)) throw {code: 'revision_mismatch'};
      const rows = more ? [...head.rows, ...page.items.filter(row => !head.rows.some(prior => prior.channelId === row.channelId))] : page.items;
      this.publish({...this.snapshot, loading: false, rows: Object.freeze(rows), generation: page.generation, cursor: page.nextCursor});
    } catch (error) {if (this.valid(attempt)) this.fail(error, {...this.snapshot, loading: false, error: errorFacts(error)});}
  }
  value(row: ChannelMapEntry): ChannelOverride {return this.snapshot.edits[row.channelId] ?? {number: row.overridden ? row.number : '', guideChannelId: row.guideChannelId, hidden: row.hidden};}
  edit(row: ChannelMapEntry, patch: Partial<ChannelOverride>) {
    if (!this.isActive() || this.snapshot.saving || this.queue || !this.snapshot.rows.includes(row)) return;
    const value = {...this.value(row), ...patch};
    if (!/^[0-9.]{0,12}$/.test(value.number) || value.guideChannelId.length > 200 || /[\x00-\x1f\x7f]/.test(value.guideChannelId)) return;
    const original = {number: row.overridden ? row.number : '', guideChannelId: row.guideChannelId, hidden: row.hidden}, edits = {...this.snapshot.edits};
    if (JSON.stringify(value) === JSON.stringify(original)) delete edits[row.channelId];else edits[row.channelId] = Object.freeze(value);
    this.publish({...this.snapshot, edits: Object.freeze(edits), error: undefined, partial: undefined});
  }
  async save() {
    if (!this.isActive() || this.snapshot.saving || this.snapshot.loading || !Object.keys(this.snapshot.edits).length) return false;
    const attempt = this.begin();this.publish({...this.snapshot, saving: true, error: undefined});
    try {
      if (!this.valid(attempt)) return false;
      if (!this.queue) {
        const rawPage = await this.api.request(this.path('channel-map') + '?limit=1', 'GET', undefined, attempt.abort.signal);if (!this.valid(attempt)) return false;
        if (this.page(rawPage).generation !== this.snapshot.generation) throw {code: 'revision_mismatch'};
        const raw = await this.api.request(this.path('configuration'), 'GET', undefined, attempt.abort.signal);if (!this.valid(attempt)) return false;
        const doc = parseEnvelope(raw, this.serverId, parseLiveSourceDocument).result;if (doc.sourceId !== this.sourceId) throw {code: 'invalid_response'};
        const remaining = Object.entries(this.snapshot.edits).map(([channelId, value]) => Object.freeze({channelId, ...value}));
        this.queue = {remaining: Object.freeze(remaining), revision: doc.revision, saved: 0, total: remaining.length};
      }
      while (this.queue.remaining.length) {
        const queue = this.queue;
        queue.pending ??= Object.freeze({expectedRevision: queue.revision, operationId: channelRequestID(), overrides: Object.freeze(queue.remaining.slice(0, 200))});
        if (!this.valid(attempt)) return false;
        const raw = await this.api.request(this.path('channel-map'), 'PUT', queue.pending, attempt.abort.signal);if (!this.valid(attempt)) return false;
        const page = this.page(raw);if (page.generation !== this.snapshot.generation) throw {code: 'revision_mismatch'};
        const edits = {...this.snapshot.edits};for (const row of queue.pending.overrides) delete edits[row.channelId];
        queue.saved += queue.pending.overrides.length;queue.remaining = queue.remaining.slice(queue.pending.overrides.length);queue.revision++;queue.pending = undefined;
        // The receipt is the server's actual first page after this batch.
        // Drop appended pages rather than showing stale derived numbering.
        this.publish({...this.snapshot, edits: Object.freeze(edits), rows: page.items, cursor: page.nextCursor, partial: {saved: queue.saved, total: queue.total}, uncertain: false});
        if (!this.valid(attempt)) return false;
      }
      this.queue = undefined;this.publish({...this.snapshot, saving: false, uncertain: false});return this.valid(attempt);
    } catch (error) {
      if (this.valid(attempt)) {
        const partial = this.queue && {saved: this.queue.saved, total: this.queue.total};
        // A definite conflict did not apply this batch; a transport/receipt
        // failure keeps the original body until the receipt can be reconciled.
        if (!terminal(error) && errorFacts(error).status === 409) this.queue = undefined;
        this.fail(error, {...this.snapshot, saving: false, error: errorFacts(error), partial, uncertain: !!this.queue?.pending});
      }
      return false;
    }
  }
  override dispose() {this.queue = undefined;super.dispose();}
}
