import {useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import type {WebQueueListEntry, WebQueueListPort} from '../../platform/playback';
import {Icon} from '../../kit/Icon';
import {SidePanel} from '../../kit/overlays';
import type {WatchPanelContext} from './types';
import styles from './queue-list.module.css';

const t = defaultI18n.t;

/** desktop-player f1q: the rest of the real queue in its own order, from the captured run the rows
 * were drawn for. Play now, Move up, Move down and Remove are the caption's four actions; the
 * port owns revision fencing, boundary checks and the failure line, so nothing here retries or
 * claims a local change. A hidden entry is a redacted row, never an invented title. */
export function WatchQueueList({port, context}: {port: WebQueueListPort; context: WatchPanelContext}) {
  const snapshot = useSyncExternalStore(port.subscribe, port.getSnapshot, port.getSnapshot);
  type Opening = Readonly<{port: WebQueueListPort; life: AbortController}>;
  const active = useRef<Opening | undefined>(undefined), [opening, publish] = useState<Opening>();
  useLayoutEffect(() => {
    const held: Opening = {port, life: new AbortController()};
    active.current = held;publish(held);
    return () => {if (active.current === held) active.current = undefined;held.life.abort();};
  }, [port]);
  const held = opening?.port === port && active.current === opening && !opening.life.signal.aborted ? opening : undefined;
  const closePort = context.close;
  const close = useCallback(() => {
    if (!held || active.current !== held || held.life.signal.aborted) return;
    active.current = undefined;held.life.abort();closePort();
  }, [held, closePort]);
  // The drawn snapshot's own current flag is the admission; the row data around it
  // changes constantly and must not re-run this close.
  const queueCurrent = snapshot.current;
  useEffect(() => { if (!queueCurrent) close(); }, [queueCurrent, close]);
  const name = (row: WebQueueListEntry) => row.title ?? (row.pending ? t('next.states.stillLoading') : t('player.queueUnavailable'));
  // A refusal is already drawn from the port's own snapshot; the command is never repeated here.
  const act = (run: () => Promise<void>) => {
    if (!held || active.current !== held || held.life.signal.aborted || !port.isCurrent()) return;
    void run().catch(() => undefined);
  };
  if (!held) return null;
  const lifetime = {signal: held.life.signal, isCurrent: () => active.current === held && !held.life.signal.aborted && port.isCurrent()};
  return <SidePanel open onOpenChange={value => { if (!value) close(); }} title={t('player.queue')}
    returnFocusRef={context.returnFocusRef} width={420} closeOnOutsidePress>
    {!snapshot.current ? null : snapshot.entries.length <= 1 && snapshot.more === 0
      ? <p className={styles.empty}>{t('player.queueEmpty')}</p>
      : <>
        <div className={styles.list} aria-busy={snapshot.change === 'sending' || undefined} data-watch-queue>
          {snapshot.entries.map(row => <div key={row.entryId} className={styles.row} aria-current={row.playing ? 'true' : undefined}>
            <button type="button" className={styles.name} disabled={!row.canPlay} aria-label={t('player.queuePlay', {title: name(row)})}
              onClick={() => act(() => port.play(snapshot, row.entryId, lifetime))}>
              <span>{name(row)}</span>{row.playing && <small>{t('player.queueNowPlaying')}</small>}
            </button>
            {!row.playing && <span className={styles.actions}>
              <button type="button" className={styles.action} disabled={!row.canMoveUp} aria-label={t('player.queueMoveUp')} onClick={() => act(() => port.move(snapshot, row.entryId, -1, lifetime))}><Icon name="i-up" size={18} /></button>
              <button type="button" className={styles.action} disabled={!row.canMoveDown} aria-label={t('player.queueMoveDown')} onClick={() => act(() => port.move(snapshot, row.entryId, 1, lifetime))}><Icon name="i-down" size={18} /></button>
              <button type="button" className={styles.action} disabled={!row.canRemove} aria-label={t('player.queueRemove')} onClick={() => act(() => port.remove(snapshot, row.entryId, lifetime))}><Icon name="i-x" size={18} /></button>
            </span>}
          </div>)}
          {typeof snapshot.more === 'number' && snapshot.more > 0 && <p className={styles.more}>{t('player.queueMore', {count: snapshot.more})}</p>}
        </div>
        {snapshot.change === 'failed' && <p className={styles.failed} role="status">{t('queuePlayer.changeFailed')}</p>}
      </>}
  </SidePanel>;
}
