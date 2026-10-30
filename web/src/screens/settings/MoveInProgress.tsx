import {useCallback, useEffect, useLayoutEffect, useRef, useSyncExternalStore, useState, type ReactNode} from 'react';
import type {AccountMove} from '@core/account-move.ts';
import {defaultI18n} from '@i18n';
import {Button} from '../../kit/controls/Button';
import type {OwnerMoveCaller, SettingsOwnerSwitch} from './owner-switch';
import {MoveFinishConfirmation} from './MoveFinishConfirmation';
import styles from './move-in-progress.module.css';

const t = defaultI18n.t;

/** One committed panel scope: the exact source, move revision and lifetime ticket
 * the buttons were drawn with. A replacement retires it before layout cleanup. */
type Scope = {readonly source: SettingsOwnerSwitch; readonly move: AccountMove; readonly controller: AbortController; readonly isCurrent: () => boolean; retired: boolean; pending: boolean};

const retire = (scope: Scope) => {if (!scope.retired) {scope.retired = true; scope.controller.abort();}};

/** One subscription for the real move in flight: the same owner-fenced read the
 * switch rows consult. A refusal or outage leaves `undefined`, never a guess. */
const noSubscribe = () => () => {};
const noMove = () => null;
export function SignInMoveWatch({source, children}: {
  source?: SettingsOwnerSwitch; children: (move: AccountMove | undefined) => ReactNode;
}) {
  const move = useSyncExternalStore(source?.subscribeMove ?? noSubscribe, source?.getMove ?? noMove);
  // One read per captured context; refreshMove itself refuses when the surface is gone.
  const read = useRef('');
  useEffect(() => { if (source && read.current !== source.contextId) { read.current = source.contextId; source.refreshMove(); } }, [source]);
  return <>{children(move ?? undefined)}</>;
}

/** Frame 24c: the one move in flight, with the server's own counts and the two
 * real actions. It makes no codes, admits nobody and holds no credential. */
export function MoveInProgress({move, source, isCurrent, onPeople, onError}: {
  move: AccountMove; source: SettingsOwnerSwitch; isCurrent: () => boolean; onPeople: () => void; onError: (error: unknown) => void;
}) {
  const [busy, setBusy] = useState<Scope>(), [scope, setScope] = useState<Scope>();
  const [confirmation, setConfirmation] = useState<Scope>();
  const scopeRef = useRef<Scope | undefined>(undefined), rendered = useRef({source, move, isCurrent});
  rendered.current = {source, move, isCurrent};
  const live = useCallback((target: Scope) => target === scopeRef.current && !target.retired && !target.controller.signal.aborted &&
    rendered.current.source === target.source && rendered.current.move === target.move && target.source.getMove() === target.move && target.isCurrent(), []);
  useLayoutEffect(() => {
    const target: Scope = {source, move, controller: new AbortController(), isCurrent: rendered.current.isCurrent, retired: false, pending: false};
    scopeRef.current = target; setScope(target); setBusy(undefined);
    return () => {retire(target); if (scopeRef.current === target) scopeRef.current = undefined;};
  }, [source, move]);
  const current = scope && live(scope) ? scope : undefined, pending = current !== undefined && busy === current;
  const moved = move.people - move.waiting, percent = move.people > 0 ? Math.round((Math.max(0, moved) / move.people) * 100) : 0;
  const finish = async (target: Scope, caller: OwnerMoveCaller) => {
    if (target.pending || !live(target)) return;
    target.pending = true; setBusy(target);
    try { await target.source.finishMove(target.move, {isCurrent: () => live(target) && caller.isCurrent(), signal: caller.signal ? AbortSignal.any([target.controller.signal, caller.signal]) : target.controller.signal}); }
    catch (error) { if (live(target)) onError(error); throw error; }
    finally { target.pending = false; setBusy(value => value === target ? undefined : value); }
  };
  return <section className={styles.panel} aria-live="polite">
    <h3>{t(move.to === 'local' ? 'accounts.settings.movingToLocal' : 'accounts.settings.movingToPortico')}</h3>
    <p className={styles.count}>{t('accounts.settings.waiting', {waiting: move.waiting, total: move.people})}</p>
    <div className={styles.bar} role="progressbar" aria-valuemin={0} aria-valuemax={move.people} aria-valuenow={Math.max(0, moved)}
      aria-label={t('accounts.settings.moved', {moved: Math.max(0, moved), total: move.people})}><span style={{width: `${percent}%`}} /></div>
    <p className={styles.help}>{t('accounts.settings.finishNowHelp')}</p>
    <div className={styles.actions}>
      <Button size="small" variant="glass" onClick={() => {if (current && live(current)) onPeople();}}>{t('accounts.settings.openPeople')}</Button>
      <Button size="small" variant="primary" loading={pending} disabled={!current || pending} onClick={() => {if (current && live(current)) setConfirmation(current);}}>{t('accounts.settings.finishNow')}</Button>
    </div>
    {confirmation && live(confirmation) ? <MoveFinishConfirmation move={confirmation.move} isCurrent={() => live(confirmation)}
      onClose={() => setConfirmation(value => value === confirmation ? undefined : value)} onConfirm={caller => finish(confirmation, caller)}/> : null}
  </section>;
}
