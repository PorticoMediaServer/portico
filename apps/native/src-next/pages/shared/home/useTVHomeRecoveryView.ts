import {useEffect, useRef, useState} from 'react';
import type {View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {TvHomeRecoverySession, TvHomeRecoveryTicket} from '../../../screens/home/useTvHomeRecovery';

/** states.html 5, inside the shell: the mounted Home failure keeps trying by itself while the TV is in front, tries at once
 * when it wakes, and offers the drawn Details panel. Every line is a real attempt fact or drawn copy: no diagnostic code is
 * printed. Every action carries the ticket it was drawn for, so a delayed press cannot act on a later Home. */
export function useTVHomeRecoveryView({session, onRetryNow, onSwitchServer, connectionFacts}: {
  session: TvHomeRecoverySession; onRetryNow: (ticket: TvHomeRecoveryTicket) => void; onSwitchServer?: (ticket: TvHomeRecoveryTicket) => void;
  connectionFacts?: Readonly<{serverName?: string; connectionAddress?: string}>;
}) {
  const t = defaultI18n.t;
  const [detailsTicket, setDetailsTicket] = useState<TvHomeRecoveryTicket>(), [now, setNow] = useState(() => Date.now());
  const opener = useRef<View>(null), ticket = session.ticket;
  // The panel belongs to the ticket it was opened from: a successor never inherits it, and an old panel's close cannot close
  // the new one.
  const detailsOpen = detailsTicket !== undefined && detailsTicket === ticket && !ticket.retired;
  // The countdown restarts from the new due time, never from the previous attempt's timestamp.
  useEffect(() => {
    setNow(Date.now());
    if (session.dueAt === undefined) return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [session.dueAt]);
  const remaining = session.dueAt === undefined ? 0 : Math.max(0, Math.ceil((session.dueAt - now) / 1000));
  const progress = session.dueAt === undefined || session.delayMs <= 0 ? undefined
    : Math.min(1, Math.max(0, 1 - remaining / (session.delayMs / 1000)));
  const label = session.dueAt === undefined ? t('next.states.retrying') : t('next.states.retryIn', {seconds: remaining});
  const detail = session.attempts > 0 && session.firstAt
    ? t('next.states.triesSince', {count: session.attempts, time: defaultI18n.time(session.firstAt)}) : undefined;
  // Frame 5 draws the connection help under the actions, not inside Details; the panel keeps the attempt and address facts
  // it was opened for.
  const address = connectionFacts?.connectionAddress ? `${t('next.states.address')} · ${connectionFacts.connectionAddress}` : undefined;
  const facts = [detail, address, label].filter((line): line is string => !!line);
  return {
    title: session.presented.title, body: session.presented.body, retry: {progress: progress ?? null, label, detail}, facts, detailsOpen, opener,
    retryNow: () => onRetryNow(ticket),
    switchServer: onSwitchServer ? () => onSwitchServer(ticket) : undefined,
    openDetails: () => {if (!ticket.retired) setDetailsTicket(ticket);},
    closeDetails: () => setDetailsTicket(current => current === ticket ? undefined : current),
  };
}
