import {throwIfAborted} from './abort-lifetime.ts';
import {serverPath} from './query/server-api.ts';
import type {InboxApi} from './inbox.ts';
import {consoleUTF8Size} from './console-transport.ts';

export type NotificationScopeLifetime = Readonly<{
 serverId: string;
 signal: AbortSignal;
 isCurrent: () => boolean;
 /** Retires the original consumer after a definite access refusal. */
 onRetired?: () => void;
}>;
export type ScopedNotifications = InboxApi & Readonly<{
 dispose(): void;
}>;
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
const invalid = () => Object.assign(new Error('Invalid notification document.'), {code: 'invalid_document'});
const ended = () => Object.assign(new Error('Notification scope retired.'), {name: 'AbortError'});

/** Keep notice requests inside the captured shell lifetime; the notice service decodes replies. */
export function createNotificationScope(api: InboxApi, lifetime: NotificationScopeLifetime): ScopedNotifications {
 const serverId = lifetime.serverId, signal = lifetime.signal, current = lifetime.isCurrent, onRetired = lifetime.onRetired;
 if (typeof serverId !== 'string' || !serverId || serverId.length > 160 || /[\u0000-\u001f\u007f-\u009f]/u.test(serverId)) throw invalid();
 const originalRequest = api.request.bind(api);
 let closed = false;
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
  const inbox = route === serverPath('GET /v1/notifications');
  const readAll = route === serverPath('POST /v1/notifications/read-all');
  const prefix = serverPath('GET /v1/notifications') + '/';
  const change = route.startsWith(prefix) && /^[A-Za-z0-9_-]+$/.test(route.slice(prefix.length)) && !readAll;
  if (path.includes('#') || !(inbox && method === 'GET' || readAll && method === 'POST' || change && method === 'PATCH')) throw invalid();
  for (const [key] of query) if (!inbox || !['view', 'cursor', 'limit'].includes(key) || query.getAll(key).length !== 1) throw invalid();
  if (query.has('view') && !['unread', 'all', 'archived'].includes(query.get('view')!)) throw invalid();
  let capturedBody: unknown;
  if (change) {
   let encoded: string | undefined;
   try {encoded = JSON.stringify(body);} catch {throw invalid();}
   if (!encoded || consoleUTF8Size(encoded) > 65536) throw invalid();
   capturedBody = JSON.parse(encoded);
   if (!object(capturedBody) || !Object.keys(capturedBody).length || Object.entries(capturedBody).some(([key, value]) => !['read', 'archived'].includes(key) || typeof value !== 'boolean')) throw invalid();
  } else if (body !== undefined) throw invalid();
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
   return raw as T;
  } finally {
   for (const parent of signals) parent.removeEventListener('abort', abort);
   requests.delete(owned);
  }
 };
 return Object.freeze({request, dispose});
}
