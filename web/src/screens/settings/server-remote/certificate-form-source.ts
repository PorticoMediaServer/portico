import type {QuietRefreshOutcome} from '@core/freshness-actions.ts';
import {defaultI18n} from '@i18n';
import {routePath} from '../../../../../packages/contracts/src/routes.ts';
import {certificateForm} from '@core/server-admin/server-forms.ts';
import {secureConnectionView} from '@core/server-admin/secure-connection.ts';
import {retryCertificate, type CertificateApi} from '@core/certificates.ts';
import type {SettingsRow, SettingsValue} from '@core/presentation/settings-spec.ts';
import type {SettingPresentation} from '../../../kit/settings';

type Form = ReturnType<typeof certificateForm>;
type Api = Parameters<typeof certificateForm>[0]['api'];
const rows = new Set(['remote.secure', 'remote.secureStatus', 'remote.secureAddress', 'remote.certificate']);
const cancelled = () => new DOMException('Certificate Settings build retired', 'AbortError');

/** The existing certificate form, owned by one committed Portico Account settings visit. */
export class CertificateFormSource {
  private readonly form: Form;
  private readonly certificates: CertificateApi;
  private readonly life = new AbortController();
  private disposed = false;
  private loading?: Promise<void>;
  private retrying?: Promise<void>;
  private readonly listeners = new Set<() => void>();
  private snapshotForm?: ReturnType<Form['getSnapshot']>;
  private snapshot?: ReturnType<Form['getSnapshot']> & Readonly<{retrying: boolean}>;
  constructor(api: Api, private readonly isCurrent: () => boolean, private readonly onError: (error: unknown) => void) {
    const request: Api['request'] = async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
      this.assert();
      const deadline = new AbortController(), timer = setTimeout(() => deadline.abort(new DOMException('Certificate Settings timed out', 'TimeoutError')), 15_000);
      const bounded = AbortSignal.any([this.life.signal, deadline.signal, ...(signal ? [signal] : [])]);
      let stop: (() => void) | undefined;
      try {
        const aborted = new Promise<never>((_resolve, reject) => {
          stop = () => reject(bounded.reason ?? cancelled());
          bounded.addEventListener('abort', stop, {once: true});
          if (bounded.aborted) stop();
        });
        const value = await Promise.race([api.request<T>(path, method, body, bounded), aborted]);
        this.assert();bounded.throwIfAborted();return value;
      } finally {clearTimeout(timer);if (stop) bounded.removeEventListener('abort', stop);}
    };
    this.form = certificateForm({api: {request}} as Parameters<typeof certificateForm>[0]);
    this.certificates = {requestCertificate: (action, body, signal) => request(routePath(action === 'status' ? 'get_networking_certificate' : action === 'config' ? 'post_networking_certificate_config' : 'post_networking_certificate_retry'), action === 'status' ? 'GET' : 'POST', body, signal)};
  }
  current = () => {
    if (this.disposed || this.life.signal.aborted) return false;
    const accepted = this.isCurrent();
    return accepted && !this.disposed && !this.life.signal.aborted;
  };
  private assert() {if (!this.current()) {this.dispose();throw cancelled();}}
  has = (id: string) => rows.has(id);
  getSnapshot = () => {
    const form = this.form.getSnapshot(), retrying = !!this.retrying;
    if (!this.snapshot || this.snapshotForm !== form || this.snapshot.retrying !== retrying) {
      this.snapshotForm = form;this.snapshot = Object.freeze({...form, retrying});
    }
    return this.snapshot;
  };
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);const stop = this.form.subscribe(listener);
    return () => {this.listeners.delete(listener);stop();};
  };
  invalidateQuiet = () => this.form.invalidateQuiet();
  quietBlocked = () => !this.current() || !!(this.loading || this.retrying) || this.getSnapshot().saving || this.getSnapshot().saveError !== undefined;
  refresh = async (): Promise<QuietRefreshOutcome> => this.quietBlocked() ? {kind: 'deferred'} : this.form.refresh();
  load = (): Promise<void> => {
    if (!this.current()) return Promise.resolve();
    if (this.loading) return this.loading;
    const pending = Promise.resolve().then(() => {if (this.current()) return this.form.load();}).finally(() => {if (this.loading === pending) this.loading = undefined;});
    this.loading = pending;return pending;
  };
  private claimed() {const state = this.getSnapshot();return state.draft?.claimed === true && !!state.status?.authorityId;}
  rowState(row: SettingsRow): SettingPresentation {
    const state = this.getSnapshot(), status = state.status;
    if (!this.current() || !this.has(row.id) || !this.claimed() || !state.draft || !status) return {present: false};
    const disabled = state.loading || state.saving || !!this.retrying;
    if (row.id === 'remote.secure') return {present: true, value: state.draft.enabled, defaultValue: true,
      changed: !state.draft.enabled, disabled, saving: state.saving};
    if (!state.draft.enabled) return {present: false};
    if (row.id === 'remote.secureStatus') {
      const view = secureConnectionView({certificate: status});
      return {present: true, displayValue: defaultI18n.t(view.badge), statusTone: view.tone === 'healthy' ? 'ok' : view.tone === 'warning' ? 'warn' : 'quiet'};
    }
    if (row.id === 'remote.secureAddress') return {present: !!status.routeUrl, value: status.routeUrl, readOnly: true};
    return {present: true, readOnly: true, displayValue: status.notAfter
      ? defaultI18n.t('next.remoteStatus.expires', {when: defaultI18n.dateTime(status.notAfter)}) : defaultI18n.t('next.settings.notSet')};
  }
  change(id: string, value: SettingsValue) {
    const state = this.getSnapshot();
    if (!this.current() || id !== 'remote.secure' || typeof value !== 'boolean' || !this.claimed() || state.loading || state.saving || this.retrying) return;
    this.form.set({enabled: value});
    void this.save().catch(error => {
      // A refused write belongs to this form's inline Retry, not the whole
      // Settings route. Unexpected errors still reach its owning boundary.
      if (this.current() && this.getSnapshot().saveError !== error) this.onError(error);
    });
  }
  reset(id: string) {if (id === 'remote.secure') this.change(id, true);}
  save = async () => {this.assert();if (!this.claimed() || this.retrying) return;await this.form.save();};
  retry = (): Promise<void> => {
    this.assert();
    if (this.retrying) return this.retrying;
    const state = this.getSnapshot();
    if (!this.claimed() || !state.draft?.enabled || state.saving || state.dirty) return Promise.resolve();
    this.form.invalidateQuiet();
    const pending = Promise.resolve().then(async () => {
      this.assert();await retryCertificate(this.certificates, this.life.signal);this.assert();await this.load();
    }).finally(() => {
      if (this.retrying === pending) this.retrying = undefined;
      if (this.current()) for (const listener of this.listeners) listener();
    });
    this.retrying = pending;for (const listener of this.listeners) listener();return pending;
  };
  dispose() {if (this.disposed) return;this.disposed = true;this.life.abort();this.form.dispose();this.listeners.clear();}
}
