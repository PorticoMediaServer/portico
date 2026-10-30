import {useEffect, useSyncExternalStore} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import {Button} from '../../../../kit/controls';
import type {PlaybackProblemsSource} from './playback-problems-source';
import styles from './troubleshooting.module.css';

const t = defaultI18n.t;
/** Actual active remembered methods only; an empty server result draws no group. */
export function PlaybackProblems({source}: {source: PlaybackProblemsSource}) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  useEffect(() => {void source.load();}, [source]);
  if (state.phase === 'idle' || !state.groups.length && state.phase !== 'error') return null;
  return <section className={styles.panel}>
    <div className={styles.panelHead}><h3>{t('server.routes.title')}</h3></div>
    <p>{t('server.routes.lede')}</p>
    {state.error !== undefined && <p className={styles.failure} role="alert">{t('next.states.pageFailedBody')} <Button size="small" variant="glass" onClick={() => {void source.load();}}>{t('next.shell.tryAgain')}</Button></p>}
    {state.groups.map(group => <div key={group.sourceId}>
      <b>{group.item?.title || t('server.routes.unknownTitle')}</b>
      {group.failures.map((failure, index) => <p key={index}>{t('server.routes.line', {route: t(`server.routes.route.${failure.route}` as MessageId), engine: t(`server.routes.engine.${failure.engine}` as MessageId)})}</p>)}
      <p>{t('server.routes.when', {when: defaultI18n.dateTime(group.reportedAtMs), until: defaultI18n.dateTime(group.expiresAtMs)})}</p>
      <Button size="small" variant="glass" disabled={!!state.busy} onClick={() => {void source.forget(group.sourceId);}}>{t('server.routes.forget')}</Button>
    </div>)}
    {state.more && <p>{t('server.routes.more')}</p>}
  </section>;
}
