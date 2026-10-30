import React, {useLayoutEffect, useRef, useState} from 'react';
import {Text, View} from 'react-native';
import {Button} from '../../kit/controls';
import {Dialog} from '../../kit/overlays';
import {SettingsRow} from '../../kit/shell';
import {StateView} from '../../kit/states';
import type {NativeSettingsPanelDeps} from './settings-panels';
import type {NativeShellRuntime} from '../../shell/runtime';
import {sendNativeDeviceReport} from './device-client-report';
import {useTheme} from '../../kit/theme';
import {shellType} from '../../kit/shell/visual';

export function DeviceReportRow(props: NativeSettingsPanelDeps & {runtime: NativeShellRuntime}) {
  const {context, lifetime, t, runtime} = props;
  const theme = useTheme();
  type Ticket = {context: typeof context; lifetime: typeof lifetime; controller: AbortController};
  const [sent, setSent] = useState(false), [ticket, setTicket] = useState<Ticket>();
  const [busy, setBusy] = useState(false), [error, setError] = useState<unknown>();
  const active = useRef<Ticket | undefined>(undefined), pending = useRef<Ticket | undefined>(undefined), cancel = useRef<View>(null);
  const owner = useRef({context, lifetime});
  const invalidate = () => {const held = active.current; active.current = undefined; held?.controller.abort();};
  if (owner.current.context !== context || owner.current.lifetime !== lifetime) {
    invalidate(); owner.current = {context, lifetime};
  }
  useLayoutEffect(() => () => {invalidate();}, [context, lifetime]);
  const current = (asked: Ticket | undefined): asked is Ticket => !!asked && active.current === asked && !asked.controller.signal.aborted
    && owner.current.context === asked.context && owner.current.lifetime === asked.lifetime && asked.lifetime.current();
  const close = () => {
    if (!current(ticket)) return;
    invalidate(); setTicket(undefined); setBusy(false); setError(undefined);
  };
  const send = () => {
    if (!current(ticket) || pending.current) return;
    const asked = ticket;
    pending.current = asked; setBusy(true); setError(undefined); setSent(false);
    void sendNativeDeviceReport(props, {signal: asked.controller.signal, current: () => current(asked)}).then(() => {
      if (!current(asked)) return;
      invalidate(); setTicket(undefined); setBusy(false); setSent(true);
    }, failure => {if (current(asked)) setError(failure);}).finally(() => {
      if (pending.current === asked) pending.current = undefined;
      if (current(asked)) setBusy(false);
    });
  };
  return <>
    <SettingsRow testID="settings-send-app-log" label={t('next.settingsSpec.account.device.groups.5.rows.5.label')}
      description={t('next.settingsSpec.account.device.groups.5.rows.5.help')} onPress={() => {lifetime.assert(); invalidate(); const next = {context, lifetime, controller: new AbortController()}; active.current = next; setTicket(next); setBusy(false); setError(undefined); setSent(false);}}/>
    {sent && <Text testID="settings-send-app-log-receipt" accessibilityLiveRegion="polite" style={[shellType(14, 400), {color: theme.colors.textSecondary, padding: 14}]}>{t('about.report.sent', {server: context.serverName})}</Text>}
    <Dialog testID="settings-send-app-log-confirm" open={current(ticket)} initialFocusRef={cancel} onOpenChange={next => {if (!next) close();}}
      title={t('about.report.confirmTitle', {server: context.serverName})} description={t('about.report.confirmBody')}
      footer={<View style={{flexDirection: 'row', gap: 16}}><Button ref={cancel} testID="settings-send-app-log-cancel" variant="glass" disabled={busy} onPress={close}>{t('action.cancel')}</Button>
        <Button testID="settings-send-app-log-submit" variant="glass" loading={busy} onPress={send}>{t('about.report.send')}</Button></View>}>
      {error !== undefined ? <StateView {...runtime.error(error, send)}/> : <View/>}
    </Dialog>
  </>;
}
