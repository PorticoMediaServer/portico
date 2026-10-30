import {revealApiKey} from '../integration-access.ts';
import {ApiAccessError, createApiKey, getApiAccessOptions, listApiKeys, revokeApiKey, type ApiAccessOptions, type ApiAccessTransport, type ApiCredential, type ApiCredentialPage, type IssuedApiCredential, type NewApiCredential} from '../api-access.ts';
import {randomId} from '../random-id.ts';

export type ApiKeyDraft = Omit<NewApiCredential, 'operationId'>;
type Failure = 'read'|'create'|'revoke';
type Mutation = Readonly<{kind: 'create'|'revoke'; controller: AbortController}>;
export type ApiKeysSnapshot = Readonly<{phase: 'idle'|'loading'|'ready'|'error'; options?: ApiAccessOptions; page?: ApiCredentialPage; busy: boolean; pendingCreate: boolean; error?: Failure}>;
const empty: ApiKeysSnapshot = Object.freeze({phase: 'idle', busy: false, pendingCreate: false});
const terminal = (error: unknown) => {
  const {status, code} = (error && typeof error === 'object' ? error : {}) as {status?: number; code?: string};
  return status === 401 || status === 403 || ['unauthorized', 'forbidden', 'not_permitted'].includes(code || '');
};
const scopesKey = (scopes: readonly string[]) => [...new Set(scopes)].sort().join(',');
function matchingIssue(issued: IssuedApiCredential, intent: NewApiCredential): boolean {
  const key = issued.credential;
  return key.kind === 'key' && key.name.trim() === intent.name.trim() && scopesKey(key.scopes) === scopesKey(intent.scopes)
    && (intent.expiresAt === undefined || Date.parse(key.expiresAt ?? '') === Date.parse(intent.expiresAt));
}

/** Captured owner/server authority. Lists contain no secret; an issued secret is
 * returned only to the current caller and is never stored in a snapshot. */
export class SettingsApiKeysSource {
  private state = empty;
  private readonly listeners = new Set<() => void>();
  private readonly lifetime = new AbortController();
  private read?: AbortController;
  private mutation?: Mutation;
  private intent?: NewApiCredential;
  private readonly reveals = new Set<AbortController>();
  constructor(private readonly api: ApiAccessTransport, private readonly isCurrent: () => boolean, private readonly reportError: (error: unknown) => void, private readonly operationId: () => string = randomId) {}
  private current() {return !this.lifetime.signal.aborted && this.isCurrent();}
  private ownsRead(read: AbortController) {return this.current() && this.read === read && !read.signal.aborted && !this.mutation;}
  private ownsMutation(attempt: Mutation) {return this.current() && this.mutation === attempt && !attempt.controller.signal.aborted;}
  private transport(owns: () => boolean): ApiAccessTransport {
    return {request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => owns()
      ? this.api.request<T>(path, method, body, signal)
      : Promise.reject(Object.assign(new Error('The credential scope ended.'), {name: 'AbortError'}))};
  }
  getSnapshot = (): ApiKeysSnapshot => this.current() ? this.state : empty;
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private publish(patch: Partial<ApiKeysSnapshot>) {if (this.current()) {this.state = Object.freeze({...this.state, ...patch});for (const listener of this.listeners) listener();}}
  private failure(error: unknown, kind: Failure) {
    if (terminal(error)) {
      for (const request of this.reveals) request.abort();this.reveals.clear();
      this.read?.abort();this.mutation?.controller.abort();this.mutation = undefined;this.intent = undefined;
      this.lifetime.abort();this.state = empty;for (const listener of this.listeners) listener();
      this.reportError(error);
    } else this.publish({phase: 'error', error: kind});
  }
  load = (cursor = '') => this.readPage(cursor);
  private async readPage(cursor = '', keepError?: Failure): Promise<void> {
    if (!this.current() || this.mutation) return;
    this.read?.abort();const read = new AbortController();this.read = read;
    this.publish({phase: 'loading', error: keepError});
    if (!this.ownsRead(read)) return;
    const api = this.transport(() => this.ownsRead(read));
    try {
      const [page, options] = await Promise.all([listApiKeys(api, cursor, read.signal), getApiAccessOptions(api, read.signal)]);
      if (!this.ownsRead(read)) return;
      if (page.items.some(item => item.kind !== 'key')) throw new ApiAccessError('invalid_document');
      this.publish({phase: 'ready', page, options, error: keepError});
    } catch (error) {if (this.ownsRead(read)) this.failure(error, 'read');}
    finally {if (this.read === read) this.read = undefined;}
  }
  private claim(kind: Mutation['kind']): Mutation|undefined {
    if (!this.current() || this.mutation) return;
    this.read?.abort();this.read = undefined;
    const attempt = Object.freeze({kind, controller: new AbortController()});this.mutation = attempt;
    return attempt;
  }
  create = async (draft: ApiKeyDraft): Promise<IssuedApiCredential|undefined> => {
    if (!this.current() || this.mutation || this.intent || this.state.phase !== 'ready' || !this.state.options) return;
    const name = draft.name.trim(), scopes = [...new Set(draft.scopes)], offered = this.state.options.scopes;
    if (!name || name.length > 80 || !scopes.length || scopes.some(scope => !offered.includes(scope)) || (scopes.includes('admin') && scopes.length !== 1)) return;
    let intent: NewApiCredential;
    try {intent = Object.freeze({...draft, name, scopes: Object.freeze(scopes), operationId: this.operationId()});}
    catch (error) {if (this.current()) this.failure(error, 'create');return;}
    const attempt = this.claim('create');if (!attempt) return;
    this.intent = intent;
    // Private single flight is claimed before any subscriber can retry/re-enter.
    this.publish({busy: true, pendingCreate: true, error: undefined});
    return this.performCreate(attempt, intent);
  };
  retryCreate = async (): Promise<IssuedApiCredential|undefined> => {
    const intent = this.intent;if (!intent) return;
    const attempt = this.claim('create');if (!attempt) return;
    this.publish({busy: true, error: undefined});
    return this.performCreate(attempt, intent);
  };
  private async performCreate(attempt: Mutation, intent: NewApiCredential): Promise<IssuedApiCredential|undefined> {
    let issued: IssuedApiCredential|undefined;
    try {
      if (!this.ownsMutation(attempt)) return;
      const receipt = await createApiKey(this.transport(() => this.ownsMutation(attempt)), intent, attempt.controller.signal);
      if (!this.ownsMutation(attempt)) return;
      if (!matchingIssue(receipt, intent)) throw new ApiAccessError('invalid_document');
      issued = receipt;this.intent = undefined;this.publish({pendingCreate: false, page: undefined});
    } catch (error) {if (this.ownsMutation(attempt)) this.failure(error, 'create');}
    finally {if (this.mutation === attempt) {this.mutation = undefined;this.publish({busy: false});}}
    if (issued && this.current()) await this.readPage();
    return this.current() ? issued : undefined;
  }
  revoke = async (credential: ApiCredential): Promise<void> => {
    if (!this.current() || this.mutation || this.state.phase !== 'ready' || !this.state.page?.items.includes(credential) || credential.kind !== 'key' || !['active','awaiting_approval'].includes(credential.state)) return;
    const attempt = this.claim('revoke');if (!attempt) return;
    this.publish({busy: true, error: undefined});let failed = false;
    try {
      if (!this.ownsMutation(attempt)) return;
      const receipt = await revokeApiKey(this.transport(() => this.ownsMutation(attempt)), credential.id, attempt.controller.signal);
      if (!this.ownsMutation(attempt)) return;
      if (receipt.kind !== 'key' || receipt.id !== credential.id) throw new ApiAccessError('invalid_document');
      this.publish({page: undefined});
    } catch (error) {if (this.ownsMutation(attempt)) {failed = true;this.failure(error, 'revoke');}}
    finally {if (this.mutation === attempt) {this.mutation = undefined;this.publish({busy: false});}}
    if (this.current()) await this.readPage('', failed ? 'revoke' : undefined);
  };
  reveal = async (credential: ApiCredential): Promise<string|undefined> => {
    if (!this.current() || this.state.busy || !this.state.page?.items.includes(credential) || credential.kind !== 'key' || credential.state !== 'active') return;
    const request = new AbortController();this.reveals.add(request);
    const owns = () => this.current() && !request.signal.aborted && this.reveals.has(request) && !!this.state.page?.items.includes(credential);
    try {const secret = await revealApiKey(this.transport(owns), credential.id, request.signal);return owns() ? secret : undefined;}
    catch (error) {if (owns()) this.failure(error, 'read');return undefined;}
    finally {this.reveals.delete(request);}
  };
  dispose = () => {for (const request of this.reveals) request.abort();this.reveals.clear();this.read?.abort();this.mutation?.controller.abort();this.mutation = undefined;this.intent = undefined;this.lifetime.abort();this.state = empty;this.listeners.clear();};
}
