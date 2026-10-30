import {useState, useSyncExternalStore} from 'react';
import type {ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {remoteAccessHelp, remoteAccessSummary, remoteWarningLines} from '@core/remote-access.ts';
import {secureConnectionView} from '@core/server-admin/secure-connection.ts';
import {certificateProblemMessage} from '@core/certificates.ts';
import type {RemoteFormSource} from './remote-form-source';
import type {CertificateFormSource} from './certificate-form-source';
import {Banner} from '../../../kit/overlays';
import {Button} from '../../../kit/controls';
import styles from './server-remote.module.css';

const t = defaultI18n.t;
const noSubscribe = () => () => {};
const noSnapshot = () => undefined;

/** The certificate status stays beneath its switch, including renewal failures
 * while the previous certificate is still serving. Unknown codes stay generic. */
export function CertificateStatusControl({source}: {source: CertificateFormSource}) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [retrying, setRetrying] = useState(false), [failed, setFailed] = useState(false);
  const view = secureConnectionView({certificate: state.status}), problemId = certificateProblemMessage(state.status), problem = problemId && t(problemId);
  return <div className={styles.certificateStatus} data-problem={!!problem || failed || undefined}>
    <span role="status">{problem ?? t(view.badge)}</span>
    {problem && <Button size="small" variant="glass" loading={retrying} disabled={state.saving || state.dirty} onClick={() => {
      if (!source.current() || retrying) return;
      setRetrying(true);setFailed(false);
      void source.retry().catch(() => {if (source.current()) setFailed(true);}).finally(() => {if (source.current()) setRetrying(false);});
    }}>{t('next.states.tryAgain')}</Button>}
    {failed && <span role="alert">{t('next.remoteStatus.failed')}</span>}
  </div>;
}

/** One reachability hero, then the approved settings groups. All actions retain
 * their original owner-scoped forms and checked server status. */
export function ServerRemotePage({isCurrent, source, certificate, groups, advanced}: Readonly<{
  api: Readonly<{request: <T>(path: string, method: string, body?: unknown, signal?: AbortSignal) => Promise<T>}>;
  isCurrent: () => boolean;
  source?: RemoteFormSource;
  certificate?: CertificateFormSource;
  groups: ReactNode; advanced: ReactNode;
}>) {
  const formState = useSyncExternalStore(source?.subscribe ?? noSubscribe, source?.getSnapshot ?? noSnapshot, source?.getSnapshot ?? noSnapshot);
  const certificateState = useSyncExternalStore(certificate?.subscribe ?? noSubscribe, certificate?.getSnapshot ?? noSnapshot, certificate?.getSnapshot ?? noSnapshot);
  const remote = formState?.status;
  const [checking, setChecking] = useState(false), [failed, setFailed] = useState(false);
  const tone = remote?.state === 'reachable' ? 'ok' : remote?.state === 'remote_unavailable' || remote?.state === 'hosted_unavailable' ? 'warn' : 'quiet';
  const checkedAt = remote?.checkedAt && Number.isFinite(Date.parse(remote.checkedAt)) ? remote.checkedAt : undefined;
  const help = remoteAccessHelp(remote?.errorCode, t);
  const failure = <Banner>{t('next.remoteStatus.failed')}</Banner>;
  return <div className={styles.page}>
    <section className={styles.reach} data-tone={tone} aria-label={t('next.remoteStatus.reachability')}>
      <div><strong className={styles.reachTitle} role="status">{checking ? t('settings.server.remote.checking') : remote ? remoteAccessSummary(remote, t) : t('next.remoteStatus.reading')}</strong>
        {checkedAt && <p className={styles.note}>{t('web.servers.checked', {when: defaultI18n.dateTime(checkedAt)})}</p>}
        {help && <p className={styles.note}>{help}</p>}
        {remote && remoteWarningLines(remote, t).map((line, index) => <p key={index} className={styles.note} data-tone="warn">{line}</p>)}
      </div>
      {source && <Button size="small" variant="glass" icon="i-refresh" loading={checking} disabled={!remote || formState?.saving || formState?.dirty} onClick={() => {
        if (!isCurrent() || checking) return;
        setChecking(true);setFailed(false);
        void source.check().catch(() => {if (isCurrent()) setFailed(true);}).finally(() => {if (isCurrent()) setChecking(false);});
      }}>{t('next.settingsSpec.server.general.groups.1.rows.2.control.label')}</Button>}
    </section>
    {failed && failure}
    {groups}
    {advanced}
    {formState?.unavailable && <Banner action={{label: t('next.states.tryAgain'), onAction: () => {if (source?.current()) void source.load();}}}>{t('next.remoteStatus.failed')}</Banner>}
    {source && formState?.saveError !== undefined && <Banner action={{label: t('next.states.tryAgain'), onAction: () => {
      if (source.current()) void source.save().catch(() => {if (isCurrent()) setFailed(true);});
    }}}>{t('next.remoteStatus.failed')}</Banner>}
    {certificate && certificateState?.loadError !== undefined && <Banner action={{label: t('next.states.tryAgain'), onAction: () => {if (certificate.current()) void certificate.load();}}}>{t('next.remoteStatus.failed')}</Banner>}
    {certificate && certificateState?.saveError !== undefined && <Banner action={{label: t('next.states.tryAgain'), onAction: () => {
      if (certificate.current()) void certificate.save().catch(() => {if (isCurrent()) setFailed(true);});
    }}}>{t('next.remoteStatus.failed')}</Banner>}
  </div>;
}
