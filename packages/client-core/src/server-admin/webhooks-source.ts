import {webRouteCrypto} from '../route-identity.ts';
import {storageOperationId} from './panel-logic.ts';
import {ApiAccessError, createWebhook, deleteWebhook, listWebhooks, listWebhookDeliveries, sendWebhookTest, updateWebhook, type ApiAccessTransport, type IssuedWebhook, type NewWebhook, type Webhook, type WebhookChange, type WebhookDeliveryPage, type WebhookList} from '../api-access.ts';

type Failure = 'read'|'write'|'deliveries';
export type WebhooksSnapshot = Readonly<{phase: 'idle'|'loading'|'ready'|'error'; list?: WebhookList; selected?: Webhook; deliveries?: WebhookDeliveryPage; busy: boolean; uncertain: boolean; error?: Failure}>;
const empty: WebhooksSnapshot = Object.freeze({phase: 'idle', busy: false, uncertain: false});
const terminal = (error: unknown) => {
  const {status, code} = (error && typeof error === 'object' ? error : {}) as {status?: number; code?: string};
  return status === 401 || status === 403 || ['unauthorized','forbidden','not_permitted'].includes(code ?? '');
};
const eventsKey = (events: readonly string[]) => [...new Set(events)].sort().join(',');
/** A bounded digest of the create body exactly as it will be sent: only the
 * fields present, and never the request itself. It uses the same audited
 * route crypto the web app registers, so a browser without WebCrypto still
 * digests; where neither is available the caller fails closed rather than
 * retrying under a fresh id. */
async function webhookRequestFingerprint(intent: NewWebhook): Promise<string | undefined> {
  const body = {
    name: intent.name, url: intent.url, events: [...intent.events],
    ...(intent.secret !== undefined ? {secret: intent.secret} : {}),
    ...(intent.enabled !== undefined ? {enabled: intent.enabled} : {}),
  };
  try {
    const digest = await webRouteCrypto.sha256(new TextEncoder().encode(JSON.stringify(body)));
    return [...new Uint8Array(digest)].map(value => value.toString(16).padStart(2, '0')).join('');
  } catch {return undefined;}
}
const matches = (hook: Webhook, input: WebhookChange) => (input.name === undefined || hook.name === input.name)
  && (input.url === undefined || hook.url === input.url) && (input.enabled === undefined || hook.enabled === input.enabled)
  && (input.events === undefined || eventsKey(hook.events) === eventsKey(input.events));

/** One owner/server lifetime. A webhook create keeps one operation id for its
 * exact request, so an uncertain response is retried as the same create: the
 * same webhook answers without its secret, a changed request is refused and a
 * webhook since deleted answers not found. Edits, removals and tests are not
 * replayed. */
export class SettingsWebhooksSource {
  private state = empty;
  private readonly listeners = new Set<() => void>();
  private readonly lifetime = new AbortController();
  private read?: AbortController;
  private detail?: AbortController;
  private mutation?: AbortController;
  private createAttempt?: Readonly<{fingerprint: string; operationId: string}>;
  // What this owner has done since a transfer, bound to the revision the
  // server confirmed. The server keeps the authority; this only orders the
  // existing surfaces a retired webhook needs.
  private reviewProgress = new Map<string, Readonly<{urlEvents: boolean; secret: boolean; revision: number}>>();
  constructor(private readonly api: ApiAccessTransport, private readonly isCurrent: () => boolean, private readonly reportError: (error: unknown) => void) {}
  private current = () => !this.lifetime.signal.aborted && this.isCurrent();
  private ownsRead = (read: AbortController) => this.current() && this.read === read && !read.signal.aborted && !this.mutation;
  private ownsWrite = (write: AbortController) => this.current() && this.mutation === write && !write.signal.aborted;
  private transport(owns: () => boolean): ApiAccessTransport {return {request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => owns() ? this.api.request<T>(path, method, body, signal) : Promise.reject(Object.assign(new Error('The webhook scope ended.'), {name: 'AbortError'}))};}
  getSnapshot = (): WebhooksSnapshot => this.current() ? this.state : empty;
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private publish(patch: Partial<WebhooksSnapshot>) {if (this.current()) {this.state = Object.freeze({...this.state, ...patch});for (const listener of this.listeners) listener();}}
  private failure(error: unknown, kind: Failure) {
    if (terminal(error)) {this.read?.abort();this.detail?.abort();this.mutation?.abort();this.mutation = undefined;this.lifetime.abort();this.state = empty;for (const listener of this.listeners) listener();this.reportError(error);}
    else this.publish({phase: 'error', error: kind});
  }
  load = async (): Promise<void> => {
    if (!this.current() || this.mutation) return;
    this.read?.abort();this.detail?.abort();const read = new AbortController();this.read = read;
    this.publish({phase: 'loading', error: undefined, selected: undefined, deliveries: undefined});
    if (!this.ownsRead(read)) return;
    try {
      const list = await listWebhooks(this.transport(() => this.ownsRead(read)), read.signal);
      if (!this.ownsRead(read)) return;
      for (const id of [...this.reviewProgress.keys()]) {
        const now = list.items.find(hook => hook.id === id);
        const held = this.reviewProgress.get(id);
        if (!now?.requiresOwnerReview || !held || now.revision !== held.revision) this.reviewProgress.delete(id);
      }
      this.publish({phase: 'ready', list});
    }
    catch (error) {if (this.ownsRead(read)) this.failure(error, 'read');}
    finally {if (this.read === read) this.read = undefined;}
  };
  /** Explicitly reconcile the list after an uncertain write. This only reads;
   * a later write requires a separate user action against this new list. */
  reconcile = async () => {await this.load();if (this.current() && this.state.phase === 'ready') this.publish({uncertain: false});};
  select = async (hook: Webhook, cursor = ''): Promise<void> => {
    if (!this.current() || this.mutation || !this.state.list?.items.includes(hook)) return;
    this.detail?.abort();const detail = new AbortController();this.detail = detail;
    const owns = () => this.current() && this.detail === detail && !detail.signal.aborted && this.state.selected === hook && this.state.list?.items.includes(hook) === true && !this.mutation;
    this.publish({selected: hook, deliveries: undefined, error: undefined});if (!owns()) return;
    try {const deliveries = await listWebhookDeliveries(this.transport(owns), hook.id, {cursor: cursor || undefined, limit: 50}, detail.signal);if (owns()) this.publish({deliveries});}
    catch (error) {if (owns()) this.failure(error, 'deliveries');}
  };
  close = () => {this.detail?.abort();this.detail = undefined;this.publish({selected: undefined, deliveries: undefined});};
  private claim(hook?: Webhook): AbortController | undefined {
    if (!this.current() || this.mutation || this.state.uncertain || this.state.phase !== 'ready' || !this.state.list || hook && !this.state.list.items.includes(hook)) return;
    this.read?.abort();this.detail?.abort();const write = new AbortController();this.mutation = write;
    this.publish({busy: true, error: undefined});return write;
  }
  private async write<T>(operation: (api: ApiAccessTransport, signal: AbortSignal, owns: () => boolean) => Promise<T>, hook?: Webhook): Promise<T | undefined> {
    const write = this.claim(hook);if (!write) return;
    const owns = () => this.ownsWrite(write);
    let result: T | undefined;
    try {if (!owns()) return;result = await operation(this.transport(owns), write.signal, owns);if (!owns()) return;}
    catch (error) {if (this.ownsWrite(write)) {this.publish({uncertain: true});this.failure(error, 'write');}}
    finally {if (this.mutation === write) {this.mutation = undefined;this.publish({busy: false});}}
    if (result !== undefined && this.current()) await this.load();
    return this.current() ? result : undefined;
  }
  create = async (input: NewWebhook): Promise<IssuedWebhook | undefined> => {
    const name = input.name.trim(), url = input.url.trim(), events = Object.freeze([...new Set(input.events)]);
    if (!name || name.length > 160 || !events.length || events.some(event => !this.state.list?.events.includes(event)) || this.state.list && this.state.list.items.length >= this.state.list.maximum) return;
    try {const parsed = new URL(url);if (!['http:','https:'].includes(parsed.protocol) || parsed.username || parsed.password) return;} catch {return;}
    const intent = Object.freeze({...input, name, url, events});
    return this.write(async (api, signal, owns) => {
      // The attempt belongs to this claimed write: an ignored create never
      // touches the id a pending uncertain one still needs.
      // No digest means no safe retry identity: refuse rather than create
      // something a retry could duplicate.
      // No digest means no safe retry identity: refuse visibly, under this
      // write, rather than create something a retry could silently duplicate.
      const fingerprint = await webhookRequestFingerprint(intent);
      if (!owns() || signal.aborted) return;
      if (!fingerprint) throw new ApiAccessError('invalid_document');
      const operationId = this.createAttempt?.fingerprint === fingerprint ? this.createAttempt.operationId : storageOperationId();
      const attempt = Object.freeze({fingerprint, operationId});
      this.createAttempt = attempt;
      let issued: IssuedWebhook;
      try {issued = await createWebhook(api, intent, signal, operationId);}
      catch (error) {
        // A refused identity or a webhook since deleted is definite: the next
        // attempt is a new request, never this one repeated. Only this write's
        // own attempt, while it is still owned, is retired.
        const code = (error as {code?: string} | undefined)?.code;
        if (owns() && !signal.aborted && this.createAttempt === attempt && (code === 'idempotency_key_reused' || code === 'not_found')) this.createAttempt = undefined;
        throw error;
      }
      if (!owns() || signal.aborted) return;
      if (!matches(issued.webhook, intent)) throw new ApiAccessError('invalid_document');
      if (this.createAttempt === attempt) this.createAttempt = undefined;
      return issued;
    });
  };
  /** Which drawn step a retired webhook still needs here, for the revision the
   * server last confirmed. A different revision starts the review again. */
  reviewStep = (hook: Webhook): 'urlEvents'|'secret'|'done' => {
    if (!hook.requiresOwnerReview) return 'done';
    const held = this.reviewProgress.get(hook.id);
    if (!held || held.revision !== hook.revision) return 'urlEvents';
    return held.urlEvents ? held.secret ? 'done' : 'secret' : 'urlEvents';
  };
  /** Both review steps done here, so an enable may be offered; the server still decides. */
  reviewed = (hook: Webhook): boolean => this.reviewStep(hook) === 'done';
  update = async (hook: Webhook, change: WebhookChange): Promise<IssuedWebhook | undefined> => {
    const intent = Object.freeze({...change, ...(change.events ? {events: Object.freeze([...change.events])} : {})});
    return this.write(async (api, signal, owns) => {
      const issued = await updateWebhook(api, hook.id, hook.revision, intent, signal);
      // Nothing is recorded or compared once this exact write is no longer the
      // source's current one: a retired writer cannot repopulate progress.
      if (!owns() || signal.aborted) return;
      if (issued.webhook.id !== hook.id || !matches(issued.webhook, intent)) throw new ApiAccessError('invalid_document');
      // Progress carries only from the revision this write was accepted
      // against; the answer already carries the next one. A refused review no
      // longer asks for one, so nothing is kept for it.
      const held = this.reviewProgress.get(hook.id);
      const previous = held && held.revision === hook.revision ? held : {urlEvents: false, secret: false};
      if (!issued.webhook.requiresOwnerReview) this.reviewProgress.delete(hook.id);
      else this.reviewProgress.set(hook.id, {urlEvents: previous.urlEvents || (intent.url !== undefined && intent.events !== undefined), secret: previous.secret || intent.secret !== undefined || intent.rotateSecret === true, revision: issued.webhook.revision});
      return issued;
    }, hook);
  };
  remove = (hook: Webhook) => this.write(async (api, signal) => {await deleteWebhook(api, hook.id, signal);return true;}, hook);
  test = (hook: Webhook) => this.write((api, signal) => sendWebhookTest(api, hook.id, signal), hook);
  dispose = () => {this.read?.abort();this.detail?.abort();this.mutation?.abort();this.mutation = undefined;this.createAttempt = undefined;this.reviewProgress.clear();this.lifetime.abort();this.state = empty;this.listeners.clear();};
}
