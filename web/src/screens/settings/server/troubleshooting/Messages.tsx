import {serverNow} from '@core/server-clock.ts';
import {useEffect, useRef, useState, useSyncExternalStore, type ReactNode} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import {logLevels, type LogLevel} from '@core/server-administration.ts';
import {Button, Select, Switch} from '../../../../kit/controls';
import {SettingsMessagesSource} from './messages-source';
import {messageTime} from './message-time';
import styles from './troubleshooting.module.css';
const t = defaultI18n.t;
const levelKey = (level: LogLevel) => `web.logs.level.${level}` as MessageId;

/** `canDetail` is false for a tier that may read Messages but not open a detail window:
 *  it still sees whether one is open, with no control that would fail. The server keeps no kind
 *  for a line, so Messages filters by level only (console rule 66). */
export function MessagesPanel({source, options, canDetail = true}: Readonly<{source: SettingsMessagesSource; options?: ReactNode; canDetail?: boolean; reportError: (error: unknown) => void}>) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const box = useRef<HTMLDivElement>(null);
  const [now, setNow] = useState(serverNow());
  useEffect(() => {void source.load();return source.pause;}, [source]);
  useEffect(() => {const timer = setInterval(() => setNow(serverNow()), 30000);return () => clearInterval(timer);}, []);
  useEffect(() => {if (state.live && box.current) box.current.scrollTop = box.current.scrollHeight;}, [state.lines, state.live]);
  const until = state.detailUntil, detailed = !!until && Date.parse(until) > now;
  return <div className={styles.messagePanels}>
    {options}
    {state.phase === 'ready' && <section className={styles.detailWindow}>
      <div><b>{detailed ? t('next.settingsSpec.server.troubleshooting.groups.0.rows.0.label') : t('settings.server.detailWindow.forMinutes', {minutes: state.detailMinutes})}</b><p className={styles.help}>{detailed ? t('settings.server.detailWindow.until', {time: defaultI18n.dateTime(until!)}) : t('settings.server.detailWindow.help')}</p></div>
      {canDetail && <Button size="xs" variant="glass" disabled={state.changing} onClick={() => {void source.detail(!detailed).catch(() => { /* The source reports through its captured owner. */ });}}>{t(detailed ? 'settings.server.detailWindow.stop' : 'settings.server.detailWindow.start')}</Button>}
    </section>}
    <section className={styles.messages}>
      <div className={styles.messageHeader}><h3>{t('web.logs.messages')}<span className={styles.messageScope}>{t('web.logs.adminVisible')}</span></h3></div>
      <p className={styles.messageLede}>{t('web.logs.messagesLede')}</p>
      <div className={styles.messageBox}>
      {state.phase === 'error' && <p className={styles.messageFailure} role="alert"><span>{t('web.logs.readFailed')}</span><button type="button" onClick={() => {void source.load();}}>{t('next.shell.tryAgain')}</button></p>}
      <div className={styles.messageFilters}>
        <Select className={styles.messageSelect} aria-label={t('web.logs.level')} value={state.level || 'all'} onValueChange={value => {void source.select(value === 'all' ? '' : value as LogLevel);}}
          options={[{value: 'all', label: t('web.logs.allLevels')}, ...logLevels.map(level => ({value: level, label: t('web.logs.levelAndAbove', {level: t(levelKey(level))})}))]} />
        <label className={styles.liveChoice}><Switch checked={state.live} onCheckedChange={source.setLive} />{t('guide.live')}</label>
        {state.live ? <span className={styles.following} data-paused={state.reconnecting || undefined} role="status">{t(state.reconnecting ? 'web.logs.reconnecting' : 'web.logs.following')}</span>
          : <><span className={styles.note}>{state.readAt ? t('web.logs.updated', {time: defaultI18n.dateTime(state.readAt)}) : ''}</span><Button size="small" variant="glass" disabled={state.phase === 'loading'} onClick={() => {void source.load();}}>{t('web.logs.refresh')}</Button></>}
      </div>
      <div ref={box} className={styles.messageLog} role="log" aria-live="off" aria-busy={state.phase === 'loading'}>
        {state.lines.map(line => <div key={line.sequence} className={styles.messageLine} data-level={line.level}>
          <time dateTime={line.at}>{messageTime(defaultI18n, line.at)}</time><b data-level={line.level}>{t(levelKey(line.level))}</b><span>{line.message}{line.detail && <small className={styles.messageDetail}>{line.detail}</small>}</span>
        </div>)}
        {!state.lines.length && (state.phase === 'loading' || state.phase === 'idle' ? <div role="status" aria-label={t('web.logs.loadingMessages')}>{Array.from({length: 5}, (_, index) => <div key={index} className={styles.messageSkeleton} aria-hidden="true"><i /><i /><i /><i /></div>)}</div> : <p className={styles.none}>{t('web.logs.noMessages')}</p>)}
      </div>
      </div>
    </section>
  </div>;
}
