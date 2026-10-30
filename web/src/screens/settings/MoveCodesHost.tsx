import {useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import type {AccountMove, AccountMoveCode, AccountMoveWaiting} from '@core/account-move.ts';
import {defaultI18n} from '@i18n';
import {CodedError, unreadableServerResponse} from '@core/server-messages.ts';
import {Button} from '../../kit/controls/Button';
import type {OwnerMoveCaller, SettingsOwnerSwitch} from './owner-switch';
import {MoveFinishConfirmation} from './MoveFinishConfirmation';
import {useMoveCodePublisher} from './move-codes-port';
import {MoveCodesSheet} from './MoveCodesSheet';
import styles from './owner-signin-switch.module.css';

const t = defaultI18n.t;
type Waiting = Readonly<{items: readonly AccountMoveWaiting[]; truncated: boolean}>;
/** One committed flight: this exact source, move revision and lifetime ticket.
 * Handlers are rendered against one flight and can only ever act on it. */
type Flight = {
	readonly id: number; readonly source: SettingsOwnerSwitch; readonly contextId: string;
	readonly move: AccountMove; readonly controller: AbortController;
	readonly isCurrent: () => boolean; retired: boolean; pending: boolean;
};
type Held<T> = Readonly<{flight: Flight; value: T}>;
type Receipt = Readonly<{flight: Flight; codes: readonly AccountMoveCode[]; targets: readonly string[]; issuer: string}>;

const retire = (flight: Flight) => {if (!flight.retired) {flight.retired = true; flight.controller.abort();}};

/**
 * Frames 24d and 24c2: the owner makes codes for the exact people still waiting,
 * one person at a time or everyone listed. Each flight is bound to one exact
 * source, move revision and abort ticket. Layout commits a fresh lifetime, and
 * the rendered source/move witness refuses a replaced flight before cleanup.
 * Every handler only acts
 * through the flight it was drawn with, so a retained button cannot issue a
 * successor's targets or finish a move it did not draw. The sheet's address is
 * the issuer origin proved for this server, never the page's own location.
 */
export function MoveCodesHost({source, serverName, isCurrent, onError}: {
	source: SettingsOwnerSwitch; serverName: string; isCurrent: () => boolean; onError: (error: unknown) => void;
}) {
	const move = useSyncExternalStore(source.subscribeMove, source.getMove);
	const [waiting, setWaiting] = useState<Held<Waiting>>();
	const [receipt, setReceipt] = useState<Receipt>();
	const [busy, setBusy] = useState<Held<string>>();
	const [note, setNote] = useState(''), [flight, setFlight] = useState<Flight>();
	const [confirmation, setConfirmation] = useState<Flight>();
	const sequence = useRef(0), flightRef = useRef<Flight | undefined>(undefined);
	const rendered = useRef({source, move, isCurrent, onError});
	rendered.current = {source, move, isCurrent, onError};
	const refreshed = useRef<SettingsOwnerSwitch | undefined>(undefined);
	const live = useCallback((target: Flight | undefined): target is Flight =>
		target !== undefined && target === flightRef.current && !target.retired && !target.controller.signal.aborted &&
		rendered.current.source === target.source && rendered.current.move === target.move && target.source.getMove() === target.move && target.isCurrent(), []);
	useEffect(() => {
		if (refreshed.current !== source) {refreshed.current = source; source.refreshMove();}
	}, [source]);
	useLayoutEffect(() => {
		setWaiting(undefined); setReceipt(undefined); setBusy(undefined); setNote('');
		if (!move) {setFlight(undefined); return;}
		const opened = rendered.current;
		const target: Flight = {id: ++sequence.current, source, contextId: source.contextId, move,
			controller: new AbortController(), isCurrent: opened.isCurrent, retired: false, pending: false};
		flightRef.current = target; setFlight(target);
		void target.source.waiting(target.controller.signal).then(list => {
			if (live(target)) setWaiting({flight: target, value: list});
		}, error => {if (live(target)) opened.onError(error);});
		return () => {retire(target); if (flightRef.current === target) flightRef.current = undefined;};
	}, [source, move, live]);
	const makeFor = useCallback(async (target: Flight, ids?: readonly string[]) => {
		const list = waiting && waiting.flight === target ? waiting.value : undefined;
		if (!live(target) || target.pending || !list) return;
		const targets = ids ?? list.items.map(row => row.accountId);
		// Exactly the rendered people: no invented target, and no whole-population
		// action while the read proves it listed only the first 2000.
		if (!targets.length || targets.some(id => !list.items.some(row => row.accountId === id))) return;
		if (!ids && list.truncated) return;
		const issuer = target.source.issuerOrigin();
		if (!issuer) {onError(new CodedError('invalid_account_move', unreadableServerResponse)); return;}
		target.pending = true; setBusy({flight: target, value: ids ? ids[0] ?? '' : 'all'}); setNote('');
		try {
			const codes = await target.source.codes(targets, target.controller.signal, target.move);
			if (!live(target)) return;
			// The receipt must be exactly these targets before anything is shown.
			if (codes.length !== targets.length || targets.some(id => !codes.some(code => code.accountId === id))) return;
			setReceipt({flight: target, codes, targets, issuer});
			setWaiting(current => current?.flight === target ? {flight: target, value: {...current.value,
				items: current.value.items.map(row => {const code = codes.find(code => code.accountId === row.accountId);
					return code ? {...row, expiresAt: code.expiresAt} : row;})}} : current);
		} catch (error) {if (live(target)) onError(error);}
		finally {
			target.pending = false;
			setBusy(current => current && current.flight === target ? undefined : current);
		}
	}, [waiting, live, onError]);
	const finish = async (target: Flight, caller: OwnerMoveCaller) => {
		if (!live(target) || target.pending) return;
		target.pending = true; setBusy({flight: target, value: 'finish'}); setNote('');
		try {await target.source.finishMove(target.move, {isCurrent: () => live(target) && caller.isCurrent(), signal: caller.signal ? AbortSignal.any([target.controller.signal, caller.signal]) : target.controller.signal});}
		catch (error) {if (live(target)) onError(error);throw error;}
		finally {
			target.pending = false;
			setBusy(current => current && current.flight === target ? undefined : current);
		}
	};
	// A retained sheet may dismiss only the receipt it was drawn for.
	const close = (target: Receipt) => {setReceipt(current => current === target ? undefined : current);};
	const current = live(flight) ? flight : undefined, list = waiting && waiting.flight === current ? waiting.value : undefined;
	const receiptShown = receipt && receipt.flight === current ? receipt : undefined;
	const held = busy && busy.flight === current ? busy.value : undefined;
	// Publish this flight's narrow row port while it stands; the People table's row
	// action then issues through this same receipt owner, never a second one.
	const publishPort = useMoveCodePublisher();
	useLayoutEffect(() => {
		if (!current) {publishPort(undefined); return;}
		const expires = new Map<string, string>();
		if (list) for (const row of list.items) if (row.expiresAt) expires.set(row.accountId, row.expiresAt);
		publishPort({expires, busy: held ?? '', make: accountId => {void makeFor(current, [accountId]);}});
		return () => publishPort(undefined);
	}, [current, list, held, publishPort, makeFor]);
	return <>
		<div className={styles.codesRow}>
			<span>{t('accounts.move.sheetTitle')}
				<small>{move ? t('accounts.settings.waiting', {waiting: move.waiting, total: move.people})
					: list ? t(list.truncated ? 'accounts.move.waitingTruncated' : 'accounts.move.waitingCount', {count: defaultI18n.number(list.items.length)})
						: t('accounts.move.waitingHelp')}</small>
			</span>
			{current && list ? <Button variant="glass" size="small" loading={held === 'all'} disabled={Boolean(held) || list.truncated}
				onClick={() => void makeFor(current)}>{t('accounts.move.makeCodes')}</Button> : null}
			{current ? <Button variant="glass" size="small" loading={held === 'finish'} disabled={Boolean(held)} onClick={() => {if (live(current)) setConfirmation(current);}}>{t('accounts.settings.finishNow')}</Button> : null}
		</div>
		{move ? <p className={styles.hostNote}>{t('accounts.settings.finishNowHelp')}</p> : null}
		{note && <p className={styles.hostNote}>{note}</p>}
		{confirmation && live(confirmation) ? <MoveFinishConfirmation move={confirmation.move} isCurrent={() => live(confirmation)}
			onClose={() => setConfirmation(value => value === confirmation ? undefined : value)} onConfirm={caller => finish(confirmation, caller)}/> : null}
		{receiptShown ? <MoveCodesSheet codes={receiptShown.codes} serverName={serverName} address={receiptShown.issuer}
			isCurrent={() => live(receiptShown.flight)} onClose={() => close(receiptShown)} onError={onError}/> : null}
	</>;
}
