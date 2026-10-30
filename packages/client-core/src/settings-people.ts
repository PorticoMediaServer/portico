import {encodedRoutePath, routePath} from '../../contracts/src/route-paths.ts';
import {decodeGetAdminAccessMembersResponse, decodePostAdminAccessMembersBulkResponse} from '../../contracts/src/decoders.ts';
import type {RouteResponse, RouteRequest} from '../../contracts/src/route-paths.ts';
import {parseAccessMember, parseLimitsDocument, type AccessMember, type AccessTier, type MemberLimits} from './server-administration.ts';
import {randomId} from './random-id.ts';

export type SettingsPersonRow = Readonly<RouteResponse<'get_admin_access_members'>['items'][number]>;
export type SettingsPeoplePage = Readonly<{items: readonly SettingsPersonRow[]; totalCount: number; matchedCount: number; nextCursor: string; limit: number}>;
export type SettingsPeopleQuery = Readonly<{search: string; role: 'all' | 'owner' | 'admin' | 'member'; status: 'all' | 'now' | 'month' | 'idle' | 'off' | 'waiting' | 'left' | 'did_not_move' | 'active'; sort: 'person' | 'role' | 'access' | 'profiles' | 'active'; direction: 'asc' | 'desc'}>;
export type SettingsPeopleApi = {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>};
export type SettingsBulkAccess = NonNullable<RouteRequest<'post_admin_access_members_bulk'>['access']>;
export type SettingsBulkAction = 'disable' | 'enable' | 'remove' | 'access';
const line = (value: unknown, max: number): value is string => typeof value === 'string' && value.length <= max && !/[\x00-\x1f\x7f]/.test(value);
const identifier = (value: unknown): value is string => line(value, 160) && /^[A-Za-z0-9_.-]+$/.test(value);
const integer = (value: unknown, min = 0, max = Number.MAX_SAFE_INTEGER): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value >= min && value <= max;
function invalid(): never {throw Object.assign(new Error('invalid_settings_people'), {code: 'invalid_settings_people'});}

/** Access lists are whole documents: an unreadable account can never silently disappear. */
export function parseSettingsPeoplePage(raw: unknown): SettingsPeoplePage {
  const page = decodeGetAdminAccessMembersResponse(raw);
  if (!integer(page.limit, 1, 200) || page.items.length > page.limit || !integer(page.totalCount) || !integer(page.matchedCount, 0, page.totalCount) || !line(page.nextCursor ?? '', 4096)) invalid();
  const seen = new Set<string>();
  const items = page.items.map(row => {
    if (!identifier(row.accountId) || seen.has(row.accountId) || !line(row.username, 64) || !row.username || !['owner','admin','member'].includes(row.role) || !integer(row.revision, 1) || !integer(row.limitsRevision, 1)) invalid();
    if (!['active','off','waiting','left','did_not_move'].includes(row.status) || !['idle','watching','listening'].includes(row.liveState) || !integer(row.maxStreams, 0, 1000) || !integer(row.profileCount, 1, 8) || row.profileNames.length !== row.profileCount) invalid();
    if (row.maxStreamsMode === 'limit' ? row.maxStreams < 1 : row.maxStreams !== 0) invalid();
    if (row.serverMaxStreams !== null && !integer(row.serverMaxStreams, 1, 1000) || row.effectiveMaxStreams !== null && !integer(row.effectiveMaxStreams, 1, 1000)) invalid();
    if (row.profileNames.some(name => !line(name, 120) || !name) || row.allowedLibraries.length > 1000 || row.allowedLibraries.some(value => !identifier(value)) || row.libraryNames.length > 1000 || row.libraryNames.some(value => !line(value, 512))) invalid();
    if (row.email !== undefined && !line(row.email, 254) || row.lastActiveAt !== undefined && (!line(row.lastActiveAt, 64) || !Number.isFinite(Date.parse(row.lastActiveAt)))) invalid();
    seen.add(row.accountId); return Object.freeze({...row, allowedLibraries: Object.freeze([...row.allowedLibraries]), libraryNames: Object.freeze([...row.libraryNames]), profileNames: Object.freeze([...row.profileNames])}) as SettingsPersonRow;
  });
  return Object.freeze({items: Object.freeze(items), totalCount: page.totalCount, matchedCount: page.matchedCount, nextCursor: page.nextCursor ?? '', limit: page.limit});
}

/** The selected session owns this client. Handover closes every in-flight read/write. */
export class SettingsPeopleClient {
  private closed = false;
  private requests = new Set<AbortController>();
  private api: SettingsPeopleApi;
  private isCurrent: () => boolean;
  constructor(api: SettingsPeopleApi, isCurrent: () => boolean) {this.api = api; this.isCurrent = isCurrent;}
  dispose() {this.closed = true; for (const request of this.requests) request.abort(); this.requests.clear();}
  private active() {if (this.closed || !this.isCurrent()) throw Object.assign(new Error('settings_scope_closed'), {code: 'settings_scope_closed'});}
  private async call<T>(path: string, method: string, body: unknown, parse: (raw: unknown) => T, signal?: AbortSignal): Promise<T> {
    this.active(); const controller = new AbortController(), abort = () => controller.abort(); this.requests.add(controller);
    if (signal?.aborted) controller.abort(); else signal?.addEventListener('abort', abort, {once: true});
    const timer = setTimeout(abort, 10000);
    try {const raw = await this.api.request<unknown>(path, method, body, controller.signal); this.active(); if (controller.signal.aborted) throw Object.assign(new Error('settings_request_ended'), {code: 'settings_request_ended'}); return parse(raw);}
    catch (error) {const status = error && typeof error === 'object' && 'status' in error ? error.status : undefined; if (status === 401 || status === 403) this.dispose(); throw error;}
    finally {clearTimeout(timer); signal?.removeEventListener('abort', abort); this.requests.delete(controller);}
  }
  read(query: SettingsPeopleQuery, cursor = '', signal?: AbortSignal): Promise<SettingsPeoplePage> {
    if (!line(query.search, 160) || !line(cursor, 4096)) invalid();
    const params = new URLSearchParams({q: query.search, role: query.role, status: query.status, sort: query.sort, direction: query.direction, limit: '40'});
    if (cursor) params.set('cursor', cursor);
    return this.call(routePath('get_admin_access_members') + '?' + params, 'GET', undefined, parseSettingsPeoplePage, signal);
  }
  /** Parsed current-query first page. Installation and tail/detail ownership stay with the caller. */
  refresh(query: SettingsPeopleQuery, signal?: AbortSignal): Promise<SettingsPeoplePage> {
    return this.read(query, '', signal);
  }
  person(accountId: string, signal?: AbortSignal): Promise<AccessMember> {
    if (!identifier(accountId)) invalid();
    return this.call(encodedRoutePath('get_admin_access_member', {id: encodeURIComponent(accountId)}), 'GET', undefined, raw => {const person = parseAccessMember(raw); if (person.accountId !== accountId) invalid(); return person;}, signal);
  }
  notes(person: AccessMember, notes: string): Promise<AccessMember> {
    if (!line(notes, 2000)) invalid();
    const body: RouteRequest<'put_admin_access_member_notes'> = {notes, expectedRevision: person.revision, operationId: randomId()};
    return this.call(encodedRoutePath('put_admin_access_member_notes', {id: encodeURIComponent(person.accountId)}), 'PUT', body, raw => {const value = parseAccessMember(raw); if (value.accountId !== person.accountId) invalid(); return value;});
  }
  role(person: AccessMember, role: AccessTier): Promise<AccessMember> {
    const body: RouteRequest<'put_admin_access_members_id_role'> = {role, expectedRevision: person.revision, operationId: randomId()};
    return this.call(encodedRoutePath('put_admin_access_members_id_role', {id: encodeURIComponent(person.accountId)}), 'PUT', body, raw => {const value = parseAccessMember(raw); if (value.accountId !== person.accountId) invalid(); return value;});
  }
  limits(person: AccessMember, limits: MemberLimits) {
    const body: RouteRequest<'put_admin_access_members_id_limits'> = {limits: {...limits, schedule: {...limits.schedule, windows: limits.schedule.windows.map(window => ({...window, days: [...window.days]}))}, channelPolicy: {...limits.channelPolicy, channels: [...limits.channelPolicy.channels]}, tagPolicy: {deniedLabels: [...limits.tagPolicy.deniedLabels]}}, expectedRevision: person.limitsRevision, operationId: randomId()};
    return this.call(encodedRoutePath('put_admin_access_members_id_limits', {id: encodeURIComponent(person.accountId)}), 'PUT', body, raw => parseLimitsDocument(raw, person.accountId));
  }
  /** Exactly one producer transaction, using the revisions already published in the table. */
  bulk(rows: readonly SettingsPersonRow[], action: SettingsBulkAction, access?: SettingsBulkAccess, operationId = randomId()): Promise<readonly string[]> {
    if (!rows.length || rows.length > 200 || new Set(rows.map(row => row.accountId)).size !== rows.length || (action === 'access') !== (access !== undefined)) invalid();
    const body: RouteRequest<'post_admin_access_members_bulk'> = {action, operationId, targets: rows.map(row => ({accountId: row.accountId, expectedRevision: row.revision, expectedLimitsRevision: row.limitsRevision})), ...(access ? {access} : {})};
    return this.call(routePath('post_admin_access_members_bulk'), 'POST', body, raw => {
      const result = decodePostAdminAccessMembersBulkResponse(raw);
      if (result.action !== action || result.accountIds.length !== rows.length || new Set(result.accountIds).size !== rows.length || result.accountIds.some(id => !rows.some(row => row.accountId === id))) invalid();
      return Object.freeze([...result.accountIds]);
    });
  }
}
