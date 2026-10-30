import {useCallback, useLayoutEffect, useRef, useState, type RefObject} from 'react';
import type {HttpLocalApi} from '@core/index.ts';
import {defaultI18n} from '@i18n';
import {Dialog} from '../../kit/overlays';
import {Button} from '../../kit/controls';
import {captureAppLog, type AppLogEntry} from '../../platform/app-log';
import styles from './app-log.module.css';

const t = defaultI18n.t;
type Recipient = Readonly<{displayName: string; revision: string}>;
type Body = Readonly<{recipientRevision: string; entries: readonly AppLogEntry[]}>;
type Lease = {life: AbortController; busy: boolean};
type Phase = 'loading' | 'idle' | 'busy' | 'done' | 'failed';
function recipient(value: unknown): Recipient {
  const row = value as Partial<Recipient> | null;
  if (!row || typeof row.displayName !== 'string' || !row.displayName.trim() || row.displayName.length > 512
    || typeof row.revision !== 'string' || !/^[a-f0-9]{64}$/.test(row.revision)) throw new Error('invalid_app_report_recipient');
  return Object.freeze({displayName: row.displayName, revision: row.revision});
}

/** One explicit opening captures the journal and recipient. An uncertain retry
 * keeps that same body/key; closing aborts and discards consent. */
export function SendAppLog({api, isCurrent, onClose, returnFocusRef}: {
  api: Pick<HttpLocalApi, 'request' | 'requestRaw'>; isCurrent(): boolean; onClose(): void;
  returnFocusRef: RefObject<HTMLElement | null>;
}) {
  // Inert opening data survives an effect replay. A committed layout lease owns
  // all I/O, while the original journal, recipient/body and key never change.
  const [capture] = useState(() => ({api, isCurrent, log: captureAppLog(), key: crypto.randomUUID(),
    owner: undefined as Recipient | undefined, body: undefined as Body | undefined, sent: false}));
  const active = useRef<Lease | undefined>(undefined);
  const [state, setState] = useState<{lease: Lease; phase: Phase}>();
  const [consent, setConsent] = useState(false);
  const current = useCallback((lease: Lease | undefined): lease is Lease => !!lease && active.current === lease && !lease.life.signal.aborted
    && capture.api === api && capture.isCurrent() && isCurrent() && capture.log.isCurrent(), [capture, api, isCurrent]);
  useLayoutEffect(() => {
    const lease: Lease = {life: new AbortController(), busy: false};
    active.current = lease;
    setConsent(false);
    const retire = () => {if (active.current === lease) active.current = undefined;lease.life.abort();};
    if (!current(lease)) {setState(undefined);retire();return;}
    const publish = (phase: Phase) => {if (current(lease)) setState({lease, phase});};
    publish(capture.sent ? 'done' : capture.owner || !capture.log.entries.length ? 'idle' : 'loading');
    if (!capture.owner && capture.log.entries.length) void capture.api.request('/v1/app-reports/recipient', 'GET', undefined, lease.life.signal)
      .then(value => {
        if (!current(lease)) return;
        capture.owner = recipient(value);
        capture.body = Object.freeze({recipientRevision: capture.owner.revision, entries: capture.log.entries});
        publish('idle');
      }).catch(() => publish('failed'));
    return retire;
  }, [capture, current]);
  // Every rendered handler captures its own lease. It cannot look up and adopt
  // the replacement when replay or an authority/callback change retires it.
  const lease = state?.lease, phase = state?.phase ?? 'loading', owner = capture.owner;
  const publish = (next: Phase) => {if (current(lease)) setState({lease, phase: next});};
  const close = () => {if (!lease || active.current !== lease) return;active.current = undefined;lease.life.abort();onClose();};
  const send = async () => {
    const body = capture.body;
    if (!current(lease) || !owner || !body || !consent || lease.busy || capture.sent) return;
    if (!capture.log.sendable() || new TextEncoder().encode(JSON.stringify(body)).byteLength > 16384) {publish('failed');return;}
    lease.busy = true;publish('busy');
    try {
      const response = await capture.api.requestRaw('/v1/app-reports', 'POST', {headers: {'Idempotency-Key': capture.key}, body, signal: lease.life.signal});
      if (!current(lease)) return;
      const receipt = response.body as {id?: unknown; entryCount?: unknown} | null;
      if ((response.status !== 200 && response.status !== 201) || !receipt || typeof receipt.id !== 'string' || !receipt.id || receipt.entryCount !== body.entries.length) throw new Error('app_report_not_acknowledged');
      capture.sent = true;capture.log.acknowledge();publish('done');
    } catch {publish('failed');}
    finally {lease.busy = false;}
  };
  return <Dialog open onOpenChange={value => {if (!value) close();}} size="sm"
    title={t('next.settingsSpec.account.device.groups.5.rows.5.label')} returnFocusRef={returnFocusRef}
    footer={<><Button variant="glass" size="small" onClick={close}>{t('action.cancel')}</Button>
      {phase !== 'done' && <Button size="small" loading={phase === 'busy'} disabled={!current(lease) || !owner || !consent || !capture.log.entries.length || phase === 'busy'} onClick={() => {void send();}}>{t('next.settingsSpec.account.device.groups.5.rows.5.control.label')}</Button>}</>}>
    <div className={styles.body}>
      <p>{t('next.settingsSpec.account.device.groups.5.rows.5.help')}</p>
      {!capture.log.entries.length ? <p role="status">{t('next.settings.appLog.empty')}</p>
        : phase === 'loading' ? <p role="status">{t('status.loading')}</p>
        : phase === 'done' ? <p role="status">{t('next.settings.appLog.sent', {owner: owner!.displayName})}</p>
        : owner && <>
          <p>{t('next.settings.appLog.recipient', {owner: owner.displayName})}</p>
          <label className={styles.confirm}><input type="checkbox" checked={consent} disabled={!current(lease) || phase === 'busy'} onChange={event => {if (current(lease)) setConsent(event.currentTarget.checked);}} />
            <span>{t('next.settingsSpec.account.device.groups.5.rows.5.control.confirm')}</span></label>
        </>}
      {phase === 'failed' && <p role="alert">{t('about.report.failed')}</p>}
    </div>
  </Dialog>;
}
