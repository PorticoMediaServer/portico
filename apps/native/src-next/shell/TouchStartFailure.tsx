import React, {useEffect, useLayoutEffect, useRef, useState} from 'react';
import {FlatList, Text, View, useWindowDimensions} from 'react-native';
import {playbackUserMessage} from '../../../../packages/client-core/src/playback-messages';
import type {RemotePlaybackControls} from '../../../../packages/client-core/src/playback-controls';
import type {NativePlaybackStartFailure} from '../platform/playback/start-failure';
import type {NativePlaybackCapacityWait} from '../platform/playback/capacity-retry';
import {Button} from '../kit/controls';
import {Icon} from '../kit/Icon';
import {BottomSheet} from '../kit/overlays';
import {alpha} from '../kit/overlays/surface';
import {CountdownRing} from '../kit/states';
import {useTheme} from '../kit/theme';
import type {NativeShellRuntime} from './runtime';

type Opening = {owner: NativePlaybackStartFailure | NativePlaybackCapacityWait; active: boolean; action: number; busy?: string};

/** The exact retained ticket, opening and rendered-owner tuple for one first-start refusal. The
 * arguments are the same originals the effect already held: no ref is read while calling it, and
 * `owner.isCurrent()` stays the last check. */
function ticketCurrent(
  ticket: Opening | undefined,
  opening: Readonly<{current: Opening | undefined}>,
  owner: Opening['owner'] | undefined,
  rendered: Readonly<{current: Opening['owner'] | undefined}>,
): boolean {
  return !!ticket && ticket.active && opening.current === ticket
    && ticket.owner === owner && rendered.current === ticket.owner && ticket.owner.isCurrent();
}

/** A first-start refusal has no admitted picture or player chrome. Touch presents the
 * original retained operation; it never creates a Play request from the current selection. */
export function TouchStartFailure({failure, wait, runtime}: {
  failure?: NativePlaybackStartFailure; wait?: NativePlaybackCapacityWait; runtime: NativeShellRuntime;
}) {
  const theme = useTheme(), t = runtime.text.t, {height} = useWindowDimensions();
  const owner = failure?.isCurrent() ? failure : wait?.isCurrent() ? wait : undefined;
  const rendered = useRef(owner); rendered.current = owner;
  const opening = useRef<Opening | undefined>(undefined);
  const consent = useRef<AbortController | undefined>(undefined);
  const offered = useRef<readonly RemotePlaybackControls[]>([]);
  const [sessions, setSessions] = useState<Readonly<{owner: Opening['owner']; items: readonly RemotePlaybackControls[]}>>();
  const rows = sessions && sessions.owner === owner ? sessions.items : [];
  const [error, setError] = useState<unknown>(), [busy, setBusy] = useState<string>();
  const [report, setReport] = useState(false), [sent, setSent] = useState(false);
  const [now, setNow] = useState(Date.now), began = useRef(Date.now());
  const current = (ticket = opening.current) => ticketCurrent(ticket, opening, owner, rendered);
  useLayoutEffect(() => {
    if (!owner) return;
    const ticket: Opening = {owner, active: true, action: 0}; opening.current = ticket;
    offered.current = [];setSessions(undefined);setError(undefined);setBusy(undefined);setReport(false);setSent(false);
    began.current = Date.now();setNow(Date.now());
    return () => {
      ticket.active = false;
      if (opening.current === ticket) opening.current = undefined;
      consent.current?.abort();
      // Admission/retry clears the port before its intentional new output. Cancel only a
      // still-live original wait; its own guard cannot cancel a successor operation.
      if (ticket.owner.isCurrent()) ticket.owner.cancel();
    };
  }, [owner]);
  useEffect(() => {
    const ticket = opening.current;
    if (!failure || failure !== owner || failure.scope !== 'account' || !ticketCurrent(ticket, opening, owner, rendered)) return;
    let alive = true;
    const apply = (value: readonly RemotePlaybackControls[]) => {
      if (!alive || !ticketCurrent(ticket, opening, owner, rendered)) return;
      offered.current = value;setSessions({owner: ticket!.owner, items: value});
    };
    const off = failure.subscribeSessions(apply);
    // remote() is the retained port's COMPLETE scoped read; no UI pagination, hint
    // interpretation or start loop. The port alone decides whether re-admission is allowed.
    void failure.remote().then(value => apply(value.items), reason => {
      if (alive && ticketCurrent(ticket, opening, owner, rendered)) setError(reason);
    });
    return () => {alive = false;off();};
  }, [failure, owner]);
  useEffect(() => {
    if (!owner || owner !== wait && (owner !== failure || failure?.scope !== 'account')) return;
    // Display only: the retained port owns the budget/deadline and the permission to retry.
    // Its exhaustion getter can change without an event; never poll GET/POST from this clock.
    const timer = setInterval(() => {if (owner.isCurrent()) setNow(Date.now());}, 250);
    return () => clearInterval(timer);
  }, [wait, failure, owner]);
  if (!owner || !owner.isCurrent() || theme.isTV) return null;
  const cancel = () => {const ticket = opening.current; if (current(ticket)) ticket!.owner.cancel();};
  const close = () => {
    if (!current()) return;
    if (report) {consent.current?.abort();setReport(false);if (opening.current) {opening.current.action++;opening.current.busy = undefined;}setBusy(undefined);}
    else cancel();
  };
  const run = (key: string, action: () => Promise<void> | void) => {
    const ticket = opening.current;
    if (!current(ticket) || ticket!.busy) return;
    const command = ++ticket!.action;
    const commandCurrent = () => current(ticket) && ticket!.action === command;
    ticket!.busy = key;setBusy(key);setError(undefined);
    void Promise.resolve().then(() => {if (commandCurrent()) return action();}).catch(reason => {
      if (commandCurrent() && !(reason instanceof Error && reason.name === 'AbortError')) setError(reason);
    }).finally(() => {if (commandCurrent()) {ticket!.busy = undefined;setBusy(undefined);}});
  };
  const stop = (row: RemotePlaybackControls) => {
    if (!failure || failure !== owner || failure.scope !== 'account' || !offered.current.includes(row)) return;
    run(row.sessionId, () => failure.stop(row));
  };
  const sendReport = () => {
    const source = runtime.reportPlaybackProblem;
    if (!source || !current()) return;
    const controller = new AbortController();consent.current?.abort();consent.current = controller;
    run('report', async () => {
      await source.call(runtime, {signal: controller.signal, current: () => current() && consent.current === controller && !controller.signal.aborted});
      if (current() && consent.current === controller && !controller.signal.aborted) {setSent(true);setReport(false);}
    });
  };
  const waiting = wait === owner ? wait : undefined, refused = failure === owner ? failure : undefined;
  const limit = refused?.scope === 'account' ? refused.streamLimit : undefined;
  const conversion = !!waiting || refused?.code === 'conversion_capacity';
  const exhausted = refused?.scope === 'account' && refused.automaticRecoveryExhausted;
  const retry = !!waiting || refused?.retryable === true && (refused.scope !== 'account' || exhausted);
  const title = report ? t('about.report.confirmTitle', {server: runtime.getSnapshot().serverName ?? ''})
    : conversion ? t('next.states.convertingBusy') : limit ? t('next.states.streamLimit', {count: limit.playing})
    : refused?.retryable ? t('next.states.playbackStopped') : t('next.states.cannotPlay');
  const body = report ? t('about.report.confirmBody') : waiting ? t('next.states.convertingBusyBody')
    : limit ? t('next.states.streamLimitBody', {count: limit.limit})
    : playbackUserMessage(refused?.scope === 'account' ? 'owner_account_cap' : refused?.scope === 'server' ? 'owner_server_cap' : refused?.error);
  const buttonStyle = {minWidth: 100, height: 46, paddingHorizontal: 20, borderRadius: 10};
  const seconds = waiting ? Math.max(0, Math.ceil((waiting.retryAt - now) / 1000)) : 0;
  return <BottomSheet open onOpenChange={open => {if (!open) close();}} title={title} description={body}
    testID="touch-start-failure" fitContent scrollable={false} contentStyle={{paddingHorizontal: 20}}
    titleTextStyle={{fontSize: 25 * theme.textScale, lineHeight: 27 * theme.textScale, letterSpacing: -0.75 * theme.textScale}}
    descriptionTextStyle={{fontSize: 14 * theme.textScale, lineHeight: 21 * theme.textScale}}
    headerContext={<View style={{paddingHorizontal: 20, marginBottom: 14}}><View style={{width: 44, height: 44, borderRadius: 22,
      backgroundColor: alpha(theme.colors.text, 0.08), alignItems: 'center', justifyContent: 'center'}}>
      <Icon name={conversion ? 'i-clock' : refused?.scope === 'account' || refused?.scope === 'server' ? 'i-monitor' : 'i-info'} size={22}
        color={conversion || refused?.scope === 'account' || refused?.scope === 'server' ? theme.colors.amber : theme.colors.text}/>
    </View></View>}
    footer={!report && refused?.scope === 'account' && !exhausted ? undefined : <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 8}}>
      {report ? <><Button testID="touch-start-report-cancel" variant="primary" style={buttonStyle} onPress={close}>{t('action.cancel')}</Button>
        <Button testID="touch-start-report-send" variant="glass" style={buttonStyle} disabled={!!busy} loading={busy === 'report'} onPress={sendReport}>{t('about.report.send')}</Button></>
        : <>{retry && <Button testID="touch-start-retry" variant="primary" style={buttonStyle} disabled={!!busy} loading={busy === 'retry'} onPress={() => {
          if (waiting) run('retry', () => waiting.retryNow());else if (refused) run('retry', () => refused.retry());
        }}>{t(waiting ? 'next.states.tryNow' : 'next.states.tryAgain')}</Button>}
        <Button testID="touch-start-back" variant={retry ? 'glass' : 'primary'} style={buttonStyle} onPress={cancel}>{t('next.states.back')}</Button>
        {!retry && refused?.scope !== 'account' && runtime.reportPlaybackProblem && <Button testID="touch-start-report" variant="glass" style={buttonStyle} icon="i-flag" onPress={() => {
          if (current() && !busy) {setReport(true);setError(undefined);}
        }}>{t('next.states.reportProblem')}</Button>}</>}
    </View>}>
    {waiting && !report && <View style={{flexDirection: 'row', alignItems: 'center', gap: 10, marginBottom: 16}}>
      <CountdownRing size={20} progress={Math.min(1, Math.max(0, 1 - (waiting.retryAt - now) / Math.max(1, waiting.retryAt - began.current)))}/>
      <Text style={{fontSize: 13.5 * theme.textScale, color: theme.colors.textSecondary, fontVariant: ['tabular-nums']}}>{t('next.states.retryIn', {seconds})}</Text>
    </View>}
    {!report && refused?.scope === 'account' && rows.length > 0 && <FlatList data={rows} keyExtractor={row => row.sessionId}
      style={{height: Math.min(rows.length * 64, Math.max(100, height * 0.4)), marginBottom: 14, borderRadius: 12,
        backgroundColor: alpha(theme.colors.raised, 0.85), borderWidth: 1, borderColor: alpha(theme.colors.text, 0.08)}}
      initialNumToRender={6} maxToRenderPerBatch={6} windowSize={5}
      renderItem={({item: row, index}) => <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, paddingVertical: 9, paddingLeft: 12, paddingRight: 9,
        borderTopWidth: index ? 1 : 0, borderColor: alpha(theme.colors.text, 0.06)}}>
        <Icon name="i-monitor" size={18} color={theme.colors.textTertiary}/>
        <View style={{flex: 1, minWidth: 0}}><Text numberOfLines={1} style={{fontSize: 13.5 * theme.textScale, fontFamily: theme.type.bodyStrong.fontFamily, color: theme.colors.text}}>{row.device.name}</Text>
          <Text numberOfLines={1} style={{fontSize: 12.5 * theme.textScale, marginTop: 1, color: theme.colors.textTertiary}}>{row.item?.title ?? t('remote.hiddenTitle')}</Text></View>
        <Button testID={`touch-start-stop-${row.sessionId}`} variant="glass" size="small" disabled={!!busy || !row.actions.includes('stop') || row.state === 'ended'}
          loading={busy === row.sessionId} accessibilityLabel={t('remote.stopOn', {device: row.device.name})} onPress={() => stop(row)}>{t('action.stop')}</Button>
      </View>}
    />}
    {sent && <Text style={{fontSize: 14 * theme.textScale, color: theme.colors.textSecondary, marginBottom: 16}}>{t('about.report.sent', {server: runtime.getSnapshot().serverName ?? ''})}</Text>}
    {error !== undefined && <Text accessibilityRole="alert" style={{fontSize: 14 * theme.textScale, color: theme.colors.record, marginBottom: 16}}>{playbackUserMessage(error)}</Text>}
  </BottomSheet>;
}
