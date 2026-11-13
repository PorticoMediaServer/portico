import type {Member, MemberEdit, NowPlayingPage, StreamLimits} from '../../contracts/src/next/api.ts';
import type {AccessMember, AccessTier, AccountLimits, MemberLimits, LimitsDocument, StreamLimitMode} from './server-administration.ts';
import {callServer, type ServerRequester} from './query/server-api.ts';
import {CodedError, unreadableServerResponse} from './server-messages.ts';

/** One row of People's table. `revision` and `limitsRevision` are always 1: on the rebuilt server the later save wins.
 * `streamsKnown` is false when this viewer may not read the owner's stream limits (an administrator), so the stream
 * cells say nothing rather than guess. `lastActiveAt` and `profileNames` come when the server sends them. */
export type SettingsPersonRow = Readonly<{accountId: string; username: string; email?: string; role: AccessTier; revision: number; limitsRevision: number;
  disabled: boolean; status: 'active' | 'off' | 'waiting' | 'left' | 'did_not_move'; hasMoveCode: boolean; allowedLibraries: readonly string[]; libraryNames: readonly string[];
  maxStreams: number; maxStreamsMode: StreamLimitMode; serverMaxStreams: number | null; effectiveMaxStreams: number | null; streamsKnown: boolean;
  profileCount: number; profileNames: readonly string[]; liveState: 'idle' | 'watching' | 'listening'; lastActiveAt?: string;
  /** The server keeps this account's whole-account limits (home only, hours, rating and the rest), so they can be shown and set. */
  accountLimits: boolean}>;
export type SettingsPeoplePage = Readonly<{items: readonly SettingsPersonRow[]; totalCount: number; matchedCount: number; nextCursor: string; limit: number}>;
export type SettingsPeopleQuery = Readonly<{search: string; role: 'all' | 'owner' | 'admin' | 'member'; status: 'all' | 'now' | 'month' | 'idle' | 'off' | 'waiting' | 'left' | 'did_not_move' | 'active'; sort: 'person' | 'role' | 'access' | 'profiles' | 'active'; direction: 'asc' | 'desc'}>;
export type SettingsPeopleApi = ServerRequester;
/** What bulk access may change. Only libraries and the stream count are on the rebuilt server yet; the rest is refused. */
export type SettingsBulkAccess = Readonly<{allowedLibraries?: string[]; maxStreamsMode?: string; maxStreams?: number; allowRemote?: boolean; maxContentRating?: string; accessTokens?: boolean; maxProfiles?: number}>;
export type SettingsBulkAction = 'disable' | 'enable' | 'remove' | 'access';
type Library = Readonly<{id: string; name: string}>;
type Streams = Pick<SettingsPersonRow, 'maxStreams' | 'maxStreamsMode' | 'serverMaxStreams' | 'effectiveMaxStreams' | 'streamsKnown'>;

const line = (value: unknown, max: number): value is string => typeof value === 'string' && value.length <= max && !/[\x00-\x1f\x7f]/.test(value);
function invalid(): never {throw Object.assign(new Error('invalid_settings_people'), {code: 'invalid_settings_people'});}
const unreadable = (): never => {throw new CodedError('invalid_response', unreadableServerResponse);};
const notYet = (): never => {throw new CodedError('not_available', 'This is not available on this server yet.');};
const monthMs = 30 * 24 * 60 * 60 * 1000;
const roleOrder: Readonly<Record<AccessTier, number>> = {owner: 0, admin: 1, member: 2};

/** What an account is held to when the server keeps no whole-account limits: nothing but the owner's stream limits. */
const noAccountLimits: Omit<MemberLimits, 'maxStreams' | 'maxStreamsMode'> = Object.freeze({accessTokens: true, maxProfiles: 0, allowRemote: true, conversion: 'server',
  remoteBitrateKbps: 0, ratingSystem: '', maxContentRating: '', maximumAge: -1, allowUnrated: true,
  schedule: Object.freeze({timezone: '', windows: Object.freeze([])}), channelPolicy: Object.freeze({mode: 'all', channels: Object.freeze([])}), tagPolicy: Object.freeze({deniedLabels: Object.freeze([])})});
/** The highest-rating choices People offers, each the age the server keeps for it (13 shows PG-13 and below). */
const ratingAges: Readonly<Record<string, number>> = {G: 0, PG: 8, 'PG-13': 13, R: 17};
/** The choice that shows a stored age: the highest whose age is at or below it. */
const ratingOf = (age: number) => Object.entries(ratingAges).filter(([, at]) => at <= age).pop()?.[0] ?? 'G';

/** The server's whole-account limits in the shape People's rows read. Live TV channels are not among them yet, and the
 * server keeps no limit on converting, so that row is not shown. */
function screenLimits(a: AccountLimits): Omit<MemberLimits, 'maxStreams' | 'maxStreamsMode'> {
  const age = a.maxRatingAge ?? null;
  return Object.freeze({accessTokens: a.allowTokens, maxProfiles: a.maxProfiles ?? 0, allowRemote: !a.homeOnly, conversion: 'server',
    remoteBitrateKbps: a.remoteMaxBitrateKbps ?? 0, ratingSystem: age === null ? '' : 'age', maxContentRating: age === null ? '' : ratingOf(age), maximumAge: age ?? -1,
    allowUnrated: a.allowUnrated, schedule: Object.freeze({timezone: a.viewingHours?.timezone || deviceZone(), windows: Object.freeze([...(a.viewingHours?.windows ?? [])])}),
    channelPolicy: noAccountLimits.channelPolicy, tagPolicy: Object.freeze({deniedLabels: Object.freeze([...a.blockedLabels])})});
}
/** The zone viewing hours are kept in when the owner has not chosen one: this device's. */
const deviceZone = () => {try {return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';} catch {return 'UTC';}};
type LimitChanges = {-readonly [K in keyof AccountLimits]?: AccountLimits[K]};
/** What People's edited rows change in the server's limits. A save names only those fields; the server keeps the rest.
 * A choice the server cannot keep (converting, Live TV channels, a rating it has no age for) is refused, never
 * dropped. Viewing hours always carry a zone. */
function limitChanges(before: MemberLimits, next: MemberLimits): LimitChanges {
  if (next.conversion !== before.conversion || JSON.stringify(next.channelPolicy) !== JSON.stringify(before.channelPolicy)) notYet();
  const out: LimitChanges = {};
  if (next.allowRemote !== before.allowRemote) out.homeOnly = !next.allowRemote;
  if (next.accessTokens !== before.accessTokens) out.allowTokens = next.accessTokens;
  if (next.allowUnrated !== before.allowUnrated) out.allowUnrated = next.allowUnrated;
  if (next.maxContentRating !== before.maxContentRating) {
    const age = next.maxContentRating === '' ? null : ratingAges[next.maxContentRating];
    if (age === undefined) notYet();
    out.maxRatingAge = age;
  }
  if (JSON.stringify(next.tagPolicy.deniedLabels) !== JSON.stringify(before.tagPolicy.deniedLabels)) out.blockedLabels = [...next.tagPolicy.deniedLabels];
  if (next.remoteBitrateKbps !== before.remoteBitrateKbps) out.remoteMaxBitrateKbps = next.remoteBitrateKbps > 0 ? next.remoteBitrateKbps : null;
  if (next.maxProfiles !== before.maxProfiles) out.maxProfiles = next.maxProfiles > 0 ? next.maxProfiles : null;
  if (JSON.stringify(next.schedule) !== JSON.stringify(before.schedule))
    out.viewingHours = next.schedule.windows.length ? {timezone: next.schedule.timezone || deviceZone(), windows: next.schedule.windows.map(w => ({...w, days: [...w.days]}))} : null;
  return out;
}
const accountLimitsOf = (m: Member) => {const limits = (m as {limits?: unknown}).limits; return limits && typeof limits === 'object' ? limits as AccountLimits : undefined;};

/** One account's streams from the owner's limits: its own number wins over the per-person number, and its own 0 is no limit. */
export function streamsOf(accountId: string, limits: StreamLimits | undefined): Streams {
  if (!limits) return {maxStreams: 0, maxStreamsMode: 'inherit', serverMaxStreams: null, effectiveMaxStreams: null, streamsKnown: false};
  const server = limits.perPerson ? limits.perPerson : null, own = limits.people && Object.hasOwn(limits.people, accountId) ? limits.people[accountId] : undefined;
  if (own === undefined) return {maxStreams: 0, maxStreamsMode: 'inherit', serverMaxStreams: server, effectiveMaxStreams: server, streamsKnown: true};
  return own > 0 ? {maxStreams: own, maxStreamsMode: 'limit', serverMaxStreams: server, effectiveMaxStreams: own, streamsKnown: true}
    : {maxStreams: 0, maxStreamsMode: 'unlimited', serverMaxStreams: server, effectiveMaxStreams: null, streamsKnown: true};
}

function checkMember(m: Member): Member {
  if (!line(m?.id, 160) || !m.id || !line(m.username, 64) || !m.username || !(m.role in roleOrder) || typeof m.disabled !== 'boolean' || typeof m.allLibraries !== 'boolean' || !Array.isArray(m.libraries)) unreadable();
  return m;
}
const allowedOf = (m: Member) => Object.freeze(m.allLibraries ? [] : [...m.libraries]);
const lastActiveOf = (m: Member) => {const at = (m as {lastActiveAt?: unknown}).lastActiveAt; return typeof at === 'string' && Number.isFinite(Date.parse(at)) ? at : undefined;};
const profilesOf = (m: Member) => {const names = (m as {profiles?: unknown}).profiles; return Object.freeze(Array.isArray(names) ? names.filter((name): name is string => line(name, 120) && !!name) : []);};

/** Where an account stands: waiting to move after the kind of sign-in changed, left, not moved when the owner finished the
 * move without them, turned off, or active. */
function statusOf(m: Member): SettingsPersonRow['status'] {
  const state = (m as {state?: unknown}).state;
  return state === 'waiting' ? 'waiting' : state === 'left' ? 'left' : state === 'not_moved' ? 'did_not_move' : m.disabled ? 'off' : 'active';
}
function personRow(m: Member, libraries: readonly Library[], limits: StreamLimits | undefined, live: ReadonlyMap<string, SettingsPersonRow['liveState']>, codes: ReadonlySet<string>): SettingsPersonRow {
  const allowed = allowedOf(m), names = allowed.map(id => libraries.find(library => library.id === id)?.name).filter((name): name is string => !!name);
  const profileNames = profilesOf(m), lastActiveAt = lastActiveOf(m);
  return Object.freeze({accountId: m.id, username: m.username, ...(m.email ? {email: m.email} : {}), role: m.role as AccessTier, revision: 1, limitsRevision: 1,
    disabled: m.disabled, status: statusOf(m), hasMoveCode: codes.has(m.id), allowedLibraries: allowed, libraryNames: Object.freeze(names),
    ...streamsOf(m.id, limits), profileCount: profileNames.length, profileNames, liveState: live.get(m.username) ?? 'idle', ...(lastActiveAt ? {lastActiveAt} : {}),
    accountLimits: !!accountLimitsOf(m)});
}

/** An account as People's panel edits it: its stream count from the owner's limits, and its whole-account limits when the
 * server keeps them (else the defaults every account has). */
export function accessMemberOf(m: Member, streams: Streams): AccessMember {
  checkMember(m);
  const {streamsKnown: _known, ...facts} = streams, profileNames = profilesOf(m), lastActiveAt = lastActiveOf(m), account = accountLimitsOf(m);
  return Object.freeze({accountId: m.id, username: m.username, ...(m.email ? {email: m.email} : {}), profileId: '', role: m.role as AccessTier, disabled: m.disabled, ...(m.twoFactorEnabled ? {twoStep: true} : {}),
    ...(m.notes ? {notes: m.notes} : {}), profileNames, ...(lastActiveAt ? {lastActiveAt} : {}), revision: 1, allowedLibraries: allowedOf(m), limitsRevision: 1,
    limits: Object.freeze({...(account ? screenLimits(account) : noAccountLimits), maxStreams: facts.maxStreams, maxStreamsMode: facts.maxStreamsMode}),
    ...(account ? {accountLimits: account} : {}), serverMaxStreams: facts.serverMaxStreams, effectiveMaxStreams: facts.effectiveMaxStreams});
}

function matches(row: SettingsPersonRow, query: SettingsPeopleQuery, now: number): boolean {
  const search = query.search.trim().toLocaleLowerCase();
  if (search && !row.username.toLocaleLowerCase().includes(search) && !row.email?.toLocaleLowerCase().includes(search)) return false;
  if (query.role !== 'all' && row.role !== query.role) return false;
  const recent = !!row.lastActiveAt && now - Date.parse(row.lastActiveAt) <= monthMs;
  switch (query.status) {
    case 'all': return true;
    case 'active': case 'off': case 'waiting': case 'left': case 'did_not_move': return row.status === query.status;
    case 'now': return row.liveState !== 'idle';
    case 'month': return row.liveState !== 'idle' || recent;
    case 'idle': return row.status === 'active' && row.liveState === 'idle' && !recent;
    default: return false;
  }
}
function compare(a: SettingsPersonRow, b: SettingsPersonRow, sort: SettingsPeopleQuery['sort']): number {
  const byName = a.username.localeCompare(b.username);
  switch (sort) {
    case 'role': return roleOrder[a.role] - roleOrder[b.role] || byName;
    case 'access': return a.libraryNames.join(', ').localeCompare(b.libraryNames.join(', ')) || byName;
    case 'profiles': return a.profileCount - b.profileCount || byName;
    case 'active': return Number(a.liveState !== 'idle') - Number(b.liveState !== 'idle') || (a.lastActiveAt ?? '').localeCompare(b.lastActiveAt ?? '') || -byName;
    default: return byName;
  }
}

/**
 * People's table and panel on the rebuilt server. The server answers with every account (a household is at most a few
 * hundred), so search, filters and sorting happen here and the table gets one page. The selected session owns this
 * client: handover closes every request in flight.
 */
export class SettingsPeopleClient {
  private closed = false;
  private requests = new Set<AbortController>();
  private readonly api: SettingsPeopleApi;
  private readonly isCurrent: () => boolean;
  private readonly libraries: () => readonly Library[];
  /** `libraries` names the libraries an account may open, for the table's Access column. */
  constructor(api: SettingsPeopleApi, isCurrent: () => boolean, libraries: () => readonly Library[] = () => []) {this.api = api; this.isCurrent = isCurrent; this.libraries = libraries;}
  dispose() {this.closed = true; for (const request of this.requests) request.abort(); this.requests.clear();}
  private active() {if (this.closed || !this.isCurrent()) throw Object.assign(new Error('settings_scope_closed'), {code: 'settings_scope_closed'});}
  /** One request bound to this client. A refusal of the viewer (401, 403) closes the client unless the read is `optional`. */
  private async call<T>(send: (signal: AbortSignal) => Promise<T>, signal?: AbortSignal, optional = false): Promise<T> {
    this.active(); const controller = new AbortController(), abort = () => controller.abort(); this.requests.add(controller);
    if (signal?.aborted) controller.abort(); else signal?.addEventListener('abort', abort, {once: true});
    const timer = setTimeout(abort, 10000);
    try {const raw = await send(controller.signal); this.active(); if (controller.signal.aborted) throw Object.assign(new Error('settings_request_ended'), {code: 'settings_request_ended'}); return raw;}
    catch (error) {const status = error && typeof error === 'object' && 'status' in error ? error.status : undefined; if (!optional && (status === 401 || status === 403)) this.dispose(); throw error;}
    finally {clearTimeout(timer); signal?.removeEventListener('abort', abort); this.requests.delete(controller);}
  }
  /** The owner's stream limits, or undefined for a viewer who may not read them (only the owner may). */
  private streamLimits(signal?: AbortSignal): Promise<StreamLimits | undefined> {
    return this.call(s => callServer(this.api, 'GET /v1/admin/playback/stream-limits', {signal: s}), signal, true).catch(error => {if (this.closed || signal?.aborted) throw error; return undefined;});
  }
  /** Who is playing now, by username. A failed read leaves everyone idle: the table still loads. */
  private async live(signal?: AbortSignal): Promise<ReadonlyMap<string, SettingsPersonRow['liveState']>> {
    const page: NowPlayingPage | undefined = await this.call(s => callServer(this.api, 'GET /v1/admin/sessions', {signal: s}), signal, true).catch(error => {if (this.closed || signal?.aborted) throw error; return undefined;});
    const live = new Map<string, SettingsPersonRow['liveState']>();
    for (const item of Array.isArray(page?.items) ? page.items : []) if (typeof item?.username === 'string' && live.get(item.username) !== 'watching') live.set(item.username, item.kind === 'audio' ? 'listening' : 'watching');
    return live;
  }
  /** Who of those waiting to move holds a code that still works. Read only while someone waits, from the owner's sign-in
   * page; an administrator, who may not read it, sees no code either way. */
  private async moveCodes(members: readonly Member[], signal?: AbortSignal): Promise<ReadonlySet<string>> {
    if (!members.some(member => statusOf(member) === 'waiting')) return new Set();
    const page = await this.call(s => callServer(this.api, 'GET /v1/admin/sign-in', {signal: s}), signal, true).catch(error => {if (this.closed || signal?.aborted) throw error; return undefined;});
    const now = Date.now();
    return new Set((Array.isArray(page?.waiting) ? page.waiting : []).filter(row => typeof row?.codeExpiresAt === 'string' && Date.parse(row.codeExpiresAt) > now).map(row => row.id));
  }
  async read(query: SettingsPeopleQuery, cursor = '', signal?: AbortSignal): Promise<SettingsPeoplePage> {
    if (!line(query.search, 160) || !line(cursor, 4096)) invalid();
    const [members, limits, live] = await Promise.all([this.call(s => callServer(this.api, 'GET /v1/admin/members', {signal: s}), signal), this.streamLimits(signal), this.live(signal)]);
    if (!Array.isArray(members?.items)) unreadable();
    const seen = new Set<string>(), libraries = this.libraries();
    const codes = await this.moveCodes(members.items, signal);
    const rows = members.items.map(member => {checkMember(member); if (seen.has(member.id)) unreadable(); seen.add(member.id); return personRow(member, libraries, limits, live, codes);});
    if (cursor) return Object.freeze({items: Object.freeze([]), totalCount: rows.length, matchedCount: 0, nextCursor: '', limit: 1});
    const now = Date.now(), direction = query.direction === 'desc' ? -1 : 1;
    const matched = rows.filter(row => matches(row, query, now)).sort((a, b) => direction * compare(a, b, query.sort));
    return Object.freeze({items: Object.freeze(matched), totalCount: rows.length, matchedCount: matched.length, nextCursor: '', limit: Math.max(1, matched.length)});
  }
  refresh(query: SettingsPeopleQuery, signal?: AbortSignal): Promise<SettingsPeoplePage> {return this.read(query, '', signal);}
  async person(accountId: string, signal?: AbortSignal): Promise<AccessMember> {
    if (!line(accountId, 160) || !accountId) invalid();
    const [member, limits] = await Promise.all([this.call(s => callServer(this.api, 'GET /v1/admin/members/{id}', {id: accountId, signal: s}), signal), this.streamLimits(signal)]);
    if (member?.id !== accountId) unreadable();
    return accessMemberOf(member, streamsOf(accountId, limits));
  }
  private async edit(person: AccessMember, body: MemberEdit): Promise<AccessMember> {
    const member = await this.call(s => callServer(this.api, 'PATCH /v1/admin/members/{id}', {id: person.accountId, body, signal: s}));
    if (member?.id !== person.accountId) unreadable();
    return accessMemberOf(member, {maxStreams: person.limits.maxStreams, maxStreamsMode: person.limits.maxStreamsMode, serverMaxStreams: person.serverMaxStreams, effectiveMaxStreams: person.effectiveMaxStreams, streamsKnown: true});
  }
  async notes(person: AccessMember, notes: string): Promise<AccessMember> {
    if (!line(notes, 2000)) invalid();
    return this.edit(person, {notes});
  }
  async role(person: AccessMember, role: AccessTier): Promise<AccessMember> {
    if (role === 'owner') invalid();
    return this.edit(person, {role});
  }
  /** Libraries this account may open: none listed is all of them. */
  libraryAccess(person: Pick<AccessMember, 'accountId'>, allowedLibraries: readonly string[]): Promise<Member> {
    return this.call(s => callServer(this.api, 'PATCH /v1/admin/members/{id}', {id: person.accountId, body: {allLibraries: allowedLibraries.length === 0, libraries: [...allowedLibraries]}, signal: s}));
  }
  /** Sets accounts' own stream numbers in the owner's limits, keeping everything else in them. Only the owner may. */
  private async setStreams(accountIds: readonly string[], mode: string | undefined, count: number | undefined): Promise<StreamLimits> {
    if (mode !== 'inherit' && mode !== 'unlimited' && !(mode === 'limit' && Number.isSafeInteger(count) && count! >= 1 && count! <= 1000)) invalid();
    const current = await this.call(s => callServer(this.api, 'GET /v1/admin/playback/stream-limits', {signal: s})) ?? {};
    const people: Record<string, number> = {...current.people};
    for (const id of accountIds) {if (mode === 'inherit') delete people[id]; else people[id] = mode === 'unlimited' ? 0 : count!;}
    const body: StreamLimits = {...current, people};
    await this.call(s => callServer(this.api, 'PUT /v1/admin/playback/stream-limits', {body, signal: s}));
    return body;
  }
  /** The stream count goes to the owner's stream limits; the rest to the account's own limits, naming only what changed.
   * An account whose limits the server does not keep has only the stream count: anything else is refused. */
  async limits(person: AccessMember, limits: MemberLimits): Promise<LimitsDocument> {
    const accountChanged = (Object.keys(noAccountLimits) as (keyof typeof noAccountLimits)[]).some(key => JSON.stringify(limits[key]) !== JSON.stringify(person.limits[key]));
    const streamsChanged = limits.maxStreams !== person.limits.maxStreams || limits.maxStreamsMode !== person.limits.maxStreamsMode;
    if (accountChanged && !person.accountLimits) notYet();
    const body = accountChanged ? limitChanges(person.limits, limits) : undefined;
    let next = person;
    if (body) next = await this.edit(person, {limits: body} as MemberEdit);
    const saved = streamsChanged ? streamsOf(person.accountId, await this.setStreams([person.accountId], limits.maxStreamsMode, limits.maxStreams))
      : {maxStreams: person.limits.maxStreams, maxStreamsMode: person.limits.maxStreamsMode, serverMaxStreams: person.serverMaxStreams, effectiveMaxStreams: person.effectiveMaxStreams};
    return Object.freeze({accountId: person.accountId, revision: 1, limits: Object.freeze({...next.limits, maxStreams: saved.maxStreams, maxStreamsMode: saved.maxStreamsMode}),
      serverMaxStreams: saved.serverMaxStreams, effectiveMaxStreams: saved.effectiveMaxStreams});
  }
  /** Let back in someone who left the server (area 2 rule 39): they can sign in again, and everything they had, kept
   * since they left, is theirs again. The owner's alone; the server turns back on only an account that left. */
  async letBackIn(person: SettingsPersonRow): Promise<void> {
    if (person.status !== 'left') invalid();
    await this.call(s => callServer(this.api, 'POST /v1/admin/portico/left/{id}/let-back-in', {id: person.accountId, signal: s}));
  }
  /** One request per account (the server has no bulk route); the accounts done are returned. Bulk access sets libraries
   * and, where the server keeps them, away from home, the highest rating, tokens and the profile count; the stream count
   * goes to the owner's stream limits. An account whose limits the server does not keep is refused before anything is sent. */
  async bulk(rows: readonly SettingsPersonRow[], action: SettingsBulkAction, access?: SettingsBulkAccess, _operationId?: string): Promise<readonly string[]> {
    if (!rows.length || rows.length > 1000 || new Set(rows.map(row => row.accountId)).size !== rows.length || (action === 'access') !== (access !== undefined)) invalid();
    const limited = !!access && (['allowRemote', 'maxContentRating', 'accessTokens', 'maxProfiles'] as const).some(key => access[key] !== undefined);
    if (limited && rows.some(row => !row.accountLimits)) notYet();
    const rating = access?.maxContentRating === undefined ? undefined : access.maxContentRating === '' ? null : ratingAges[access.maxContentRating];
    if (rating === undefined && access?.maxContentRating !== undefined) notYet();
    const bulkLimits: LimitChanges = {...(access?.allowRemote !== undefined ? {homeOnly: !access.allowRemote} : {}), ...(rating !== undefined ? {maxRatingAge: rating} : {}),
      ...(access?.accessTokens !== undefined ? {allowTokens: access.accessTokens} : {}), ...(access?.maxProfiles !== undefined ? {maxProfiles: access.maxProfiles > 0 ? access.maxProfiles : null} : {})};
    const done: string[] = [];
    for (const row of rows) {
      if (action === 'remove') await this.call(s => callServer(this.api, 'DELETE /v1/admin/members/{id}', {id: row.accountId, signal: s}));
      else if (action === 'disable' || action === 'enable') await this.call(s => callServer(this.api, 'PATCH /v1/admin/members/{id}', {id: row.accountId, body: {disabled: action === 'disable'}, signal: s}));
      else if (access) {
        const body: Record<string, unknown> = access.allowedLibraries ? {allLibraries: access.allowedLibraries.length === 0, libraries: [...access.allowedLibraries]} : {};
        if (limited) body.limits = bulkLimits;
        if (Object.keys(body).length) await this.call(s => callServer(this.api, 'PATCH /v1/admin/members/{id}', {id: row.accountId, body: body as MemberEdit, signal: s}));
      }
      done.push(row.accountId);
    }
    if (access && (access.maxStreamsMode !== undefined || access.maxStreams !== undefined)) await this.setStreams(rows.map(row => row.accountId), access.maxStreamsMode, access.maxStreams);
    return Object.freeze(done);
  }
}
