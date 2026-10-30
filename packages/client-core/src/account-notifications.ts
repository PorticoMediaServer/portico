import {routeBody as hostedRouteBody} from "../../contracts/src/hosted/routes.ts";
import {routePath as hostedRoutePath, encodedRoutePath as hostedEncodedRoutePath} from "../../contracts/src/hosted/routes.ts";
/**
 * The Portico Account's in-app notification feed and data export (BE-hosted, `be/hosted` da0a9db):
 * no push and no alert email any more. Parsers are strict about the shapes and lenient about
 * unknown kinds. Retired server-private events never become generic account rows. One item of a page that
 * cannot be read is left out and the rest of the page is kept.
 *
 * - `GET /v1/account/notifications[?cursor=]` → `{items, unread, nextCursor?}` (newest first, 50 a page)
 * - `GET /v1/account/notifications/{id}`
 * - `POST /v1/account/notifications/read` `{ids}` or `{all: true}`
 * - `GET|PUT /v1/account/notification-preferences` `{serverOffline}`
 * - export: `POST /v1/account/export` → `{id, state, expiresAt}`, then `GET /v1/account/exports/{id}`
 *   until `ready` (or the `account_export_ready` item), then `POST /v1/account/exports/{id}/download`.
 */
import {keepReadable, noteSkipped} from './tolerant.ts';

export type AccountNotificationKind =
  | 'server_offline'
  | 'new_device' | 'security_event' | 'account_export_ready' | 'signing_key_expiring';
export const ACCOUNT_NOTIFICATION_KINDS: readonly AccountNotificationKind[] = ['server_offline', 'new_device', 'security_event', 'account_export_ready', 'signing_key_expiring'];
/** Four ordinary kinds plus the real operator key alert in the same account
 * feed. The producer scopes that alert to its recipient, not a client role. */
export type AccountFeedKind='new_device'|'account_export_ready'|'security_event'|'server_offline'|'signing_key_expiring';
export function isAccountFeedNotification(item:AccountNotification):item is AccountNotification&{kind:AccountFeedKind}{return (ACCOUNT_NOTIFICATION_KINDS as readonly string[]).includes(item.kind);}
export const ACCOUNT_SECURITY_EVENTS = ['password_changed', 'password_reset', 'recovery_codes_replaced', 'sign_in_method_removed', 'provider_linked', 'two_step_enabled', 'two_step_disabled', 'contact_email_changed', 'contact_email_verified'] as const;
export type AccountSecurityEvent = (typeof ACCOUNT_SECURITY_EVENTS)[number];

export type AccountNotification = Readonly<{
  id: string;
  /** Unknown non-retired kinds are kept for caller-specific handling. */
  kind: AccountNotificationKind | 'other';
  rawKind: string;
  occurredAt: string;
  read: boolean;
  server?: Readonly<{id: string; name: string}>;
  detail: Readonly<Record<string, string>>;
}>;
export type AccountNotificationPage = Readonly<{items: readonly AccountNotification[]; unread: number; nextCursor?: string}>;
export type NotificationPreferences = Readonly<{serverOffline: boolean}>;
export type AccountExportJob = Readonly<{id: string; state: 'pending' | 'ready'; expiresAt: string}>;

type Request = <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => Promise<T>;

function bad(): never { throw Object.assign(new Error('invalid notification response'), {code: 'invalid_response'}); }
const obj = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const str = (v: unknown, max = 256): v is string => typeof v === 'string' && v.length > 0 && v.length <= max;
const instant = (v: unknown): v is string => typeof v === 'string' && v.length <= 64 && Number.isFinite(Date.parse(v));

export function parseAccountNotification(v: unknown): AccountNotification {
  if (!obj(v) || !str(v.id, 128) || !str(v.kind, 64) || !instant(v.occurredAt) || typeof v.read !== 'boolean') bad();
  if(v.kind==='storage_nearly_full'||v.kind==='invitation_accepted')bad();
  let server: {id: string; name: string} | undefined;
  if (v.server !== undefined && v.server !== null) {
    // A server reference or detail that cannot be read is left off; the item still says what happened and when.
    if (!obj(v.server) || !str(v.server.id, 128) || typeof v.server.name !== 'string' || v.server.name.length > 200) noteSkipped('account-notification', 'server');
    else server = {id: v.server.id, name: v.server.name};
  }
  const detail: Record<string, string> = {};
  if (v.detail !== undefined && v.detail !== null) {
    if (!obj(v.detail)) noteSkipped('account-notification', 'detail');
    else for (const [k, val] of Object.entries(v.detail)) if (typeof val === 'string' && k.length <= 64 && val.length <= 500) detail[k] = val;
  }
  const kind = (ACCOUNT_NOTIFICATION_KINDS as readonly string[]).includes(v.kind) ? v.kind as AccountNotificationKind : 'other';
  return Object.freeze({id: v.id, kind, rawKind: v.kind, occurredAt: v.occurredAt, read: v.read, ...(server ? {server: Object.freeze(server)} : {}), detail: Object.freeze(detail)});
}

export function parseAccountNotificationPage(v: unknown): AccountNotificationPage {
  if (!obj(v) || !Array.isArray(v.items) || v.items.length > 200 || typeof v.unread !== 'number' || !Number.isInteger(v.unread) || v.unread < 0) bad();
  if (v.nextCursor !== undefined && v.nextCursor !== '' && !str(v.nextCursor, 2048)) bad();
  const items = keepReadable('account-notifications', 'items', v.items, parseAccountNotification);
  return Object.freeze({items: Object.freeze(items), unread: v.unread, ...(v.nextCursor ? {nextCursor: v.nextCursor as string} : {})});
}

export function parseNotificationPreferences(v: unknown): NotificationPreferences {
  if (!obj(v) || typeof v.serverOffline !== 'boolean' || Object.keys(v).some(key=>key!=='serverOffline')) bad();
  return Object.freeze({serverOffline: v.serverOffline});
}

export function parseAccountExportJob(v: unknown): AccountExportJob {
  if (!obj(v) || !str(v.id, 160) || (v.state !== 'pending' && v.state !== 'ready') || !instant(v.expiresAt)) bad();
  return Object.freeze({id: v.id, state: v.state, expiresAt: v.expiresAt});
}

export class AccountNotificationsClient {
  private readonly request: Request;
  constructor(request: Request) { this.request = request; }
  async page(cursor?: string, signal?: AbortSignal): Promise<AccountNotificationPage> {
    return parseAccountNotificationPage(await this.request(hostedRoutePath("account_notifications_list") + (cursor ? '?cursor=' + encodeURIComponent(cursor) : ''), 'GET', undefined, signal));
  }
  async one(id: string, signal?: AbortSignal): Promise<AccountNotification> {
    return parseAccountNotification(await this.request(hostedEncodedRoutePath("account_notification_get", {"id": encodeURIComponent(id)}), 'GET', undefined, signal));
  }
  async markRead(ids: readonly string[] | 'all'): Promise<void> {
    if (ids !== 'all' && !ids.length) return;
    await this.request(hostedRoutePath("account_notifications_read"), 'POST', ids === 'all' ? {all: true} : {ids: [...ids]});
  }
  async preferences(signal?: AbortSignal): Promise<NotificationPreferences> {
    return parseNotificationPreferences(await this.request(hostedRoutePath("account_notification_preferences_get"), 'GET', undefined, signal));
  }
  async savePreferences(p: NotificationPreferences): Promise<NotificationPreferences> {
    return parseNotificationPreferences(await this.request(hostedRoutePath("account_notification_preferences_set"), 'PUT', hostedRouteBody("account_notification_preferences_set", {serverOffline: p.serverOffline})));
  }
  /** Starts (or rejoins) the account's export with the signed-in session (BE-hosted cc222bf: `{operationId}`, 16–128 characters, no proof). Idempotent per operation id. */
  async requestExport(operationId: string, signal?: AbortSignal): Promise<AccountExportJob> {
    if (operationId.length < 16 || operationId.length > 128) throw new Error('An export operation id is 16 to 128 characters.');
    throwIfExportAborted(signal);
    const raw = await this.request<unknown>(hostedRoutePath("account_export_request"), 'POST', hostedRouteBody("account_export_request", {operationId}), signal);
    throwIfExportAborted(signal);
    return parseAccountExportJob(raw);
  }
  async exportStatus(id: string, signal?: AbortSignal): Promise<AccountExportJob> {
    throwIfExportAborted(signal);
    const raw = await this.request<unknown>(hostedEncodedRoutePath("account_export_get", {"export": encodeURIComponent(id)}), 'GET', undefined, signal);
    throwIfExportAborted(signal);
    return parseAccountExportJob(raw);
  }
  async downloadExport(id: string, signal?: AbortSignal): Promise<unknown> {
    throwIfExportAborted(signal);
    const data = await this.request(hostedEncodedRoutePath("account_export_download", {"export": encodeURIComponent(id)}), 'POST', hostedRouteBody("account_export_download", {}), signal);
    throwIfExportAborted(signal);
    return data;
  }
}

/** A cancellation a caller can tell apart from a failed or unfinished export. */
function throwIfExportAborted(signal?: AbortSignal): void {
  if (signal?.aborted) throw Object.assign(new Error('Account export stopped'), {name: 'AbortError'});
}

/** The default wait between polls, stopped with its timer and listener when the run is aborted. */
function exportSleep(signal?: AbortSignal): (ms: number) => Promise<void> {
  return ms => new Promise<void>((resolve, reject) => {
    const stop = () => Object.assign(new Error('Account export stopped'), {name: 'AbortError'});
    if (signal?.aborted) { reject(stop()); return; }
    let timer: ReturnType<typeof setTimeout> | undefined;
    const onAbort = () => { if (timer !== undefined) clearTimeout(timer); signal?.removeEventListener('abort', onAbort); reject(stop()); };
    timer = setTimeout(() => { signal?.removeEventListener('abort', onAbort); resolve(); }, ms);
    signal?.addEventListener('abort', onAbort, {once: true});
  });
}

/**
 * The whole export: request, wait until ready (polling with a growing interval, up to `maxWaitMs`),
 * download. Returns the document, or `pending` with the job when it's still being prepared (the
 * `account_export_ready` notification finishes it later). An aborted signal rejects with an
 * `AbortError` before any further request, including after an injected sleep settles.
 */
export async function runAccountExport(client: AccountNotificationsClient, operationId: string, o: {maxWaitMs?: number; sleep?: (ms: number) => Promise<void>; signal?: AbortSignal} = {}): Promise<{state: 'ready'; data: unknown; job: AccountExportJob} | {state: 'pending'; job: AccountExportJob}> {
  const signal = o.signal;
  const sleep = o.sleep ?? exportSleep(signal);
  throwIfExportAborted(signal);
  let job = await client.requestExport(operationId, signal);
  throwIfExportAborted(signal);
  let waited = 0, step = 1500;
  while (job.state !== 'ready' && waited < (o.maxWaitMs ?? 60_000)) {
    throwIfExportAborted(signal);
    await sleep(step);
    throwIfExportAborted(signal);
    waited += step;
    step = Math.min(step * 2, 10_000);
    job = await client.exportStatus(job.id, signal);
    throwIfExportAborted(signal);
  }
  if (job.state !== 'ready') return {state: 'pending', job};
  throwIfExportAborted(signal);
  const data = await client.downloadExport(job.id, signal);
  throwIfExportAborted(signal);
  return {state: 'ready', data, job};
}
