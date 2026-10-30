import {throwIfAborted} from './abort-lifetime.ts';
import {routePath} from '../../contracts/src/route-paths.ts';
import type {InboxApi} from './inbox.ts';
import {parseNotificationBatchResult, parseNotificationInbox, parseNotificationSummary, parseNotificationVocabulary, type NotificationVocabulary} from './notifications.ts';
import {consoleUTF8Size} from './console-transport.ts';

export type NotificationScopeLifetime = Readonly<{
 serverId: string;
 signal: AbortSignal;
 isCurrent: () => boolean;
 /** Retires the original consumer on a changed envelope or lost authority. */
 onRetired?: () => void;
}>;
export type ScopedNotifications = InboxApi & Readonly<{
 vocabulary(signal?: AbortSignal): Promise<NotificationVocabulary>;
 dispose(): void;
}>;
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
const invalid = () => Object.assign(new Error('Invalid notification document.'), {code: 'invalid_document'});
const ended = () => Object.assign(new Error('Notification scope retired.'), {name: 'AbortError'});
const audience = (value: unknown): value is string => value === 'profile' || value === 'account-admin' || value === 'all';

/** One original API/server/viewer lifetime for the existing notification routes.
 * Its fence belongs only to notification envelopes; it is never compared with
 * a Home/catalogue fence and never grants an action or another audience. */
export function createNotificationScope(api: InboxApi, lifetime: NotificationScopeLifetime): ScopedNotifications {
 const serverId = lifetime.serverId, signal = lifetime.signal, current = lifetime.isCurrent, onRetired = lifetime.onRetired;
 if (typeof serverId !== 'string' || !serverId || serverId.length > 160 || /[\u0000-\u001f\u007f-\u009f]/u.test(serverId)) throw invalid();
 const originalRequest = api.request.bind(api);
 let closed = false, fence: string | undefined;
 const requests = new Set<AbortController>();
 const live = () => {try {return !closed && !signal.aborted && current();} catch {return false;}};
 const dispose = () => {
  if (closed) return;
  closed = true;signal.removeEventListener('abort', dispose);
  for (const request of requests) request.abort();
  requests.clear();
 };
 const check = () => {if (!live()) {dispose();throw ended();}};
 const retire = () => {
  const notify = !closed;
  dispose();
  if (notify) {try {onRetired?.();} catch {/* A sink cannot publish a refused document. */}}
  return Object.assign(new Error('Notification scope changed.'), {code: 'access_refused'});
 };
 signal.addEventListener('abort', dispose, {once: true});
 if (signal.aborted) dispose();
 const request: InboxApi['request'] = async <T>(path: string, method = 'GET', body?: unknown, callerSignal?: AbortSignal): Promise<T> => {
  check();throwIfAborted(callerSignal);
  if (typeof path !== 'string' || path.length > 8192) throw invalid();
  const split = path.indexOf('?'), route = split < 0 ? path : path.slice(0, split);
  const query = new URLSearchParams(split < 0 ? '' : path.slice(split + 1));
  const inbox = route === routePath('get_notifications_inbox'), unread = route === routePath('get_notifications_unread_count');
  const vocabulary = route === routePath('get_notifications_capabilities'), action = route === routePath('post_notifications_inbox_actions');
  if ((!inbox && !unread && !vocabulary && !action) || method !== (action ? 'POST' : 'GET') || path.includes('#')) throw invalid();
  for (const [key] of query) {
   if (query.getAll(key).length !== 1 || !(inbox ? ['audience', 'state', 'cursor', 'limit'] : unread ? ['audience'] : []).includes(key)) throw invalid();
  }
  let capturedBody: unknown, requestedAudience: string | undefined;
  if (action) {
   let encoded: string | undefined;
   try {encoded = JSON.stringify(body);} catch {throw invalid();}
   if (!encoded || consoleUTF8Size(encoded) > 65536) throw invalid();
   capturedBody = JSON.parse(encoded) as unknown;
   if (!object(capturedBody) || !audience(capturedBody.audience)) throw invalid();
   requestedAudience = capturedBody.audience;
  } else {
   if (body !== undefined) throw invalid();
   if (inbox || unread) {
    requestedAudience = query.get('audience') ?? 'profile';
    if (!audience(requestedAudience)) throw invalid();
   }
  }
  const owned = new AbortController(), abort = () => owned.abort();
  const signals = [signal, ...(callerSignal ? [callerSignal] : [])];
  for (const parent of signals) {parent.addEventListener('abort', abort, {once: true});if (parent.aborted) abort();}
  requests.add(owned);
  try {
   check();throwIfAborted(owned.signal);
   let raw: unknown;
   try {raw = await originalRequest<T>(path, method, capturedBody, owned.signal);} catch (error) {
    check();throwIfAborted(owned.signal);
    if (object(error) && (error.status === 401 || error.status === 403)) retire();
    throw error;
   }
   check();throwIfAborted(owned.signal);
   if (!object(raw) || !object(raw.scope) || !object(raw.data)) throw invalid();
   const scope = raw.scope;
   if (scope.serverId !== serverId) throw retire();
   if (typeof scope.viewerFence !== 'string' || !/^[a-f0-9]{64}$/.test(scope.viewerFence)) throw invalid();
   if (fence !== undefined && fence !== scope.viewerFence) throw retire();
   if (requestedAudience !== undefined && raw.data.audience !== requestedAudience) throw invalid();
   // A malformed first payload cannot admit its viewer fence. Keep each
   // canonical parser's tolerant-item/strict-document policy, then return the
   // original envelope for the service's normal adoption.
   try {
    if(inbox)parseNotificationInbox(raw.data);
    else if(unread)parseNotificationSummary(raw.data);
    else if(action)parseNotificationBatchResult(raw.data);
    else parseNotificationVocabulary(raw.data);
   } catch (error) {
    check();throwIfAborted(owned.signal);
    // Only the canonical parsers' shared validation refusal is a definite
    // document error. Transport, authority, cancellation and programmer errors
    // never acquire this classification or admit the envelope's fence.
    if(error instanceof Error && error.constructor===Error && error.message==='Invalid notification response.') throw invalid();
    throw error;
   }
   fence = scope.viewerFence;
   check();throwIfAborted(owned.signal);
   return raw as T;
  } finally {
   for (const parent of signals) parent.removeEventListener('abort', abort);
   requests.delete(owned);
  }
 };
 return Object.freeze({request, dispose,
  vocabulary: async (callerSignal?: AbortSignal) => {
   const raw = await request<unknown>(routePath('get_notifications_capabilities'), 'GET', undefined, callerSignal);
   check();throwIfAborted(callerSignal);
   if (!object(raw)) throw invalid();
   const value = parseNotificationVocabulary(raw.data);
   check();throwIfAborted(callerSignal);return value;
  },
 });
}
