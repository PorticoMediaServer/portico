import React, {useEffect, useRef, useState} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {SidePanel} from '../../kit/overlays';
import {StateLayout} from '../../kit/states';
import {useTheme} from '../../kit/theme';
import type {TvHomeRecoverySession, TvHomeRecoveryTicket} from './useTvHomeRecovery';

/** states.html 5, inside the shell: the mounted Home failure keeps trying by itself while
 * the TV is in front, tries at once when it wakes, and offers the drawn Details panel.
 * Every line is a real attempt fact or drawn copy: no diagnostic code is printed. Every
 * action carries the ticket it was drawn for, so a delayed press cannot act on a later Home. */
export function TvHomeRecovery({session, onRetryNow, onSwitchServer, connectionFacts}: {
  session: TvHomeRecoverySession; onRetryNow: (ticket: TvHomeRecoveryTicket) => void; onSwitchServer?: (ticket: TvHomeRecoveryTicket) => void;
  connectionFacts?: Readonly<{serverName?: string; connectionAddress?: string}>;
}) {
  const theme = useTheme(), t = defaultI18n.t;
  const [detailsTicket, setDetailsTicket] = useState<TvHomeRecoveryTicket>(), [now, setNow] = useState(() => Date.now());
  const opener = useRef<View>(null), ticket = session.ticket;
  // The panel belongs to the ticket it was opened from: a successor never inherits it, and an
  // old panel's close cannot close the new one.
  const details = detailsTicket !== undefined && detailsTicket === ticket && !ticket.retired;
  // The countdown restarts from the new due time, never from the previous attempt's timestamp.
  useEffect(() => {setNow(Date.now()); if (session.dueAt === undefined) return; const id = setInterval(() => setNow(Date.now()), 1000); return () => clearInterval(id);}, [session.dueAt]);
  const remaining = session.dueAt === undefined ? 0 : Math.max(0, Math.ceil((session.dueAt - now) / 1000));
  const progress = session.dueAt === undefined || session.delayMs <= 0 ? undefined : Math.min(1, Math.max(0, 1 - remaining / (session.delayMs / 1000)));
  const label = session.dueAt === undefined ? t('next.states.retrying') : t('next.states.retryIn', {seconds: remaining});
  const detail = session.attempts > 0 && session.firstAt ? t('next.states.triesSince', {count: session.attempts, time: defaultI18n.time(session.firstAt)}) : undefined;
  // Frame 5 draws the connection help under the actions, not inside Details; the panel keeps
  // the attempt and address facts it was opened for.
  const facts = [detail, connectionFacts?.connectionAddress ? `${t('next.states.address')} · ${connectionFacts.connectionAddress}` : undefined, label].filter((line): line is string => !!line);
  return <>
    <StateLayout icon="i-srvoff" tone="warning" testID="tv-home-recovery"
      title={session.presented.title} body={session.presented.body}
      retry={{progress: progress ?? null, label, detail}}
      primaryAction={{id: 'retry', label: t('next.states.tryNow'), icon: 'i-retry', hasTVPreferredFocus: true, onPress: () => onRetryNow(session.ticket)}}
      secondaryAction={onSwitchServer ? {id: 'switch-server', label: t('next.states.switchServer'), icon: 'i-server', onPress: () => onSwitchServer(session.ticket)} : undefined}
      detailsAction={{id: 'details', label: t('next.states.details'), ref: opener, accessibilityState: {expanded: details}, onPress: () => {if (!ticket.retired) setDetailsTicket(ticket);}}}
      help={t('next.states.native.tvConnectionHelp')}/>
    <SidePanel testID="tv-home-recovery-details" open={details} onOpenChange={value => {if (!value) setDetailsTicket(current => current === ticket ? undefined : current);}} title={t('next.states.details')} topInset={0} width={700} returnFocusRef={opener}>
      <View style={{gap: 18}}>{facts.map(line => <Text key={line} style={[theme.type.body, {color: theme.colors.textSecondary}]}>{line}</Text>)}</View>
    </SidePanel>
  </>;
}
