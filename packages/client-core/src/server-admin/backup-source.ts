import {throwIfAborted} from '../abort-lifetime.ts';
import {deleteBackup, getBackups, getBackupSettings, inspectBackupSource, isAuthenticationFailure, restoreBackup, startBackup, updateBackupSettings, waitForBackupCompletion, waitForServerAnswer, type BackupSettings, type BackupSettingsPatch, type BackupTransport} from '../backups.ts';
import {isRestoreRefusalCode, type RestoreRefusalCode, type BackupsDocument, type RestoreSource, type RestoreStaged} from '../administration.ts';
import {randomId} from '../random-id.ts';

export type SettingsBackupSnapshot = Readonly<{settings?: BackupSettings; document?: BackupsDocument; staged?: RestoreStaged; restorePhase?: 'preparing' | 'restarting'; restoreJobId?: string; restoreError?: RestoreRefusalCode | true; restoreEncrypted?: boolean; busy: boolean}>;
export type SettingsRestoreSelection = Readonly<{encrypted: boolean; isCurrent: () => boolean}>;
const empty: SettingsBackupSnapshot = Object.freeze({busy: false});

/** Owner-scoped backup facts and commands. Secret inputs remain request-local. */
export class SettingsBackupSource {
  private state = empty;
  private readonly lifetime = new AbortController();
  private loading?: Promise<void>;
  private candidate?: AbortController;
  private restoring?: object;
  private restoreIntent?: {source: RestoreSource; operationId: string; jobId?: string};
  private readonly selections = new WeakMap<SettingsRestoreSelection, {source: RestoreSource; signal: AbortSignal; candidate: AbortController; transferred: boolean}>();
  constructor(private readonly api: BackupTransport, private readonly serverId: string, private readonly isCurrent: () => boolean, private readonly notify: () => void, private readonly operationId: () => string = randomId) {}
  private current = () => !this.lifetime.signal.aborted && this.isCurrent();
  getSnapshot = () => this.current() ? this.state : empty;
  private publish(state: SettingsBackupSnapshot) {if (this.current()) {this.state = Object.freeze(state);this.notify();}}
  private restoreActive() {return !!this.state.document?.restore && !this.state.document.restore.errorCode;}
  private requireCurrent() {if (!this.current()) throw Object.assign(new Error('The backup scope ended.'), {name: 'AbortError'});}
  private refuse(error: unknown): never {
    if (isAuthenticationFailure(error)) {this.dispose();this.notify();}
    throw error;
  }
  load = (): Promise<void> => {
    if (this.lifetime.signal.aborted) return Promise.reject(Object.assign(new Error('The backup scope ended.'), {name: 'AbortError'}));
    if (!this.current() || this.state.busy) return Promise.resolve();
    if (this.loading) return this.loading;
    const task = Promise.all([getBackupSettings(this.api, this.serverId, this.lifetime.signal), getBackups(this.api, this.serverId, this.lifetime.signal)]).then(([settings, document]) => {
      if (document.lastRestore?.jobId && document.lastRestore.jobId === this.restoreIntent?.jobId) this.restoreIntent = undefined;
      this.publish({...this.state, settings, document, ...(document.lastRestore?.jobId && document.lastRestore.jobId === this.state.restoreJobId ? {restorePhase: undefined, staged: undefined, restoreError: undefined} : {})});
    }).catch(error => this.refuse(error)).finally(() => {if (this.loading === task) this.loading = undefined;});
    this.loading = task;return task;
  };
  async save(patch: BackupSettingsPatch, signal?: AbortSignal): Promise<boolean> {
    const request = this.linkSignals(signal ? [signal] : []), requestSignal = request.signal;
    if (!this.current() || requestSignal.aborted || this.loading || this.state.busy || !this.state.settings) {request.dispose();return false;}
    this.publish({...this.state, busy: true});
    try {
      this.requireCurrent();
      throwIfAborted(requestSignal);
      const settings = await updateBackupSettings(this.api, this.serverId, patch, requestSignal);
      if (!this.current() || requestSignal.aborted) return false;
      this.publish({...this.state, settings});return true;
    } catch (error) {if (isAuthenticationFailure(error) || !requestSignal.aborted) this.refuse(error);} finally {request.dispose();this.publish({...this.state, busy: false});}
    return false;
  }
  async backupNow(options: Readonly<{intervalMs?: number; timeoutMs?: number}> = {}): Promise<boolean> {
    if (!this.current() || this.loading || this.state.busy || !this.state.document || this.state.document.running || this.state.document.restore) return false;
    this.publish({...this.state, busy: true});
    try {
      this.requireCurrent();
      const jobId = await startBackup(this.api, this.serverId, this.operationId(), this.lifetime.signal);
      this.requireCurrent();
      const document = await waitForBackupCompletion(() => {
        this.requireCurrent();return getBackups(this.api, this.serverId, this.lifetime.signal);
      }, {...options, expectedJobId: jobId, signal: this.lifetime.signal, onProgress: document => this.publish({...this.state, document})});
      if (!this.current()) return false;
      this.publish({...this.state, document});return true;
    } catch (error) {this.refuse(error);} finally {this.publish({...this.state, busy: false});}
  }
  async inspect(source: RestoreSource, signal: AbortSignal) {
    this.requireCurrent();
    if ('backupId' in source && !this.state.document?.backups.some(backup => backup.id === source.backupId)) throw new Error('The selected backup is no longer listed.');
    const selected = Object.freeze('backupId' in source ? {backupId: source.backupId} : {path: source.path});
    const request = new AbortController(), abort = () => request.abort();
    this.lifetime.signal.addEventListener('abort', abort, {once: true});signal.addEventListener('abort', abort, {once: true});
    if (signal.aborted) request.abort();
    try {
      throwIfAborted(request.signal);
      const inspection = await inspectBackupSource(this.api, this.serverId, selected, request.signal);
      this.requireCurrent();throwIfAborted(request.signal);return inspection;
    } catch (error) {
      if (this.current() && !request.signal.aborted) {const code = (error as {code?: unknown})?.code;this.publish({...this.state, restoreError: typeof code === 'string' && isRestoreRefusalCode(code) ? code : true, restoreEncrypted: undefined});}
      this.refuse(error);
    } finally {this.lifetime.signal.removeEventListener('abort', abort);signal.removeEventListener('abort', abort);}
  }
  /** Inspection establishes the exact candidate. A later command cannot retarget its path or ID. */
  async prepareRestore(source: RestoreSource, signal: AbortSignal): Promise<SettingsRestoreSelection> {
    this.requireCurrent();
    if (signal.aborted || this.restoring || this.loading || this.state.busy || this.state.document?.running || this.restoreActive() || this.state.staged) throw Object.assign(new Error('Another backup action is active.'), {name: 'AbortError'});
    this.cancelRestoreSelection();
    const candidate = new AbortController();this.candidate = candidate;
    const selected = Object.freeze('backupId' in source ? {backupId: source.backupId} : {path: source.path});
    const request = this.linkSignals([signal, candidate.signal]);
    try {
      const inspection = await this.inspect(selected, request.signal);
      throwIfAborted(request.signal);this.requireCurrent();
      const captured = {source: selected, signal, candidate, transferred: false};
      const selection: SettingsRestoreSelection = Object.freeze({encrypted: inspection.encrypted,
        isCurrent: () => this.current() && this.candidate === candidate && !candidate.signal.aborted && !captured.signal.aborted &&
          (!('backupId' in selected) || !!this.state.document?.backups.some(backup => backup.id === selected.backupId)),
      });
      this.selections.set(selection, captured);return selection;
    } catch (error) {if (this.candidate === candidate) {candidate.abort();this.candidate = undefined;}throw error;} finally {request.dispose();}
  }
  /** Transfer an inspected encrypted candidate to its exact parent-owned passphrase step. */
  handoffRestoreSelection(selection: SettingsRestoreSelection, signal: AbortSignal): void {
    const captured = this.selections.get(selection);
    if (!captured || captured.transferred || !selection.encrypted || !selection.isCurrent() || signal.aborted) throw Object.assign(new Error('The restore selection ended.'), {name: 'AbortError'});
    captured.transferred = true;captured.signal = signal;
  }
  async restore(selection: SettingsRestoreSelection, passphrase: string | undefined, signal: AbortSignal): Promise<RestoreStaged | undefined> {
    this.requireCurrent();
    const captured = this.selections.get(selection);
    if (!captured || !selection.isCurrent()) throw Object.assign(new Error('The restore selection ended.'), {name: 'AbortError'});
    if (this.restoring || this.loading || this.state.busy || this.state.document?.running || this.restoreActive() || this.state.staged) return undefined;
    if (selection.encrypted ? typeof passphrase !== 'string' || !passphrase.length : passphrase !== undefined) throw new Error('Invalid restore passphrase input.');
    const sourceKey = (source: RestoreSource) => 'backupId' in source ? `backup:${source.backupId}` : `path:${source.path}`;
    if (this.restoreIntent && sourceKey(this.restoreIntent.source) !== sourceKey(captured.source)) throw new Error('restore_result_unconfirmed');
    // The server's restore receipt is bound to source+operation, not a retained passphrase.
    const intent = this.restoreIntent ?? {source: captured.source, operationId: this.operationId(), jobId: undefined};
    this.restoreIntent = intent;
    const request = this.linkSignals([captured.signal, signal, captured.candidate.signal]);
    const flight = {};this.restoring = flight;
    const current = () => this.current() && this.restoring === flight && this.restoreIntent === intent && this.candidate === captured.candidate && !request.signal.aborted;
    this.publish({...this.state, busy: true, restorePhase: 'preparing', restoreJobId: intent.jobId, restoreError: undefined, restoreEncrypted: selection.encrypted});
    try {
      if (!current() || !selection.isCurrent()) throw Object.assign(new Error('The restore selection ended.'), {name: 'AbortError'});
      const staged = await restoreBackup(this.api, this.serverId, intent.operationId, intent.source, request.signal, passphrase, {
        isCurrent: current,
        onAccepted: jobId => {
          if (!current() || intent.jobId && intent.jobId !== jobId) throw new Error('restore_job_changed');
          intent.jobId = jobId;this.publish({...this.state, restoreJobId: jobId});
        },
        onProgress: document => {
          if (!current()) return;
          this.publish({...this.state, document, restorePhase: document.lastRestore?.jobId === intent.jobId ? undefined : document.restore?.result ? 'restarting' : 'preparing'});
        },
      });
      if (!current()) throw Object.assign(new Error('The restore selection ended.'), {name: 'AbortError'});
      this.publish({...this.state, staged, restorePhase: 'restarting'});
      // Staging itself requests the graceful restart. Never issue a second restart.
      const document = await waitForServerAnswer(async () => {
        if (!current()) throw Object.assign(new Error('The restore selection ended.'), {name: 'AbortError'});
        const document = await getBackups(this.api, this.serverId, request.signal);
        if (!current()) throw Object.assign(new Error('The restore selection ended.'), {name: 'AbortError'});
        if (!intent.jobId || document.lastRestore?.jobId !== intent.jobId) throw new Error('restore_result_pending');
        return document;
      }, {signal: request.signal});
      if (!current()) throw Object.assign(new Error('The restore selection ended.'), {name: 'AbortError'});
      this.restoreIntent = undefined;
      this.publish({...this.state, document, staged: undefined, restorePhase: undefined, restoreError: undefined});
      return staged;
    } catch (error) {
      if (this.current()) {
        const completed = !!intent.jobId && this.state.document?.lastRestore?.jobId === intent.jobId;
        const code = (error as {code?: unknown})?.code;
        if (completed || typeof code === 'string' && isRestoreRefusalCode(code)) this.restoreIntent = undefined;
        this.publish({...this.state, restorePhase: undefined, restoreError: completed || request.signal.aborted ? undefined : typeof code === 'string' && isRestoreRefusalCode(code) ? code : true});
        if (completed && this.state.document?.lastRestore?.outcome === 'rolled_back') throw Object.assign(new Error('restore_rolled_back'), {code: 'restore_rolled_back'});
      }
      this.refuse(error);
    } finally {request.dispose();if (this.restoring === flight) {this.restoring = undefined;this.publish({...this.state, busy: false});}}
  }
  /** Delete one backup the owner chose from the current list; an unlisted id is refused. */
  async remove(backupId: string, signal: AbortSignal): Promise<boolean> {
    this.requireCurrent();
    const document = this.state.document;
    if (!document?.backups.some(backup => backup.id === backupId)) throw new Error('The selected backup is no longer listed.');
    if (this.loading || this.state.busy || document.running || document.restore) return false;
    const request = this.linkSignals([signal]);
    this.publish({...this.state, busy: true});
    try {
      throwIfAborted(request.signal);
      this.requireCurrent();
      await deleteBackup(this.api, this.serverId, backupId, request.signal);
      this.requireCurrent();throwIfAborted(request.signal);
      const document = await getBackups(this.api, this.serverId, this.lifetime.signal);
      if (!this.current()) return false;
      this.publish({...this.state, document});return true;
    } catch (error) {this.refuse(error);} finally {request.dispose();this.publish({...this.state, busy: false});}
  }
  cancelRestoreSelection(selection?: SettingsRestoreSelection) {if (selection && this.selections.get(selection)?.candidate !== this.candidate) return;this.candidate?.abort();this.candidate = undefined;}  private linkSignals(signals: readonly AbortSignal[]) {
    const request = new AbortController(), abort = () => request.abort(), all = [this.lifetime.signal, ...signals];
    for (const signal of all) {signal.addEventListener('abort', abort, {once: true});if (signal.aborted) request.abort();}
    return {signal: request.signal, dispose: () => {for (const signal of all) signal.removeEventListener('abort', abort);}};
  }
  dispose() {this.lifetime.abort();this.restoreIntent = undefined;this.restoring = undefined;this.cancelRestoreSelection();this.loading = undefined;this.state = empty;}
}
