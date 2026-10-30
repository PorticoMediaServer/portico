import {useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import type {MissingTitlesPage} from '@core/title-identity.ts';
import {Button, TextField} from '../../../kit/controls';
import {Artwork} from '../../../kit/artwork/Artwork';
import type {ArtworkSource} from '../../../kit/artwork/types';
import {Dialog} from '../../../kit/overlays/Dialog';
import {SettingsMissingSource} from './missing-source';
import styles from './missing-titles.module.css';
const t = defaultI18n.t;
type Removal = {page: MissingTitlesPage; source: SettingsMissingSource; live: boolean; claimed: boolean; typed: string};

/** The current server's missing list. Removal captures the page the owner read;
 * one confirmation makes one bounded pass, even when the server reports more. */
export function MissingTitles({source, isCurrent, artwork}: Readonly<{source: SettingsMissingSource; isCurrent: () => boolean; artwork: (id: string) => ArtworkSource | undefined}>) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [confirmation, setConfirmation] = useState<Removal>(), [typed, setTyped] = useState(''), [cursors, setCursors] = useState<readonly string[]>(['']);
  const generation = useRef<object>({}), paging = useRef(false), removal = useRef<Removal | undefined>(undefined);
  useEffect(() => {generation.current = {};setConfirmation(undefined);setTyped('');setCursors(['']);void source.load();return () => {generation.current = {};};}, [source]);
  const page = state.page, busy = state.busy || state.phase === 'loading', live = isCurrent();
  const changePage = async (cursor: string, next: readonly string[]) => {
    if (!isCurrent() || paging.current || busy) return;
    paging.current = true;const held = generation.current;
    try {await source.load(cursor);if (isCurrent() && held === generation.current && source.getSnapshot().phase === 'ready') setCursors(next);}
    finally {paging.current = false;}
  };
  const retire = useCallback((attempt: Removal | undefined) => {
    if (!attempt || removal.current !== attempt) return;
    attempt.live = false; removal.current = undefined;
    setConfirmation(value => value === attempt ? undefined : value); setTyped('');
  }, []);
  useLayoutEffect(() => {
    const attempt = removal.current;
    if (attempt && (attempt.source !== source || attempt.page !== page)) retire(attempt);
  }, [source, page, retire]);
  useLayoutEffect(() => () => {const attempt = removal.current; if (attempt) {attempt.live = false; removal.current = undefined;}}, [source]);
  const remove = async (attempt: Removal, capturedTyped: string) => {
    const current = () => attempt.live && removal.current === attempt && attempt.source === source && isCurrent();
    if (!current() || attempt.claimed || source.getSnapshot().page !== attempt.page || attempt.typed !== capturedTyped || capturedTyped !== String(attempt.page.total)) return;
    attempt.claimed = true;
    await source.remove(attempt.page, capturedTyped, current);
    if (current() && source.getSnapshot().page !== attempt.page) retire(attempt);
    if (isCurrent() && source.getSnapshot().removed) setCursors(['']);
  };
  return <div className={styles.section}>
    {state.error && <p role="alert">{t(state.error === 'remove' ? 'error.unconfirmedChange' : 'error.unavailable')} <Button size="xs" disabled={busy} onClick={() => {if (isCurrent()) void source.refresh();}}>{t('action.refresh')}</Button></p>}
    {state.removed && <p role="status" className={styles.note}>{t('web.missingTitles.removed', {count: state.removed.titles})}</p>}
    {!page && busy && <p role="status">{t('next.settings.loading')}</p>}
    {page && <div className={styles.box}><ul className={styles.list}>{page.items.map(entry => <li key={entry.id} className={styles.row}>
      <Artwork source={artwork(entry.id)} alt="" width={40} height={60} className={styles.poster} />
      <div className={styles.title}><b>{entry.parentTitle ? `${entry.parentTitle} · ${entry.title}` : entry.title}</b><p>{entry.removedAt ? t('web.missingTitles.sinceRemoved', {when: defaultI18n.dateTime(entry.missingSince), removes: defaultI18n.date(entry.removedAt)}) : t('web.missingTitles.since', {when: defaultI18n.dateTime(entry.missingSince)})}</p></div>
    </li>)}</ul>{!page.items.length && <p className={styles.empty}>{t('web.missingTitles.none')}</p>}
      {page.total > 0 && <div className={styles.footer}><Button size="xs" variant="glass" disabled={busy || !page.observedAt} onClick={() => {if (isCurrent() && source.getSnapshot().phase === 'ready' && !source.getSnapshot().busy && source.getSnapshot().page === page) {retire(removal.current); const attempt: Removal = {page, source, live: true, claimed: false, typed: ''}; removal.current = attempt; setConfirmation(attempt);setTyped('');}}}>{t('web.missingTitles.removeNow')}</Button><span className={styles.count}>{t('web.missingTitles.count', {count: page.total})}</span></div>}
    </div>}
    {(cursors.length > 1 || page?.nextCursor) && <div className={styles.pager}><Button size="xs" disabled={busy || cursors.length < 2} onClick={() => {const next = cursors.slice(0, -1);void changePage(next[next.length - 1] ?? '', next);}}>{t('web.pager.previous')}</Button><Button size="xs" disabled={busy || !page?.nextCursor} onClick={() => {if (page?.nextCursor) void changePage(page.nextCursor, [...cursors, page.nextCursor]);}}>{t('web.pager.next')}</Button></div>}
    <Dialog open={live && !!confirmation} onOpenChange={open => {if (!open) retire(confirmation);}} title={t('web.missingTitles.removeTitle')} description={t('server.missing.removeBody', {count: confirmation?.page.total ?? 0})} width={420}
      dismissOnBack={!state.busy} dismissOnOutsidePress={!state.busy} showCloseButton={!state.busy}
      footer={<><Button variant="glass" size="compact" disabled={state.busy} onClick={() => retire(confirmation)}>{t('action.cancel')}</Button><Button variant="glass" size="compact" icon="i-trash" className={styles.danger} disabled={busy || confirmation?.page !== page || typed !== String(confirmation?.page.total)} onClick={() => {if (confirmation) void remove(confirmation, typed);}}>{t('web.missingTitles.removeConfirm')}</Button></>}>
      <TextField label={t('server.missing.confirmCount', {count: confirmation?.page.total ?? 0})} inputMode="numeric" autoComplete="off" value={typed} disabled={state.busy} onChange={event => {if (confirmation && removal.current === confirmation && confirmation.live && !confirmation.claimed) {confirmation.typed = event.target.value;setTyped(event.target.value);}}} />
    </Dialog>
  </div>;
}
