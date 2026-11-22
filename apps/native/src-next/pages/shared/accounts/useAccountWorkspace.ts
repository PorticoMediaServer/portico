import {useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import type {ScrollView} from 'react-native';
import type {NativeAccountWorkspace, AccountWorkspacePage, AccountWorkspaceSnapshot} from '../../../platform/account-workspace';
import type {PorticoSessionRow} from '../../../../../../packages/client-core/src/portico-services-native';
import type {HostedServer} from '../../../../../../packages/client-core/src';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';

type CommittedOpening = {source?: NativeAccountWorkspace; active: boolean; failed: boolean};
export type AccountWorkspaceConfirm = {kind: 'session'; row: PorticoSessionRow} | {kind: 'others'} | {kind: 'server'; row: HostedServer};
const waiting: AccountWorkspaceSnapshot = Object.freeze({page: 'menu', phase: 'loading'});
const noSubscribe = () => () => {};
const waitingSnapshot = () => waiting;

/** Account-only settings with no server connected: one committed opening of the workspace source, its pages, and the
 * sign-out and leave commands behind one confirmation each. Moved from the old screen as it was. */
export function useAccountWorkspace({createSource, onDone, onFind, onJoin, onSignOut}: {
  createSource: () => NativeAccountWorkspace; onDone: () => void; onFind: () => void; onJoin: () => void; onSignOut: () => void;
}) {
  const opening = useRef<CommittedOpening | undefined>(undefined);
  const scroll = useRef<ScrollView>(null);
  const [ticket, setTicket] = useState<CommittedOpening>();
  const [confirm, setConfirm] = useState<AccountWorkspaceConfirm>();
  const [commandFailure, setCommandFailure] = useState<string>();
  useLayoutEffect(() => {
    if (opening.current) {opening.current.active = false; opening.current.source?.dispose();}
    const committed: CommittedOpening = {active: true, failed: false}; opening.current = committed;
    try {committed.source = createSource();} catch {committed.failed = true;}
    setTicket(committed);
    setConfirm(undefined); setCommandFailure(undefined);
    return () => {committed.active = false; if (opening.current === committed) opening.current = undefined; committed.source?.dispose();};
  }, [createSource]);
  const source = ticket?.source;
  const state = useSyncExternalStore(source?.subscribe ?? noSubscribe, source?.getSnapshot ?? waitingSnapshot);
  // H14: a new account page opens at its top, not at the menu's scroll offset.
  useLayoutEffect(() => {scroll.current?.scrollTo({y: 0, animated: false});}, [state.page]);
  const live = () => !!ticket?.active && opening.current === ticket && (!source || source.getSnapshot() === state);
  const current = () => live() && !!source && source.current();
  const go = (action: () => void) => () => {if (!current() || !ticket) return; ticket.active = false; source?.dispose(); action();};
  const page = (value: Exclude<AccountWorkspacePage, 'menu'>) => () => {if (current()) {setConfirm(undefined); setCommandFailure(undefined); void source?.open(value);}};
  const ending = !!state.pending || !!state.endingOthers;
  const command = (work: () => Promise<void>) => {
    if (!current() || ending) return;
    setCommandFailure(undefined);
    void work().then(() => {if (ticket?.active && source?.current()) setConfirm(undefined);}, error => {
      if (ticket?.active && source?.current()) setCommandFailure(presentError(error, 'portico-account').body);
    });
  };
  return {state, source, scroll, failedOpening: !!ticket?.failed, ending, confirm, failure: commandFailure || state.mutationFailure,
    heading: state.page === 'security' ? 'account.security' as const : state.page === 'devices' ? 'devices.title' as const
      : state.page === 'servers' ? 'web.servers.title' as const : state.page === 'notifications' ? 'web.account.notifications' as const : 'settings.title' as const,
    doneLabel: state.page === 'menu' || state.phase === 'ended' ? 'action.done' as const : 'action.back' as const,
    done: () => {
      if (!current()) {if (ticket && live() && (ticket.failed || state.phase === 'ended')) {ticket.active = false; source?.dispose(); onDone();} return;}
      setConfirm(undefined); setCommandFailure(undefined); if (state.page === 'menu') go(onDone)(); else source?.menu();
    },
    open: page, join: go(onJoin), find: go(onFind), signOut: go(onSignOut),
    retry: () => {if (current() && state.page !== 'menu') void source?.open(state.page);},
    ask: (value: AccountWorkspaceConfirm) => {if (current()) setConfirm(value);},
    cancelConfirm: () => setConfirm(undefined),
    dismissConfirm: (open: boolean) => {if (!open && !ending) setConfirm(undefined);},
    confirmAction: () => {
      const captured = confirm; if (!captured) return;
      command(() => captured.kind === 'server' ? source!.leaveServer(captured.row) : captured.kind === 'others' ? source!.revokeOtherSessions() : source!.revokeSession(captured.row));
    },
    pageNotifications: (direction: 'previous' | 'next') => {if (current()) void source?.pageNotifications(direction);}};
}
