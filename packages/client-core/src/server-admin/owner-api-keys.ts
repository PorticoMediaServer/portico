import type {HttpLocalApi} from '../index.ts';
import type {Key} from '../../../contracts/src/next/api.ts';
import {callServer, serverPath} from '../query/server-api.ts';
import {apiScopes, type ApiScope} from '../api-access.ts';
import {randomId} from '../random-id.ts';
import {unreadableServerResponse} from '../server-messages.ts';

export type OwnerApiKey = Readonly<Omit<Key, 'scopes'> & {scopes: readonly ApiScope[]; state: 'active' | 'expired' | 'revoked'; revokedAt?: string}>;
export type OwnerApiKeyDraft = Readonly<{name: string; scopes: readonly ApiScope[]; expiresAt?: string}>;
export type OwnerKeyIssued = Readonly<{key: OwnerApiKey; secret?: string; note?: string}>;
type Failure = 'read' | 'create' | 'revoke';
type Snapshot = Readonly<{phase: 'idle' | 'loading' | 'ready' | 'error'; page?: {items: readonly OwnerApiKey[]; nextCursor?: string}; busy: boolean; pendingCreate: boolean; error?: Failure; problem?: string}>;
const empty: Snapshot = Object.freeze({phase: 'idle', busy: false, pendingCreate: false});
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
const invalid = (): never => {throw new Error(unreadableServerResponse);};
function key(value: unknown, newlyMade = false): OwnerApiKey {
 if (!object(value) || typeof value.id !== 'string' || !value.id || typeof value.name !== 'string' || !Array.isArray(value.scopes) || !value.scopes.every(scope => apiScopes.includes(scope)) || typeof value.createdAt !== 'string' || !Number.isFinite(Date.parse(value.createdAt))) return invalid();
 for (const field of ['lastUsedAt','expiresAt','revokedAt']) if (value[field] !== undefined && (typeof value[field] !== 'string' || !Number.isFinite(Date.parse(value[field] as string)))) return invalid();
 const state = newlyMade ? 'active' : value.state;
 if (state !== 'active' && state !== 'expired' && state !== 'revoked') return invalid();
 return Object.freeze({id: value.id, name: value.name, scopes: Object.freeze([...value.scopes]), createdAt: value.createdAt, state,
  ...(value.lastUsedAt ? {lastUsedAt: value.lastUsedAt} : {}), ...(value.expiresAt ? {expiresAt: value.expiresAt} : {}), ...(value.revokedAt ? {revokedAt: value.revokedAt} : {})}) as OwnerApiKey;
}

/** The owner's rebuilt API-key list and one-time issuance. Secrets never enter a snapshot. */
export class OwnerApiKeysSource {
 private state = empty;
 private readonly listeners = new Set<() => void>();
 private readonly life = new AbortController();
 private reading?: AbortController;
 private mutating = false;
 private intent?: {body: OwnerApiKeyDraft; id: string};
 constructor(private readonly api: Pick<HttpLocalApi, 'request' | 'requestRaw'>, private readonly isCurrent: () => boolean, private readonly reportError: (error: unknown) => void, private readonly operationId = randomId) {}
 private current = () => !this.life.signal.aborted && this.isCurrent();
 getSnapshot = () => this.current() ? this.state : empty;
 subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
 private publish(patch: Partial<Snapshot>) {if (this.current()) {this.state = Object.freeze({...this.state, ...patch});for (const listener of this.listeners) listener();}}
 private failure(error: unknown, kind: Failure) {
  const status = (error as {status?: number}).status;
  if (status === 401 || status === 403) {this.reading?.abort();this.intent = undefined;this.life.abort();this.state = empty;for (const listener of this.listeners) listener();this.reportError(error);}
  else this.publish({phase: 'error', error: kind, problem: error instanceof Error ? error.message : undefined});
 }
 load = async (cursor = '', keepError?: Failure): Promise<void> => {
  if (!this.current() || this.mutating) return;
  this.reading?.abort();const reading = new AbortController();this.reading = reading;
  const signal = AbortSignal.any([this.life.signal, reading.signal]);
  this.publish({phase: 'loading', error: keepError, ...(keepError ? {} : {problem: undefined})});
  try {
   if (!this.current() || reading.signal.aborted) return;
   const answer = await callServer(this.api, 'GET /v1/admin/api-keys', {query: {cursor}, signal});
   if (!this.current() || reading.signal.aborted) return;
   if (!Array.isArray(answer.items) || answer.nextCursor !== undefined && typeof answer.nextCursor !== 'string') return invalid();
   this.publish({phase: 'ready', page: {items: Object.freeze(answer.items.map(item => key(item))), nextCursor: answer.nextCursor}, error: keepError});
  } catch (error) {if (this.current() && !reading.signal.aborted) this.failure(error, 'read');}
  finally {if (this.reading === reading) this.reading = undefined;}
 };
 create = async (draft: OwnerApiKeyDraft): Promise<OwnerKeyIssued | undefined> => {
  if (!this.current() || this.mutating || this.intent || (this.state.phase !== 'ready' && this.state.error !== 'create')) return;
  const body = {...draft, name: draft.name.trim(), scopes: [...new Set(draft.scopes)]};
  if (!body.name || body.name.length > 120 || !body.scopes.length || body.scopes.some(scope => !apiScopes.includes(scope))) return;
  this.intent = {body, id: this.operationId()};return this.retryCreate();
 };
 retryCreate = async (): Promise<OwnerKeyIssued | undefined> => {
  const intent = this.intent;if (!this.current() || this.mutating || !intent) return;
  this.mutating = true;this.reading?.abort();this.publish({busy: true, pendingCreate: true, error: undefined, problem: undefined});
  let issued: OwnerKeyIssued | undefined;
  try {
   if (!this.current()) return;
   const answer = await this.api.requestRaw(serverPath('POST /v1/admin/api-keys'), 'POST', {body: intent.body, headers: {'Idempotency-Key': intent.id}, signal: this.life.signal, singleAttempt: true});
   if (!this.current()) return;
   if (answer.status >= 400) {
    const error = object(answer.body) && object(answer.body.error) ? answer.body.error : {};
    if (answer.status >= 400 && answer.status < 500) {this.intent = undefined;this.publish({pendingCreate: false});}
    throw Object.assign(new Error(typeof error.message === 'string' ? error.message : unreadableServerResponse), {status: answer.status, code: error.code});
   }
   if (!object(answer.body) || answer.body.secret !== undefined && typeof answer.body.secret !== 'string' || answer.body.note !== undefined && typeof answer.body.note !== 'string') return invalid();
   if (!answer.body.secret && !answer.body.note) return invalid();
   const made = key(answer.body.key, true);
   if (made.name !== intent.body.name || [...made.scopes].sort().join(',') !== [...intent.body.scopes].sort().join(',') || (made.expiresAt === undefined ? intent.body.expiresAt !== undefined : Date.parse(made.expiresAt) !== Date.parse(intent.body.expiresAt ?? ''))) return invalid();
   issued = {key: made, secret: answer.body.secret as string | undefined, note: answer.body.note as string | undefined};
   this.intent = undefined;this.publish({pendingCreate: false});
  } catch (error) {if (this.current()) this.failure(error, 'create');}
  finally {this.mutating = false;this.publish({busy: false});}
  if (issued && this.current()) await this.load();
  return this.current() ? issued : undefined;
 };
 revoke = async (target: OwnerApiKey): Promise<void> => {
  if (!this.current() || this.mutating || !this.state.page?.items.includes(target) || target.state !== 'active') return;
  this.mutating = true;this.reading?.abort();this.publish({busy: true, error: undefined, problem: undefined});let failed = false;
  try {if (!this.current()) return;await callServer(this.api, 'DELETE /v1/admin/api-keys/{id}', {id: target.id, signal: this.life.signal});}
  catch (error) {if (this.current()) {failed = true;this.failure(error, 'revoke');}}
  finally {this.mutating = false;this.publish({busy: false});}
  if (this.current()) await this.load('', failed ? 'revoke' : undefined);
 };
 dispose = () => {this.reading?.abort();this.intent = undefined;this.life.abort();this.state = empty;this.listeners.clear();};
}
