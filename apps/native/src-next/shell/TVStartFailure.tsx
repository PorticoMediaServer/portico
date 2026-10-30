import React, {useEffect, useLayoutEffect, useRef, useState} from 'react';
import {Modal, ScrollView, Text, View} from 'react-native';
import {playbackUserMessage} from '../../../../packages/client-core/src/playback-messages';
import type {RemotePlaybackControls} from '../../../../packages/client-core/src/playback-controls';
import type {NativePlaybackStartFailure} from '../platform/playback/start-failure';
import type {NativePlaybackCapacityWait} from '../platform/playback/capacity-retry';
import {Button} from '../kit/controls';
import {FocusRegion, useOptionalFocusRegistry} from '../kit/focus';
import {StateLayout} from '../kit/states';
import {useTheme} from '../kit/theme';
import type {NativeShellRuntime} from './runtime';

/** A refusal has no admitted output/frozen frame. All commands belong to the held
 * startup request; this view cannot look up or start a newer selected item. */
export function TVStartFailure({failure, wait, runtime}: {failure?: NativePlaybackStartFailure; wait?: NativePlaybackCapacityWait; runtime: NativeShellRuntime}) {
  const theme = useTheme(), t = runtime.text.t, registry = useOptionalFocusRegistry();
  const owner = failure ?? wait;
  const [rows, setRows] = useState<readonly RemotePlaybackControls[]>([]);
  const [error, setError] = useState<unknown>(), [busy, setBusy] = useState<string>();
  const [report, setReport] = useState(false), [sent, setSent] = useState(false);
  const [now, setNow] = useState(Date.now), began = useRef(Date.now());
  const consent = useRef<AbortController | undefined>(undefined);
  const mounted = useRef(false);
  const current = () => mounted.current && owner?.isCurrent() === true;
  useLayoutEffect(() => {
    mounted.current = true;const original = registry?.capture(), navigation = runtime.navigation, route = navigation?.getSnapshot();
    return () => {mounted.current = false;consent.current?.abort();queueMicrotask(() => {if (original && navigation?.getSnapshot() === route) registry?.restore(original);});};
  }, [registry, runtime]);
  useEffect(() => () => {consent.current?.abort();}, [owner]);
  useEffect(() => {
    setRows([]);setError(undefined);setBusy(undefined);setReport(false);setSent(false);
    if (!failure || failure.scope !== 'account') return;
    let alive = true;
    const off = failure.subscribeSessions(value => {if (alive && failure.isCurrent()) setRows(value);});
    void failure.remote().then(value => {if (alive && failure.isCurrent()) setRows(value.items);}, reason => {if (alive && failure.isCurrent()) setError(reason);});
    return () => {alive = false;off();};
  }, [failure]);
  useEffect(() => {
    if (!wait && failure?.scope !== 'account') return;
    began.current = Date.now();setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 250);return () => clearInterval(timer);
  }, [wait, failure]);
  if (!owner?.isCurrent()) return null;
  const cancel = () => {if (current()) owner.cancel();};
  const close = () => {if (report) {consent.current?.abort();setReport(false);setBusy(undefined);} else cancel();};
  const failed = (reason: unknown) => {if (current() && !(reason instanceof Error && reason.name === 'AbortError')) setError(reason);};
  const stop = (row: RemotePlaybackControls) => {
    if (!failure || !current() || busy) return;
    setBusy(row.sessionId);setError(undefined);
    void failure.stop(row).catch(failed).finally(() => {if (current()) setBusy(undefined);});
  };
  const send = () => {
    if (!current() || busy || !runtime.reportPlaybackProblem) return;
    const controller = new AbortController();consent.current?.abort();consent.current = controller;
    setBusy('report');setError(undefined);
    void runtime.reportPlaybackProblem({signal: controller.signal, current: () => current() && consent.current === controller && !controller.signal.aborted}).then(() => {
      if (current() && !controller.signal.aborted) {setSent(true);setReport(false);}
    }, failed).finally(() => {if (current() && consent.current === controller) setBusy(undefined);});
  };
  const conversion = failure?.code === 'conversion_capacity', limit = failure?.streamLimit;
  const title = report ? t('about.report.confirmTitle', {server: runtime.getSnapshot().serverName ?? ''})
    : wait || conversion ? t('next.states.convertingBusy')
    : limit ? t('next.states.streamLimit', {count: limit.playing})
    : failure?.retryable ? t('next.states.playbackStopped') : t('next.states.cannotPlay');
  const body = report ? t('about.report.confirmBody')
    : wait ? t('next.states.convertingBusyBody')
    : limit ? t('next.states.streamLimitBody', {count: limit.limit})
    : playbackUserMessage(failure?.scope === 'account' ? 'owner_account_cap' : failure?.scope === 'server' ? 'owner_server_cap' : failure?.error);
  const retryable = !!wait || failure?.retryable === true && (failure.scope !== 'account' || failure.automaticRecoveryExhausted);
  const back = {id: 'back', testID: 'native-start-back', label: t('next.states.back'), onPress: cancel, hasTVPreferredFocus: !retryable};
  const seconds = wait ? Math.max(0, Math.ceil((wait.retryAt - now) / 1000)) : 0;
  return <Modal visible transparent={false} animationType="none" onRequestClose={close} supportedOrientations={['landscape']}>
    <FocusRegion id="native-start-refusal" entry={report ? 'native-start-report-cancel' : retryable ? 'native-start-retry' : 'native-start-back'} entryPolicy="entry" hold style={{flex: 1}} accessibilityViewIsModal>
      <StateLayout variant="player" frozenPicture={<View style={{flex: 1, backgroundColor: theme.colors.ink}}/>}
        testID="native-start-refusal" title={title} body={body} icon={wait || conversion ? 'i-clock' : failure?.scope === 'account' || failure?.scope === 'server' ? 'i-monitor' : 'i-info'}
        tone={wait || conversion || failure?.scope === 'account' || failure?.scope === 'server' ? 'warning' : 'neutral'}
        retry={wait ? {progress: Math.min(1, Math.max(0, 1 - (wait.retryAt - now) / Math.max(1, wait.retryAt - began.current))), label: t('next.states.retryIn', {seconds})} : undefined}
        primaryAction={report ? {id: 'cancel-report', testID: 'native-start-report-cancel', label: t('action.cancel'), onPress: close, hasTVPreferredFocus: true}
          : wait ? {id: 'retry', testID: 'native-start-retry', hasTVPreferredFocus: true, label: t('next.states.tryNow'), onPress: () => {if (current()) wait.retryNow();}}
          : failure?.retryable && (failure.scope !== 'account' || failure.automaticRecoveryExhausted) ? {id: 'retry', testID: 'native-start-retry', hasTVPreferredFocus: true, label: t('next.states.tryAgain'), onPress: () => {if (current()) void failure.retry().catch(failed);}}
          : back}
        secondaryAction={report ? {id: 'send-report', label: t('about.report.send'), onPress: send, loading: busy === 'report'}
          : wait || failure?.retryable && (failure.scope !== 'account' || failure.automaticRecoveryExhausted) ? back
          : runtime.reportPlaybackProblem ? {id: 'report', label: t('next.states.reportProblem'), onPress: () => {if (current()) {setReport(true);setError(undefined);}}} : undefined}
        beforeActions={!report && failure?.scope === 'account' && rows.length ? <ScrollView style={{maxHeight: 300, width: '100%', marginBottom: 28}}>
          {rows.map(row => <View key={row.sessionId} style={{flexDirection: 'row', alignItems: 'center', gap: 24, paddingVertical: 12}}>
            <View style={{flex: 1}}><Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{row.device.name}</Text>
              <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{row.item?.title ?? t('remote.hiddenTitle')}</Text></View>
            <Button variant="glass" size="small" focusId={row.sessionId} testID={`native-start-stop-${row.sessionId}`} disabled={!!busy}
              loading={busy === row.sessionId} accessibilityLabel={t('remote.stopOn', {device: row.device.name})} onPress={() => stop(row)}>{t('action.stop')}</Button>
          </View>)}
        </ScrollView> : undefined}>
        {sent && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('about.report.sent', {server: runtime.getSnapshot().serverName ?? ''})}</Text>}
        {error !== undefined && <Text accessibilityLiveRegion="polite" style={[theme.type.body, {color: theme.colors.record}]}>{playbackUserMessage(error)}</Text>}
      </StateLayout>
    </FocusRegion>
  </Modal>;
}
