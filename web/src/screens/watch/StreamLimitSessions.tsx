import {useCallback, useLayoutEffect, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {HttpLocalApi} from '@core/index.ts';
import {playbackControlsService, type RemotePlaybackControls} from '@core/playback-controls.ts';
import type {WebPlaybackStartFailure} from '../../platform/playback/start-failure';
import {Button} from '../../kit/controls';
import styles from './stream-limit.module.css';

const t = defaultI18n.t;

/** One lease of the list: the api and failed attempt it was set up for, its own abort signal and
 * service, the rows it owns, and the synchronous stop it has already started. */
type Lease = Readonly<{isCurrent: () => boolean; excludeSessionId?: string; life: AbortController;
  read(): Promise<Readonly<{items: readonly RemotePlaybackControls[]; nextCursor: string}>>;
  stop(row: RemotePlaybackControls): Promise<void>}> & {
  items: readonly RemotePlaybackControls[];
  pending?: string;
};
const offered = (lease: Lease, page: readonly RemotePlaybackControls[]) => Object.freeze(page.filter(item => item.sessionId !== lease.excludeSessionId && item.actions.includes('stop')));

/** states.html f9: the account's own other playbacks, read from the server's scoped controls
 * document, with the real Stop each one offers. Every read, row and stop belongs to one lease of
 * this failed attempt: replacement retires it synchronously, a retained handler can only name the
 * exact row it was drawn with, and a refused read draws nothing at all. */
export function WatchStreamLimitSessions({api, failure, isCurrent, excludeSessionId, reportError}: {
  api?: HttpLocalApi; failure?: WebPlaybackStartFailure; isCurrent: () => boolean; excludeSessionId?: string; reportError: (error: unknown) => void;
}) {
  const [view, setView] = useState<Readonly<{lease: Lease; pending?: string}>>();
  const active = useRef<Lease | undefined>(undefined), serial = useRef(0);
  // Refreshes the lease that is already live; it never creates or aborts one.
  const reload = useCallback((lease: Lease) => {
    if (active.current !== lease || lease.life.signal.aborted) return;
    const id = ++serial.current;
    void lease.read().then(page => {
      if (active.current !== lease || id !== serial.current || lease.life.signal.aborted || !lease.isCurrent()) return;
      lease.items = offered(lease, page.items);
      setView(current => current?.lease === lease ? {lease, ...(lease.pending ? {pending: lease.pending} : {})} : current);
    }, () => {
      if (active.current !== lease || id !== serial.current || lease.life.signal.aborted || !lease.isCurrent()) return;
      lease.items = Object.freeze([]);
      setView(current => current?.lease === lease ? {lease} : current);
    });
  }, []);
  useLayoutEffect(() => {
    if (!isCurrent() || !api && !failure) { setView(undefined); return; }
    const life = new AbortController();
    const service = api ? playbackControlsService(<T,>(path: string, method?: string, body?: unknown) => api.request<T>(path, method, body, life.signal)) : undefined;
    const lease: Lease = Object.assign({isCurrent, excludeSessionId, life,
      read: () => failure ? failure.remote() : service!.remote(),
      stop: (row: RemotePlaybackControls) => failure ? failure.stop(row) : service!.stopAccount(row)},
      {items: Object.freeze([]) as readonly RemotePlaybackControls[]});
    active.current?.life.abort();
    active.current = lease;
    setView({lease});
    const off = failure?.subscribeSessions(items => {
      if (active.current !== lease || lease.life.signal.aborted || !lease.isCurrent()) return;
      ++serial.current;lease.items = offered(lease, items);
      setView({lease, ...(lease.pending ? {pending: lease.pending} : {})});
    });
    reload(lease);
    // Retirement is synchronous: this lease, and only this lease, ends here.
    return () => {off?.();lease.life.abort(); if (active.current === lease) active.current = undefined; };
  }, [api, failure, isCurrent, excludeSessionId, reload]);
  // The handler names the rendered lease and the exact offered row; nothing is reread for authority.
  const stop = (lease: Lease, row: RemotePlaybackControls) => {
    if (active.current !== lease || lease.life.signal.aborted || lease.pending !== undefined || !lease.isCurrent()) return;
    if (!lease.items.includes(row) || !row.actions.includes('stop')) return;
    lease.pending = row.sessionId;
    setView(current => current?.lease === lease ? {lease, pending: row.sessionId} : current);
    void lease.stop(row).then(() => {
      if (active.current !== lease || lease.life.signal.aborted) return;
      // The confirmed row leaves this lease before pending clears, so a retained handler that
      // still holds it can neither pass the membership check nor start a second stop.
      lease.items = Object.freeze(lease.items.filter(item => item !== row));
      lease.pending = undefined;
      if (!lease.isCurrent()) return;
      setView(current => current?.lease === lease ? {lease} : current);
      // Rows are then re-read in place for the queue's authoritative list.
      reload(lease);
    }, error => {
      if (active.current !== lease || lease.life.signal.aborted) return;
      lease.pending = undefined;
      if (!lease.isCurrent()) return;
      setView(current => current?.lease === lease ? {lease} : current);
      reportError(error);
    });
  };
  const lease = view?.lease;
  if (!lease || !lease.items.length) return null;
  return <div className={styles.sessions}>
    <p className={styles.heading}>{t('remote.title')}</p>
    <ul>{lease.items.map(item => <li key={item.sessionId}>
      <span><b>{item.device.name}</b><small>{item.item?.title ?? t('remote.hiddenTitle')}</small></span>
      <Button variant="glass" size="small" disabled={view.pending !== undefined} loading={view.pending === item.sessionId} onClick={() => stop(lease, item)}>{t('action.stop')}</Button>
    </li>)}</ul>
  </div>;
}
