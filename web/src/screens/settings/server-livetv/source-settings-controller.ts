import {readOwnerSourceConfiguration, writeOwnerSourceConfiguration, importOwnerSourceLogos} from '@core/owner-requests.ts';
import type {LibraryContentApi} from '@core/library-content.ts';
import {parseEnvelope, parseLiveSourceDocument, type LiveSourceDocument, type LiveSourceSettings} from '@core/administration.ts';
import {rebaseDraft, type QuietRefreshOutcome} from '@core/freshness-actions.ts';
import {lanConfirmationRoots} from '@core/linear-api.ts';
import {confirmRoots} from '../../live/owner/checked';
import {channelRequestID} from '@core/live-source-draft.ts';
import {OwnerStore, errorFacts} from '../../live/owner/lifetime';
type SourceSettingsApi = LibraryContentApi;
export type SourceSettingsSnapshot = Readonly<{loading: boolean; saving: boolean; document?: LiveSourceDocument; draft?: LiveSourceSettings; error?: unknown; saved?: boolean; logoBusy?: boolean; logoResult?: Readonly<{requested: number; imported: number; skipped: number}>}>;

/** One mounted source configuration, over the existing administration document.
 * The list remains the sole source directory; this model never invents one. */
export class SourceSettingsController extends OwnerStore<SourceSettingsSnapshot> {
  private quietSerial = 0;
  private quietRead?: AbortController;
  private uncertain = false;
  quietBlocked = () => this.snapshot.loading || this.snapshot.saving || !!this.snapshot.logoBusy || this.uncertain;
  invalidateQuiet = () => {++this.quietSerial;this.quietRead?.abort();this.quietRead = undefined;};
  private operations = new Map<string, string>();
  constructor(private api: SourceSettingsApi, private serverId: string, readonly sourceId: string, current: () => boolean,
    reportError: (error: unknown) => void, private makeId = channelRequestID) {
    super(Object.freeze({loading: false, saving: false}), current, reportError);
    if (!/^[a-f0-9]{48}$/.test(sourceId)) throw new Error('live_source_identity_invalid');
  }
  private checked(raw: unknown) {
    const doc = parseEnvelope(raw, this.serverId, parseLiveSourceDocument).result;
    if (doc.sourceId !== this.sourceId) throw new Error('live_source_identity_changed');
    return doc;
  }
  /** After a save that got no answer, the form shows what the server has now. */
  private async readAgain() {
    try {
      const document = this.checked(await readOwnerSourceConfiguration(this.api, this.sourceId));
      if (!this.isActive() || !this.uncertain || this.snapshot.saving) return;
      this.uncertain = false;this.publish({...this.snapshot, document, draft: document.settings});
    } catch { /* still out of reach: the error stays, and Save or reload reads again */ }
  }
  refreshQuietly = async (): Promise<QuietRefreshOutcome> => {
    if (!this.isActive() || this.quietBlocked()) return {kind: 'deferred'};
    this.invalidateQuiet();const serial = this.quietSerial, read = this.quietRead = new AbortController(), base = this.snapshot.document;
    const owns = () => this.isActive() && !read.signal.aborted && serial === this.quietSerial && !this.quietBlocked() && this.snapshot.document === base;
    try {
      const raw = await readOwnerSourceConfiguration(this.api, this.sourceId, read.signal);
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
      const raw = await readOwnerSourceConfiguration(this.api, this.sourceId, attempt.abort.signal);if (!this.valid(attempt)) return;
      const document = this.checked(raw);this.uncertain = false;this.publish({loading: false, saving: false, document, draft: document.settings});
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
      const body = {expectedRevision: document.revision, operationId: this.makeId(), settings: draft};
      const saved = this.checked(await writeOwnerSourceConfiguration(this.api, this.sourceId, body, attempt.abort.signal));
      if (!this.valid(attempt)) return false;
      this.uncertain = false;this.operations.clear();this.publish({loading: false, saving: false, document: saved, draft: saved.settings, saved: true});return this.valid(attempt);
    } catch (error) {
      // No answer: the save may or may not have happened, so the form reads the source again.
      this.uncertain = ![400,401,403,409,412,422].includes(errorFacts(error).status ?? 0);
      let roots: readonly string[] | undefined;
      try {const candidate = lanConfirmationRoots(error);roots = candidate ? confirmRoots(candidate) : undefined;} catch {roots = undefined;}
      if (this.valid(attempt)) {this.fail(error, {...this.snapshot, saving: false, error: {...errorFacts(error), ...(roots ? {confirmRoots: roots} : {})}});if (this.uncertain) void this.readAgain();}
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
      const raw = await importOwnerSourceLogos(this.api, this.sourceId, {operationId}, attempt.abort.signal);
      if (!this.valid(attempt)) return;
      const result = parseEnvelope(raw, this.serverId, value => {
        if (!value || typeof value !== 'object') throw {code: 'invalid_response'};
        const v = value as Record<string, unknown>, number = (n: unknown) => {if (typeof n !== 'number' || !Number.isSafeInteger(n) || n < 0 || n > 100000) throw {code: 'invalid_response'};return n;};
        return Object.freeze({requested: number(v.requested), imported: number(v.imported), skipped: number(v.skipped)});
      }).result;
      this.uncertain = false;this.operations.delete(key);this.publish({...this.snapshot, logoBusy: false, logoResult: result});
    } catch (error) {this.uncertain = ![400,401,403,409,412,422].includes(errorFacts(error).status ?? 0);if (this.valid(attempt)) this.fail(error, {...this.snapshot, logoBusy: false, error: errorFacts(error)});}
  }
  override dispose() {this.invalidateQuiet();this.operations?.clear();super.dispose();}
}
