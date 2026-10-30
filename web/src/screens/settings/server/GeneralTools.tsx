import {useLayoutEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import {restartServer} from '@core/backups.ts';
import type {HttpLocalApi} from '@core/index.ts';
import {randomId} from '@core/random-id.ts';
import {broadcastBody, broadcastProblem, utf8Length, type BroadcastProblem} from '@core/server-admin/panel-logic.ts';
import {routePath} from '../../../../../packages/contracts/src/routes.ts';
import {Button, TextField} from '../../../kit/controls';
import {Segmented} from '../../../kit/controls/Choices';
import {Dialog} from '../../../kit/overlays/Dialog';
import {SettingsAction} from '../../../kit/settings';
import styles from './server.module.css';

const t = defaultI18n.t;
type Api = Pick<HttpLocalApi, 'request'>;
type ToolsProps = Readonly<{api: Api; isCurrent: () => boolean; reportError: (error: unknown) => void; disabled?: boolean}>;

/** General › Tools, "Message everyone": the drawn composer over the real notification
 * broadcast. One operation id per opening, reused by a retry, and the server's own
 * UTF-8 byte limits decide when the message may be sent. */
export function GeneralMessage({api, isCurrent, reportError, disabled, label}: ToolsProps & Readonly<{label: string}>) {
  const [open, setOpen] = useState(false), [title, setTitle] = useState(''), [body, setBody] = useState('');
  const [audience, setAudience] = useState<'profile' | 'account-admin'>('profile');
  const [severity, setSeverity] = useState<'info' | 'warning' | 'critical'>('info');
  const [busy, setBusy] = useState(false), [problem, setProblem] = useState<BroadcastProblem>('');
  const [sent, setSent] = useState(false), [failed, setFailed] = useState(false);
  const alive = useRef(true), operation = useRef('');
  useLayoutEffect(() => {alive.current = true; return () => {alive.current = false;};}, []);
  const recheck = (nextTitle: string, nextBody: string) => setProblem(broadcastProblem(nextTitle, nextBody));
  const start = () => {
    if (disabled || busy || !isCurrent()) return;
    operation.current = randomId();
    setTitle(''); setBody(''); setProblem(''); setSent(false); setFailed(false); setOpen(true);
  };
  const close = () => {if (!busy) setOpen(false);};
  const send = () => {
    if (busy || !isCurrent()) return;
    const issue = broadcastProblem(title, body); setProblem(issue);
    if (issue || !title.trim() || !body.trim()) return;
    const id = operation.current || (operation.current = randomId());
    setBusy(true); setFailed(false);
    void api.request(routePath('post_admin_notifications_broadcast'), 'POST', broadcastBody(id, {audience, severity, title, body}))
      .then(() => {
        if (!alive.current) return;
        setSent(true); setOpen(false); setTitle(''); setBody('');
      }, error => {
        if (!alive.current) return;
        setFailed(true); reportError(error);
      })
      .finally(() => {if (alive.current) setBusy(false);});
  };
  const problemText = problem === 'title-too-long' ? t('web.broadcast.titleTooLong', {count: utf8Length(title.trim())})
    : problem === 'body-too-long' ? t('web.broadcast.bodyTooLong', {count: utf8Length(body.trim())}) : undefined;
  return <>
    <div className={styles.generalTool}>
      <SettingsAction appearance="glass" disabled={disabled || busy} onClick={start}>{label}</SettingsAction>
      {sent && <span role="status" className={styles.storageNote}>{t('server.broadcast.sent')}</span>}
      {failed && <span role="alert" className={styles.backupFailure}>{t('error.unavailable')}</span>}
    </div>
    <Dialog open={open} title={t('server.messageEveryone')} description={t('web.broadcast.lede')} onOpenChange={value => {if (!value) close();}}
      footer={<><Button size="compact" variant="glass" disabled={busy} onClick={close}>{t('action.cancel')}</Button>
        <Button size="compact" variant="primary" loading={busy} disabled={busy || !title.trim() || !body.trim() || problem !== ''} onClick={send}>{t('feedback.send')}</Button></>}>
      <div className={styles.composerFields}>
        <TextField label={t('web.broadcast.title')} value={title} maxLength={200} placeholder={t('web.broadcast.titleExample')} disabled={busy}
          onChange={event => {setTitle(event.currentTarget.value); recheck(event.currentTarget.value, body);}}/>
        <label className={styles.composerLabel} htmlFor="server-general-message-body">{t('web.broadcast.message')}</label>
        <textarea id="server-general-message-body" className={styles.composerBody} rows={5} maxLength={1000} value={body} disabled={busy}
          onChange={event => {setBody(event.currentTarget.value); recheck(title, event.currentTarget.value);}}/>
        <span className={styles.composerLabel}>{t('web.broadcast.audience')}</span>
        <Segmented aria-label={t('web.broadcast.audience')} value={audience} disabled={busy} onValueChange={value => setAudience(value as typeof audience)}
          items={[{value: 'profile', label: t('web.together.everyone')}, {value: 'account-admin', label: t('web.broadcast.adminOnly')}]}/>
        <span className={styles.composerLabel}>{t('web.broadcast.importance')}</span>
        <Segmented aria-label={t('web.broadcast.importance')} value={severity} disabled={busy} onValueChange={value => setSeverity(value as typeof severity)}
          items={[{value: 'info', label: t('web.broadcast.info')}, {value: 'warning', label: t('web.broadcast.notice')}, {value: 'critical', label: t('web.broadcast.urgent')}]}/>
        {problemText && <p className={styles.composerProblem} role="alert">{problemText}</p>}
      </div>
    </Dialog>
  </>;
}

/** General › Tools, "Restart the server": the drawn confirmation over the real restart
 * command. Acceptance is not a restart that happened, so the row only says it is
 * restarting; apps reconnect by themselves. */
export function GeneralRestart({api, isCurrent, reportError, disabled, label, confirm}: ToolsProps & Readonly<{label: string; confirm?: string}>) {
  const [open, setOpen] = useState(false), [busy, setBusy] = useState(false), [state, setState] = useState<'idle' | 'waiting' | 'failed'>('idle');
  const alive = useRef(true);
  useLayoutEffect(() => {alive.current = true; return () => {alive.current = false;};}, []);
  const close = () => {if (!busy) setOpen(false);};
  const restart = () => {
    if (busy || !isCurrent()) return;
    setBusy(true); setState('idle');
    void restartServer(api).then(() => {
      if (!alive.current) return;
      setState('waiting'); setOpen(false);
    }, error => {
      if (!alive.current) return;
      setState('failed'); reportError(error);
    }).finally(() => {if (alive.current) setBusy(false);});
  };
  return <>
    <div className={styles.generalTool}>
      <SettingsAction appearance="danger" disabled={disabled || busy} onClick={() => {if (!disabled && isCurrent()) setOpen(true);}}>{label}</SettingsAction>
      {state === 'waiting' && <span role="status" className={styles.storageNote}>{t('web.maintenance.restartingTitle')}</span>}
      {state === 'failed' && <span role="alert" className={styles.backupFailure}>{t('server.restart.failed')}</span>}
    </div>
    <Dialog open={open} title={t('server.restart.title')} onOpenChange={value => {if (!value) close();}}
      footer={<><Button size="compact" variant="glass" disabled={busy} onClick={close}>{t('action.cancel')}</Button>
        <Button size="compact" variant="primary" loading={busy} disabled={busy} onClick={restart}>{t('web.maintenance.serverRestart')}</Button></>}>
      <p>{confirm ?? t('server.restart.body')}</p>
    </Dialog>
  </>;
}
