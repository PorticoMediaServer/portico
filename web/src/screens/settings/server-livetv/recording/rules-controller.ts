import type {LibraryContentApi} from '@core/library-content.ts';
import {parseChannelGuide} from '@core/channel-guide.ts';
import {recordingAllowed, recordingGuideRoute, recordingOwnerKey, recordingRulePayload, recordingSeries, type RecordingOwner, type RecordingAnchor, type RecordingSeries} from '@core/server-admin/recording-policy.ts';
import type {RecordingPermissionsController} from './permissions-controller';
import {channelRequestID} from '@core/live-source-draft.ts';
import {OwnerStore, errorFacts} from '../../../live/owner/lifetime';
import {envelope, record, text} from '../../../live/owner/checked';
type Keep = Readonly<{mode: 'keep-all' | 'keep-count' | 'keep-days'; keepCount: number; keepDays: number; keepUntilWatched: boolean}>;
export type RuleOptions = Readonly<{prePaddingMs: number | null; postPaddingMs: number | null; keepPolicy: Keep | null; newEpisodesOnly: boolean; priority: number; folderTemplate: string}>;
export type OwnerRule = Readonly<{id: string; revision: number; status: string; owner: RecordingOwner; kind: string; name: string; match: string; sourceId: string; enabled: boolean; options: RuleOptions}>;
export type RuleDraft = Omit<OwnerRule, 'id' | 'revision' | 'status'> & {id?: string; revision?: number; anchor?: RecordingAnchor};
type State = Readonly<{loading: boolean; saving: boolean; rules: readonly OwnerRule[]; cursor: string; error?: unknown; draft?: RuleDraft; series: readonly RecordingSeries[]; guideLoading: boolean; guideCursor: string; day: string; sourceId: string; uncertain?: boolean}>;
const num = (v: unknown, min: number, max: number) => {if (typeof v !== 'number' || !Number.isSafeInteger(v) || v < min || v > max) throw {code: 'invalid_response'};return v;};
const flag = (v: unknown) => {if (typeof v !== 'boolean') throw {code: 'invalid_response'};return v;};
function parseRule(raw: unknown): OwnerRule {
  const v = record(raw), owner = record(v.owner), o = record(v.options), kind = text(v.kind, 32);
  const authority = owner.authority;if (authority !== 'local' && authority !== 'hosted' && authority !== '') throw {code: 'invalid_response'};
  const keep = o.keepPolicy === null ? null : record(o.keepPolicy);
  if (keep && !['keep-all', 'keep-count', 'keep-days'].includes(String(keep.mode))) throw {code: 'invalid_response'};
  const id = text(v.id, 128);if (!id) throw {code: 'invalid_response'};
  return Object.freeze({id, revision: num(v.revision, 1, Number.MAX_SAFE_INTEGER), status: text(v.status ?? '', 128), kind,
    owner: Object.freeze({authority: authority as RecordingOwner['authority'], accountId: text(owner.accountId, 256), profileId: text(owner.profileId, 256)}), name: text(v.name, 160), match: text(v.match, 512), sourceId: text(v.sourceId, 128), enabled: flag(v.enabled),
    options: Object.freeze({prePaddingMs: o.prePaddingMs === null ? null : num(o.prePaddingMs, 0, 21600000), postPaddingMs: o.postPaddingMs === null ? null : num(o.postPaddingMs, 0, 21600000),
      keepPolicy: keep ? Object.freeze({mode: keep.mode as Keep['mode'], keepCount: num(keep.keepCount, 0, 100000), keepDays: num(keep.keepDays, 0, 3650), keepUntilWatched: flag(keep.keepUntilWatched)}) : null,
      newEpisodesOnly: flag(o.newEpisodesOnly), priority: num(o.priority, -1000, 1000), folderTemplate: text(o.folderTemplate, 400)})});
}
/** Null padding/keep asks the producer to snapshot defaults, so its receipt
 * contains resolved values. Explicit choices must still match exactly. */
const matchesOptions = (saved: RuleOptions, draft: RuleOptions) =>
  (draft.prePaddingMs === null || saved.prePaddingMs === draft.prePaddingMs) &&
  (draft.postPaddingMs === null || saved.postPaddingMs === draft.postPaddingMs) &&
  (draft.keepPolicy === null || JSON.stringify(saved.keepPolicy) === JSON.stringify(draft.keepPolicy)) &&
  saved.newEpisodesOnly === draft.newEpisodesOnly && saved.priority === draft.priority && saved.folderTemplate === draft.folderTemplate;
export const needsRuleOwner = (rule: OwnerRule) => rule.status === 'needs-owner-and-guide-selection' || !rule.owner.accountId || !rule.owner.profileId || !['series', 'title'].includes(rule.kind);
const empty: State = Object.freeze({loading: false, saving: false, guideLoading: false, rules: Object.freeze([]), cursor: '', series: Object.freeze([]), guideCursor: '', day: '', sourceId: ''});
export class RecordingRulesController extends OwnerStore<State> {
  private ids = new Map<string, string>();
  private pending?: {key: string; path: string; method: string; body?: unknown};
  constructor(private api: LibraryContentApi, private serverId: string, private permissions: RecordingPermissionsController, current: () => boolean, reportError: (error: unknown) => void) {super(empty, current, reportError);}
  async load(more = false) {
    if (!this.isActive() || this.snapshot.loading || this.snapshot.saving || this.snapshot.draft || this.pending || more && !this.snapshot.cursor) return;
    const attempt = this.begin(), head = this.snapshot;this.publish({...head, loading: true, error: undefined});if (!this.valid(attempt)) return;
    try {
      const raw = envelope(await this.api.request('/v1/admin/dvr/recording-groups?' + new URLSearchParams({limit: '100', cursor: more ? head.cursor : ''}), 'GET', undefined, attempt.abort.signal), this.serverId).result;
      if (!this.valid(attempt)) return;const page = record(raw);if (!Array.isArray(page.items) || page.items.length > 100) throw {code: 'invalid_response'};
      const rows = page.items.map(parseRule);if (more && head.rules.length + rows.length > 10000 || new Set(rows.map(row => row.id)).size !== rows.length) throw {code: 'invalid_response'};
      const rules = more ? [...head.rules, ...rows.filter(row => !head.rules.some(prior => prior.id === row.id))] : rows;
      const cursor = text(page.nextCursor ?? '', 4096);if (more && cursor && cursor === head.cursor) throw {code: 'invalid_response'};
      this.publish({...this.snapshot, loading: false, rules: Object.freeze(rules), cursor});
    } catch (error) {if (this.valid(attempt)) this.fail(error, {...this.snapshot, loading: false, error: errorFacts(error)});}
  }
  open(rule?: OwnerRule) {
    if (!this.isActive() || this.snapshot.draft || this.snapshot.saving || this.pending || rule && !this.snapshot.rules.includes(rule)) return;
    this.invalidate();const draft: RuleDraft = rule && !needsRuleOwner(rule) ? {...rule} : {owner: {authority: 'local', accountId: '', profileId: ''}, kind: 'series', name: rule?.name ?? '', match: '', sourceId: '', enabled: true,
      options: {prePaddingMs: null, postPaddingMs: null, keepPolicy: null, newEpisodesOnly: false, priority: 0, folderTemplate: ''}};
    this.publish({...this.snapshot, loading: false, guideLoading: false, draft, error: undefined, series: [], day: new Date().toISOString().slice(0, 10), sourceId: rule?.sourceId ?? '', guideCursor: ''});
  }
  async toggle(rule: OwnerRule) {
    if (!this.isActive() || this.snapshot.draft || this.snapshot.saving || this.pending || needsRuleOwner(rule) || !this.snapshot.rules.includes(rule)) return false;
    this.open(rule);
    const draft = this.getSnapshot().draft;
    if (!draft || draft.id !== rule.id) return false;
    this.edit(draft, {enabled: !rule.enabled});
    const changed = this.getSnapshot().draft;
    return changed ? this.save(changed) : false;
  }
  close(expected: RuleDraft) {if (this.snapshot.draft !== expected) return;if (this.isActive() && !this.snapshot.saving && !this.pending) {this.invalidate();this.publish({...this.snapshot, draft: undefined, guideLoading: false, series: []});}}
  edit(expected: RuleDraft, patch: Partial<Pick<RuleDraft, 'name' | 'owner' | 'options' | 'enabled'>>) {if (this.snapshot.draft !== expected) return;if (this.isActive() && !this.snapshot.saving && !this.pending && this.snapshot.draft) this.publish({...this.snapshot, draft: {...this.snapshot.draft, ...patch}, error: undefined});}
  choose(expected: RuleDraft, series: RecordingSeries) {
    if (this.snapshot.draft !== expected) return;
    if (!this.isActive() || this.snapshot.saving || this.pending || !this.snapshot.draft || !this.snapshot.series.includes(series)) return;
    this.publish({...this.snapshot, draft: {...this.snapshot.draft, name: this.snapshot.draft.name || series.title, kind: series.seriesId ? 'series' : 'title', match: series.seriesId || series.title, sourceId: series.anchor.sourceId, anchor: series.anchor, enabled: series.recordAvailable, options: {...this.snapshot.draft.options, newEpisodesOnly: false}}});
  }
  async guide(day: string, sourceId: string, cursor = '') {
    if (!this.isActive() || this.snapshot.saving || this.pending || !this.snapshot.draft || this.snapshot.draft.id) return;
    const route = recordingGuideRoute(day, sourceId), attempt = this.begin();
    this.publish({...this.snapshot, day, sourceId, guideCursor: '', guideLoading: !!route, series: [], draft: {...this.snapshot.draft, match: '', sourceId: '', anchor: undefined}, error: route ? undefined : {code: 'invalid_request'}});
    if (!route || !this.valid(attempt)) return;
    try {
      const params = new URLSearchParams({kind: route.kind, start: route.start, end: route.end, timezone: route.timezone, search: route.search, sourceId: route.sourceId, limit: '30', cursor});
      const raw = await this.api.request('/v1/guide?' + params, 'GET', undefined, attempt.abort.signal);if (!this.valid(attempt)) return;
      const guide = parseChannelGuide(raw, this.serverId, route);
      if (cursor && guide.nextCursor === cursor) throw {code: 'invalid_response'};
      this.publish({...this.snapshot, guideLoading: false, series: Object.freeze(recordingSeries(guide, true)), guideCursor: guide.nextCursor});
    } catch (error) {if (this.valid(attempt)) this.fail(error, {...this.snapshot, guideLoading: false, guideCursor: cursor, error: errorFacts(error)});}
  }
  private id(key: string) {let id = this.ids.get(key);if (!id) {if (this.ids.size >= 256) throw {code: 'server_busy'};id = channelRequestID();this.ids.set(key, id);}return id;}
  async save(draft: RuleDraft) {
    if (this.snapshot.draft !== draft) return false;
    if (!this.isActive() || this.snapshot.saving || this.snapshot.guideLoading || !draft) return false;
    const policy = this.permissions.getSnapshot();
    if (!policy.owners.some(row => recordingOwnerKey(row.owner) === recordingOwnerKey(draft.owner)) || !recordingAllowed(draft.owner, policy.grants)) return false;
    const attempt = this.begin();this.publish({...this.snapshot, saving: true, error: undefined});if (!this.valid(attempt)) return false;
    try {
      const key = JSON.stringify(draft);if (this.pending && this.pending.key !== key) throw {code: 'conflict'};
      const body = recordingRulePayload(draft, policy.grants, this.id(key));
      const request = this.pending ?? {key, path: '/v1/admin/dvr/recording-groups' + (draft.id ? '/' + encodeURIComponent(draft.id) : ''), method: draft.id ? 'PUT' : 'POST', body};this.pending = request;
      if (!this.valid(attempt)) return false;
      const raw = await this.api.request(request.path, request.method, request.body, attempt.abort.signal);if (!this.valid(attempt)) return false;
      const saved = parseRule(envelope(raw, this.serverId).result);
      if (draft.id && saved.id !== draft.id || recordingOwnerKey(saved.owner) !== recordingOwnerKey(draft.owner) || saved.match !== draft.match || saved.sourceId !== draft.sourceId || saved.name !== draft.name.trim() || saved.enabled !== draft.enabled || saved.kind !== draft.kind || saved.revision <= (draft.revision ?? 0) || !matchesOptions(saved.options, draft.options)) throw {code: 'invalid_response'};
      this.pending = undefined;this.publish({...this.snapshot, saving: false, draft: undefined, uncertain: false, series: [], rules: [saved, ...this.snapshot.rules.filter(row => row.id !== saved.id)]});return this.valid(attempt);
    } catch (error) {if (this.valid(attempt)) this.fail(error, {...this.snapshot, saving: false, uncertain: !!this.pending, error: errorFacts(error)});return false;}
  }
  async remove(rule: OwnerRule) {
    if (!this.isActive() || this.snapshot.saving || this.snapshot.draft || !this.snapshot.rules.includes(rule)) return false;
    const key = JSON.stringify(['remove', rule.id, rule.revision]);if (this.pending && this.pending.key !== key) return false;
    const attempt = this.begin();
    this.publish({...this.snapshot, saving: true, error: undefined});if (!this.valid(attempt)) return false;
    try {
      const operationId = this.id(key);
      const request = this.pending ?? {key, path: '/v1/admin/dvr/recording-groups/' + encodeURIComponent(rule.id) + '?' + new URLSearchParams({expectedRevision: String(rule.revision), operationId}), method: 'DELETE'};this.pending = request;
      const raw = await this.api.request(request.path, request.method, undefined, attempt.abort.signal);
      if (!this.valid(attempt)) return false;const result = record(envelope(raw, this.serverId).result);if (result.deleted !== true || result.id !== rule.id) throw {code: 'invalid_response'};
      this.pending = undefined;this.publish({...this.snapshot, saving: false, uncertain: false, rules: this.snapshot.rules.filter(row => row.id !== rule.id)});return this.valid(attempt);
    } catch (error) {if (this.valid(attempt)) this.fail(error, {...this.snapshot, saving: false, uncertain: !!this.pending, error: errorFacts(error)});return false;}
  }
  override dispose() {this.pending = undefined;this.ids?.clear();super.dispose();}
}
