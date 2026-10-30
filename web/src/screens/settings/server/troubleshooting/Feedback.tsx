import {useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import {RadioGroup} from 'radix-ui';
import type {FeedbackStatus} from '@core/console.ts';
import {feedbackSentFrom, feedbackTriageCategory, type FeedbackReport} from '@core/server-admin/feedback-source.ts';
import {Button} from '../../../../kit/controls';
import {Icon} from '../../../../kit/Icon';
import {Dialog} from '../../../../kit/overlays/Dialog';
import {SettingsAction} from '../../../../kit/settings';
import type {SettingsFeedbackSource} from './feedback-source';
import styles from './troubleshooting.module.css';

const t = defaultI18n.t;

export type FeedbackPanelProps = Readonly<{
  source: SettingsFeedbackSource;
  subscribe: (listener: () => void) => () => void;
  reportError: (error: unknown) => void;
}>;

const STATUSES: readonly FeedbackStatus[] = Object.freeze(['open', 'in-progress', 'resolved', 'closed']);
const CATEGORY: Readonly<Record<string, MessageId>> = Object.freeze({
  playback: 'server.feedback.category.playback', metadata: 'server.feedback.category.metadata', subtitles: 'server.feedback.category.subtitles', other: 'server.feedback.category.other',
});
const STATUS: Readonly<Record<FeedbackStatus, MessageId>> = Object.freeze({open: 'server.feedback.status.open', 'in-progress': 'server.feedback.status.in-progress',
  resolved: 'server.feedback.status.resolved', closed: 'server.feedback.status.closed'});
const statusLabel = (status: FeedbackStatus) => t(STATUS[status]);
const categoryLabel = (report: FeedbackReport) => t(CATEGORY[feedbackTriageCategory(report)] ?? 'server.feedback.category.other');
const firstLine = (message: string) => message.split('\n').find(line => line.trim().length > 0)?.trim() ?? '';

/** The drawn Feedback tab: the viewer feedback inbox and the report it opens. */
export function FeedbackPanel(props: FeedbackPanelProps) {
  const state = useSyncExternalStore(props.subscribe, props.source.getSnapshot, props.source.getSnapshot);
  const [open, setOpen] = useState<{source: SettingsFeedbackSource; report: FeedbackReport}>();
  useEffect(() => {void props.source.load();}, [props.source]);
  if (state.phase === 'error') return <p className={styles.failure} role="alert"><span>{t('next.states.pageFailedBody')}</span><button type="button" onClick={() => {void props.source.retry();}}>{t('next.shell.tryAgain')}</button></p>;
  if (state.phase === 'idle' || state.phase === 'loading' && !state.reports.length) return <p role="status">{t('next.settings.loading')}</p>;
  return <div className={styles.reports}>
    <section className={styles.panel}>
      <div className={styles.panelHead}><h3>{t('settings.server.feedback')}</h3></div>
      {state.reports.length ? <ul className={styles.rows}>{state.reports.map(report => <li key={report.id}>
        <button type="button" className={styles.feedbackRow} onClick={() => setOpen({source: props.source, report})}>
          <Icon name="i-bubble" size={20} />
          <span className={styles.reportText}><b>{firstLine(report.message) || t('server.reports.empty')}</b>
            <small>{t('web.feedback.rowSubtitle', {category: categoryLabel(report), when: defaultI18n.dateTime(report.createdAt), about: report.itemId ? 'yes' : 'no'})}</small></span>
          <span className={styles.status} data-status={report.status}>{statusLabel(report.status)}</span>
          <Icon name="i-right" size={16} />
        </button>
      </li>)}</ul> : <p className={styles.none}>{t('web.feedback.inboxEmpty')}</p>}
      {state.next && <div className={styles.pager}><Button variant="glass" size="small" onClick={() => {void props.source.more();}}>{t('server.showMore')}</Button></div>}
    </section>
    {open?.source === props.source && <TriageDialog key={open.report.id} report={open.report} source={props.source} onClose={() => setOpen(undefined)} />}
  </div>;
}

function TriageDialog({report, source, onClose}: Readonly<{report: FeedbackReport; source: SettingsFeedbackSource; onClose: () => void}>) {
  const [status, setStatus] = useState<FeedbackStatus>(report.status), [reply, setReply] = useState(''), [busy, setBusy] = useState(false), [saved, setSaved] = useState(false);
  const [loaded, setLoaded] = useState<FeedbackReport>(report), cancel = useRef<HTMLButtonElement>(null);
  const [error, setError] = useState(false);
  const life = useMemo<{controller?: AbortController; saving?: AbortController}>(() => { /* Preserve the original ownership/invalidation keys. */ void source; void report; return ({}); }, [source, report]);
  useLayoutEffect(() => {const controller = new AbortController();life.controller = controller;return () => {controller.abort();if (life.controller === controller) life.controller = undefined;};}, [life]);
  const close = () => {life.controller?.abort();life.controller = undefined;onClose();};
  useEffect(() => {
    const controller = life.controller;
    if (!controller || controller.signal.aborted) return;
    void source.read(report.id, controller.signal).then(value => {
      if (life.controller === controller && !controller.signal.aborted) {setLoaded(value);setStatus(value.status);}
    }).catch(() => {if (life.controller === controller && !controller.signal.aborted) setError(true);});
  }, [report.id, source, life]);
  const save = async () => {
    const controller = life.controller;
    if (!controller || controller.signal.aborted || life.saving || busy) return;
    life.saving = controller;setBusy(true);setError(false);
    try {
      await source.triage(loaded, status, reply, controller.signal);
      if (life.controller === controller && !controller.signal.aborted) {setSaved(true);close();}
    } catch {if (life.controller === controller && !controller.signal.aborted) setError(true);}
    finally {if (life.saving === controller) life.saving = undefined;if (life.controller === controller && !controller.signal.aborted) setBusy(false);}
  };
  const sentFrom = feedbackSentFrom(loaded);
  const meta = t('web.feedback.reportedMeta', {when: defaultI18n.dateTime(loaded.createdAt), status: statusLabel(loaded.status)});
  return <Dialog open onOpenChange={value => {if (!value) close();}} size="lg" title={categoryLabel(loaded)} initialFocusRef={cancel}
    footer={<><Button ref={cancel} size="small" onClick={close}>{t('next.settings.cancel')}</Button>
      <SettingsAction size="small" appearance="primary" loading={busy} disabled={busy || saved} onClick={() => {void save();}}>{t('web.feedback.updateReport')}</SettingsAction></>}>
    {error && <p className={styles.failure} role="alert">{t('error.unconfirmedChange')}</p>}
    <p className={styles.reportMeta}>{meta}</p>
    <p className={styles.message}>{loaded.message}</p>
    {sentFrom && <p className={styles.reportMeta}>{t('server.feedback.sentFrom', sentFrom)}</p>}
    <div className={styles.field}>
      <span className={styles.fieldLabel}>{t('web.feedback.status')}</span>
      <RadioGroup.Root className={styles.statusChoice} orientation="horizontal" aria-label={t('web.feedback.status')} value={status} disabled={busy}
        onValueChange={value => {const next = STATUSES.find(entry => entry === value);if (next) setStatus(next);}}>{STATUSES.map(value => <RadioGroup.Item key={value} value={value}
        className={styles.statusOption} data-status={value}>{statusLabel(value)}</RadioGroup.Item>)}</RadioGroup.Root>
    </div>
    <div className={styles.field}>
      <label className={styles.fieldLabel} htmlFor="feedback-reply">{t('web.feedback.replyLabel')}</label>
      <textarea id="feedback-reply" className={styles.reply} maxLength={2000} rows={4} value={reply} disabled={busy} onChange={event => setReply(event.currentTarget.value)} />
      <p className={styles.help}>{t('web.feedback.replyHelp')}</p>
    </div>
    <details className={styles.technical}><summary>{t('web.capabilities.technical')}</summary>
      <dl><dt>{t('server.feedback.field.report')}</dt><dd>{loaded.id}</dd><dt>{t('server.feedback.field.revision')}</dt><dd>{loaded.revision}</dd>
        {loaded.itemId && <><dt>{t('server.feedback.field.item')}</dt><dd>{loaded.itemId}</dd></>}</dl>
    </details>
  </Dialog>;
}
