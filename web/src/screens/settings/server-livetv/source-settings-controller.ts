import type {LibraryContentApi} from '@core/library-content.ts';
import {parseEnvelope, parseLiveSourceDocument, type LiveSourceDocument, type LiveSourceSettings} from '@core/administration.ts';
import {sameUserMutationScope, type UserMutationCoordinator, type UserMutationScope} from '@core/user-mutations.ts';
import {rebaseDraft, type QuietRefreshOutcome} from '@core/freshness-actions.ts';
import {lanConfirmationRoots} from '@core/linear-api.ts';
import {confirmRoots} from '../../live/owner/checked';
import {channelRequestID} from '@core/live-source-draft.ts';
import {OwnerStore, errorFacts} from '../../live/owner/lifetime';
type SourceSettingsApi = LibraryContentApi & {
  acknowledgeUserMutation?(intentId: string): Promise<void>;
  awaitUserMutationsReady?(): Promise<void>;
  getUserMutationCoordinator?(): UserMutationCoordinator | undefined;
  getUserMutationScope?(): UserMutationScope | undefined;
};
type Command = {body: Readonly<{expectedRevision: number; operationId: string; settings: LiveSourceSettings}>; binding?: UserMutationScope; retained: boolean};
function freeze<T>(value: T): T {
  if (value && typeof value === 'object') {for (const child of Object.values(value)) freeze(child);Object.freeze(value);}
  return value;
}
export type SourceSettingsSnapshot = Readonly<{loading: boolean; saving: boolean; document?: LiveSourceDocument; draft?: LiveSourceSettings; error?: unknown; saved?: boolean; logoBusy?: boolean; logoResult?: Readonly<{requested: number; imported: number; skipped: number}>}>;

/** One mounted source configuration, over the existing administration document.
 * The list remains the sole source directory; this model never invents one. */
export class SourceSettingsController extends OwnerStore<SourceSettingsSnapshot> {
  private quietSerial = 0;
  private quietRead?: AbortController;
  private uncertain = false;
  quietBlocked = () => this.snapshot.loading || this.snapshot.saving || !!this.snapshot.logoBusy || this.uncertain || !!this.command;
  invalidateQuiet = () => {++this.quietSerial;this.quietRead?.abort();this.quietRead = undefined;};
  private operations = new Map<string, string>();
  private command?: Command;
  constructor(private api: SourceSettingsApi, private serverId: string, readonly sourceId: string, current: () => boolean,
    reportError: (error: unknown) => void, private makeId = channelRequestID) {
    super(Object.freeze({loading: false, saving: false}), current, reportError);
    if (!/^[a-f0-9]{48}$/.test(sourceId)) throw new Error('live_source_identity_invalid');
  }
  private path() {return `/v1/admin/live-sources/${encodeURIComponent(this.sourceId)}/configuration`;}
  private checked(raw: unknown) {
    const doc = parseEnvelope(raw, this.serverId, parseLiveSourceDocument).result;
    if (doc.sourceId !== this.sourceId) throw new Error('live_source_identity_changed');
    return doc;
  }
  private async reconcileCommand(): Promise<'released' | 'not-queued' | undefined> {
    const command = this.command, coordinator = this.api.getUserMutationCoordinator?.();
    if (!command || !coordinator || !command.binding) return;
    await this.api.awaitUserMutationsReady?.();
    const scope = this.api.getUserMutationScope?.();
    if (!scope || !sameUserMutationScope(scope, command.binding)) throw {code: 'mutation_scope_changed'};
    const retained = await coordinator.inspectIntent(command.body.operationId);
    if (this.command !== command || !this.isActive()) throw {code: 'cancelled'};
    if (retained) {command.retained = true;return;}
    this.command = undefined;
    return command.retained ? 'released' : 'not-queued';
  }
  refreshQuietly = async (): Promise<QuietRefreshOutcome> => {
    if (!this.isActive() || this.quietBlocked()) return {kind: 'deferred'};
    this.invalidateQuiet();const serial = this.quietSerial, read = this.quietRead = new AbortController(), base = this.snapshot.document;
    const owns = () => this.isActive() && !read.signal.aborted && serial === this.quietSerial && !this.quietBlocked() && this.snapshot.document === base;
    try {
      const raw = await this.api.request(this.path(), 'GET', undefined, read.signal);
      if (!owns()) return {kind: 'deferred'};
      const document = this.checked(raw), local = this.snapshot.draft;
      const rebased = base && local ? rebaseDraft(base.settings, local, document.settings) : {merged: document.settings, conflict: false};
      if (rebased.conflict) return {kind: 'conflict'};
      this.publish({...this.snapshot, document, draft: rebased.merged});
      return {kind: 'adopted'};
    } catch (error) {
      if (!owns()) return {kind: 'deferred'};
      if ([401,403].includes(errorFacts(error).status ?? 0)) this.fail(error, this.snapshot);
      return {kind: 'unreadable', error};
    } finally {if (this.quietRead === read) this.quietRead = undefined;};
  };
  async load() {
    if (!this.isActive() || this.snapshot.saving || this.snapshot.logoBusy) return;
    this.invalidateQuiet();const attempt = this.begin();this.publish({...this.snapshot, loading: true, error: undefined});if (!this.valid(attempt)) return;
    try {
      await this.reconcileCommand();if (!this.valid(attempt)) return;
      if (this.command) {this.publish({...this.snapshot, loading: false});return;}
      const raw = await this.api.request(this.path(), 'GET', undefined, attempt.abort.signal);if (!this.valid(attempt)) return;
      const document = this.checked(raw);this.publish({loading: false, saving: false, document, draft: document.settings});
    } catch (error) {if (this.valid(attempt)) this.fail(error, {...this.snapshot, loading: false, error: errorFacts(error)});}
  }
  edit(patch: Partial<LiveSourceSettings>) {
    if (!this.isActive() || this.snapshot.saving || this.snapshot.logoBusy || !this.snapshot.document || !this.snapshot.draft) return;
    const parsed = parseLiveSourceDocument({...this.snapshot.document, settings: {...this.snapshot.draft, ...patch}}).settings;
    const remote = patch.remote ?? this.snapshot.draft.remote;
    const draft = {...parsed, ...(remote ? {remote: {...parsed.remote!,
      ...(remote.username === undefined ? {} : {username: remote.username}),
      ...(remote.password === undefined ? {} : {password: remote.password}),
      ...(remote.confirmedLanRoots ? {confirmedLanRoots: remote.confirmedLanRoots} : {}),
    }} : {})};
    this.publish({...this.snapshot, draft, error: undefined, saved: false});
  }
  discard() {if (this.isActive() && !this.snapshot.saving && this.snapshot.document) {this.invalidateQuiet();this.publish({...this.snapshot, draft: this.snapshot.document.settings, error: undefined});}}
  async save() {
    const {document, draft} = this.snapshot;
    if (!this.isActive() || this.snapshot.loading || this.snapshot.saving || this.snapshot.logoBusy || !document || !draft || JSON.stringify(document.settings) === JSON.stringify(draft)) return false;
    this.invalidateQuiet();const attempt = this.begin();this.publish({...this.snapshot, saving: true, error: undefined});
    try {
      if (!this.valid(attempt)) return false;
      const coordinator = this.api.getUserMutationCoordinator?.();
      if (coordinator) {await this.api.awaitUserMutationsReady?.();await coordinator.reconcile();}
      if (!this.valid(attempt)) return false;
      const released = await this.reconcileCommand();if (!this.valid(attempt)) return false;
      if (released === 'released') {
        const fresh = this.checked(await this.api.request(this.path(), 'GET', undefined, attempt.abort.signal));
        if (this.valid(attempt)) this.publish({loading: false, saving: false, document: fresh, draft: fresh.settings});
        return false; // Review current state before authoring a replacement.
      }
      const scope = this.api.getUserMutationScope?.();
      if (this.command?.binding && (!scope || !sameUserMutationScope(scope, this.command.binding))) throw {code: 'mutation_scope_changed'};
      const retained = coordinator?.snapshot().find(entry => scope && sameUserMutationScope(entry.scope, scope) && entry.path === this.path() && entry.method === 'PUT');
      // Only durable acknowledgement or the guarded Pending changes review can
      // release an accepted command. Loading, editing and conflicts cannot.
      if (retained && retained.intentId !== this.command?.body.operationId) throw {code: 'mutation_needs_review'};
      if (this.command && JSON.stringify({expectedRevision: document.revision, settings: draft}) !== JSON.stringify({expectedRevision: this.command.body.expectedRevision, settings: this.command.body.settings})) throw {code: 'mutation_needs_review'};
      if (!this.command) this.command = {body: freeze(JSON.parse(JSON.stringify({expectedRevision: document.revision, operationId: this.makeId(), settings: draft}))), ...(scope ? {binding: {...scope}} : {}), retained: false};
      const command = this.command;
      const saved = this.checked(await this.api.request(this.path(), 'PUT', command.body, attempt.abort.signal));
      command.retained = true;
      if (!this.valid(attempt)) return false;
      const live = this.api.getUserMutationScope?.();
      if (command.binding && (!live || !sameUserMutationScope(live, command.binding))) throw {code: 'mutation_scope_changed'};
      await this.api.acknowledgeUserMutation?.(command.body.operationId);
      if (!this.valid(attempt)) return false;
      this.command = undefined;this.uncertain = false;this.operations.clear();this.publish({loading: false, saving: false, document: saved, draft: saved.settings, saved: true});return this.valid(attempt);
    } catch (error) {
      const command = this.command;
      if (command && this.api.getUserMutationCoordinator?.()?.snapshot().some(entry => entry.intentId === command.body.operationId && command.binding && sameUserMutationScope(entry.scope, command.binding))) command.retained = true;
      this.uncertain = ![400,401,403,409,412,422].includes(errorFacts(error).status ?? 0);
      let roots: readonly string[] | undefined;
      try {const candidate = lanConfirmationRoots(error);roots = candidate ? confirmRoots(candidate) : undefined;} catch {roots = undefined;}
      if (this.valid(attempt)) this.fail(error, {...this.snapshot, saving: false, error: {...errorFacts(error), ...(roots ? {confirmRoots: roots} : {})}});
      return false;
    }
  }
  async importLogos() {
    const doc = this.snapshot.document;
    if (!this.isActive() || !doc || this.snapshot.loading || this.snapshot.saving || this.snapshot.logoBusy || !doc.effectiveLogoImport.enabled || JSON.stringify(doc.settings) !== JSON.stringify(this.snapshot.draft)) return;
    this.invalidateQuiet();const attempt = this.begin(), key = `logos:${this.sourceId}:${doc.revision}`;
    let operationId = this.operations.get(key);if (!operationId) {operationId = this.makeId();this.operations.set(key, operationId);}
    this.publish({...this.snapshot, logoBusy: true, logoResult: undefined, error: undefined});if (!this.valid(attempt)) return;
    try {
      const raw = await this.api.request(`/v1/admin/live-sources/${encodeURIComponent(this.sourceId)}/logos`, 'POST', {operationId}, attempt.abort.signal);
      if (!this.valid(attempt)) return;
      const result = parseEnvelope(raw, this.serverId, value => {
        if (!value || typeof value !== 'object') throw {code: 'invalid_response'};
        const v = value as Record<string, unknown>, number = (n: unknown) => {if (typeof n !== 'number' || !Number.isSafeInteger(n) || n < 0 || n > 100000) throw {code: 'invalid_response'};return n;};
        return Object.freeze({requested: number(v.requested), imported: number(v.imported), skipped: number(v.skipped)});
      }).result;
      this.uncertain = false;this.operations.delete(key);this.publish({...this.snapshot, logoBusy: false, logoResult: result});
    } catch (error) {this.uncertain = ![400,401,403,409,412,422].includes(errorFacts(error).status ?? 0);if (this.valid(attempt)) this.fail(error, {...this.snapshot, logoBusy: false, error: errorFacts(error)});}
  }
  override dispose() {this.invalidateQuiet();this.command = undefined;this.operations?.clear();super.dispose();}
}
