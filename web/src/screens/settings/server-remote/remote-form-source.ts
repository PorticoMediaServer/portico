import type {QuietRefreshOutcome} from '@core/freshness-actions.ts';
import {defaultI18n} from '@i18n';
import {remoteAccessForm} from '@core/server-admin/server-forms.ts';
import {checkRemoteAccess, type RemoteConfiguration} from '@core/remote-access.ts';
import type {HttpLocalApi} from '@core/index.ts';
import {settingsRowIndex, type SettingsRow, type SettingsValue} from '@core/presentation/settings-spec.ts';
import type {SettingPresentation} from '../../../kit/settings';
import type {SettingsPageSnapshot} from '../Settings';

type Form = ReturnType<typeof remoteAccessForm>;
type Api = Parameters<typeof remoteAccessForm>[0]['api'];
const rowIds = new Set(['remote.mapping', 'remote.mappingProtocols', 'remote.gateway', 'remote.publicPort', 'remote.ipv6']);
const cancelled = () => new DOMException('Remote Settings build retired', 'AbortError');

/** One committed owner build, one original API and one revision-bearing form.
 * This private Web adapter does not invent generic configuration bindings. */
export class RemoteFormSource {
  private readonly form: Form;
  private readonly life = new AbortController();
  private disposed = false;
  private pending?: Promise<void>;
  private pageBase?: SettingsPageSnapshot;
  private pageForm?: ReturnType<Form['getSnapshot']>;
  private pageCertificate?: Readonly<{loading: boolean; saving: boolean; loadError?: unknown}>;
  private page?: SettingsPageSnapshot;
  private readonly api: Api;
  private automaticSave?: Promise<void>;
  private checking?: Promise<void>;
  private saveGeneration = 0;
  private readonly listeners = new Set<() => void>();
  private snapshotForm?: ReturnType<Form['getSnapshot']>;
  private snapshot?: ReturnType<Form['getSnapshot']> & Readonly<{checking: boolean}>;
  constructor(api: Api, private readonly isCurrent: () => boolean, private readonly owner: boolean) {
    const request: Api['request'] = async <T>(...args: Parameters<Api['request']>): Promise<T> => {
      const [path, method, body, signal] = args;
      this.assert();
      const deadline = new AbortController();
      const timer = setTimeout(() => deadline.abort(new DOMException('Remote Settings timed out', 'TimeoutError')), 15_000);
      const bounded = AbortSignal.any([this.life.signal, deadline.signal, ...(signal ? [signal] : [])]);
      let stop: (() => void) | undefined;
      try {
        const aborted = new Promise<never>((_resolve, reject) => {
          stop = () => reject(bounded.reason ?? cancelled());
          bounded.addEventListener('abort', stop, {once: true});
          if (bounded.aborted) stop();
        });
        const value = await Promise.race([api.request<T>(path, method, body, bounded), aborted]);
        this.assert();if (bounded.aborted) throw bounded.reason;
        return value;
      } finally {clearTimeout(timer);if (stop) bounded.removeEventListener('abort', stop);}
    };
    this.api = {request};
    this.form = remoteAccessForm({api: this.api} as Parameters<typeof remoteAccessForm>[0]);
  }
  current = () => {
    if (this.disposed || !this.owner || this.life.signal.aborted) return false;
    const accepted = this.isCurrent();
    return accepted && !this.disposed && this.owner && !this.life.signal.aborted;
  };
  private assert() {if (!this.current()) {this.dispose();throw cancelled();}}
  has = (rowId: string) => rowIds.has(rowId);
  getSnapshot = () => {
    const form = this.form.getSnapshot(), checking = !!this.checking;
    if (!this.snapshot || this.snapshotForm !== form || this.snapshot.checking !== checking) {
      this.snapshotForm = form;this.snapshot = Object.freeze({...form, checking});
    }
    return this.snapshot;
  };
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);const stop = this.form.subscribe(listener);
    return () => {this.listeners.delete(listener);stop();};
  };
  invalidateQuiet = () => this.form.invalidateQuiet();
  quietBlocked = () => !this.current() || !!(this.pending || this.automaticSave || this.checking) || this.getSnapshot().saving || this.getSnapshot().saveError !== undefined;
  refresh = async (): Promise<QuietRefreshOutcome> => this.quietBlocked() ? {kind: 'deferred'} : this.form.refresh();
  load = (): Promise<void> => {
    if (!this.current()) return Promise.resolve();
    if (this.pending) return this.pending;
    // Reserve before DraftForm publishes loading to synchronous subscribers.
    const pending = Promise.resolve().then(() => {if (!this.current()) return;return this.form.load();}).finally(() => {if (this.pending === pending) this.pending = undefined;});
    this.pending = pending;return pending;
  };
  rowState(row: SettingsRow): SettingPresentation {
    if (!this.current() || !this.has(row.id)) return {present: false};
    const state = this.getSnapshot(), draft = state.draft;
    if (!draft) return {present: false};
    const value = this.value(row.id, draft), defaults = this.defaultValue(row.id);
    return {present: true, value, disabled: state.loading || state.saving || state.checking, saving: state.saving,
      changed: defaults !== undefined && JSON.stringify(value) !== JSON.stringify(defaults), defaultValue: defaults,
      ...(row.id === 'remote.ipv6' && value === undefined ? {readOnly: true, displayValue: defaultI18n.t('next.settings.notSet')} : {})};
  }
  private defaultValue(id: string): SettingsValue | undefined {
    const control = settingsRowIndex.get(id)?.row.control;
    return control?.type === 'matrix' ? Object.fromEntries((control.items ?? []).filter(item => item.id && item.default !== undefined).map(item => [item.id!, item.default!])) : control?.default ?? (id === 'remote.gateway' ? '' : undefined);
  }
  private value(id: string, config: RemoteConfiguration): SettingsValue | undefined {
    switch (id) {
      case 'remote.mapping': return config.mapping ? 'auto' : 'off';
      case 'remote.mappingProtocols': return {pcp: config.pcp ? 'try' : 'skip', natpmp: config.natpmp ? 'try' : 'skip', upnp: config.upnp ? 'try' : 'skip'};
      case 'remote.gateway': return config.gateway;
      case 'remote.publicPort': return config.publicPort;
      case 'remote.ipv6': return config.ipv6Open;
    }
  }
  change(rowId: string, value: SettingsValue) {
    if (!this.current() || !this.has(rowId)) return;
    const {draft, loading, saving} = this.getSnapshot();
    if (!draft || loading || saving || this.checking) return;
    switch (rowId) {
      case 'remote.mapping': if (value === 'auto' || value === 'off') this.form.set({mapping: value === 'auto'});break;
      case 'remote.mappingProtocols': {
        if (!draft.mapping || !value || typeof value !== 'object' || Array.isArray(value)) break;
        const matrix = value as Readonly<Record<string, SettingsValue>>;
        if (Object.keys(matrix).length !== 3 || !['pcp', 'natpmp', 'upnp'].every(key => matrix[key] === 'try' || matrix[key] === 'skip')) break;
        this.form.set({pcp: matrix.pcp === 'try', natpmp: matrix.natpmp === 'try', upnp: matrix.upnp === 'try'});break;
      }
      case 'remote.gateway': if (draft.mapping && typeof value === 'string' && value.length <= 64) this.form.set({gateway: value});break;
      case 'remote.publicPort': if (typeof value === 'number' && Number.isInteger(value) && value >= 1024 && value <= 65535) this.form.set({publicPort: value});break;
      case 'remote.ipv6': if (draft.ipv6Open !== undefined && typeof value === 'boolean') this.form.set({ipv6Open: value});break;
    }
    if (this.getSnapshot().draft !== draft) this.scheduleSave();
  }
  private scheduleSave() {
    if (this.automaticSave) return;
    const generation = this.saveGeneration;
    // A completed field is one intent; synchronous edits coalesce before the
    // revisioned form writes. While writing its controls remain disabled.
    const pending = Promise.resolve().then(async () => {
      if (!this.current() || generation !== this.saveGeneration || this.getSnapshot().saveError !== undefined || this.getSnapshot().problem) return;
      try {await this.form.save();} catch (error) {
        // A refused write is kept in saveError with its draft, and the page
        // offers a deliberate Retry. Anything else is not ours to hide.
        if (this.current() && this.getSnapshot().saveError !== error) throw error;
      }
    }).finally(() => {
      if (this.automaticSave === pending) this.automaticSave = undefined;
      const state = this.getSnapshot();
      if (this.current() && state.dirty && !state.saving && state.saveError === undefined && !state.problem && generation === this.saveGeneration) this.scheduleSave();
    });
    this.automaticSave = pending;
  }
  reset(rowId: string) {
    if (!this.current() || !this.has(rowId)) return;
    const value = this.defaultValue(rowId);
    if (value !== undefined) this.change(rowId, value);
  }
  save = async (expected = this.getSnapshot()) => {
    this.assert();if (expected !== this.getSnapshot() || this.checking) return;
    this.saveGeneration++;await this.form.save();
  };
  discard = (expected = this.getSnapshot()) => {if (this.current() && expected === this.getSnapshot()) this.form.discard();};
  check = (): Promise<void> => {
    this.assert();
    if (this.checking) return this.checking;
    const state = this.getSnapshot();
    if (!state.status || state.loading || state.saving || state.dirty) return Promise.resolve();
    this.form.invalidateQuiet();
    const pending = Promise.resolve().then(async () => {
      this.assert();await checkRemoteAccess(this.api as HttpLocalApi, this.life.signal);this.assert();await this.load();
    }).finally(() => {
      if (this.checking === pending) this.checking = undefined;
      if (this.current()) for (const listener of this.listeners) listener();
    });
    this.checking = pending;for (const listener of this.listeners) listener();return pending;
  };
  pageSnapshot(base: SettingsPageSnapshot, certificate?: Readonly<{loading: boolean; saving: boolean; loadError?: unknown}>): SettingsPageSnapshot {
    const form = this.getSnapshot();
    if (this.page && this.pageBase === base && this.pageForm === form && this.pageCertificate === certificate) return this.page;
    this.pageBase = base;this.pageForm = form;this.pageCertificate = certificate;
    return this.page = Object.freeze({...base, loading: base.loading || form.loading || certificate?.loading === true, error: base.error ?? form.loadError,
      saveState: form.saving || certificate?.saving ? 'saving' : base.saveState});
  }
  dispose() {if (this.disposed) return;this.disposed = true;this.life.abort();this.form.dispose();this.listeners.clear();}
}
