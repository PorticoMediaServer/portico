import React, {useCallback, useEffect, useRef, useState} from 'react';
import {Linking, ScrollView, Text, View} from 'react-native';
import {OwnerSignInSettingsClient, type OwnerSignInSettings} from '../../../../../../packages/client-core/src/account-move';
import {serverSignInSettingsURL} from '../../../../../../packages/client-core/src/sign-in';
import QRCode from 'react-native-qrcode-svg';
import {Button} from '../../../kit/controls';
import {SettingsRow} from '../../../kit/shell';
import {ServerHeader} from '../../../kit/shell/ServerIdentity';
import {shellType} from '../../../kit/shell/visual';
import {StateView} from '../../../kit/states';
import {BottomSheet} from '../../../kit/overlays';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {useTheme} from '../../../kit/theme';
import type {NativeSettingsLivePorts} from '../ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {useSettingsRequestScope} from '../use-settings-request-scope';
import {settingsPageHeading, settingsPageInset} from './page-layout';

/** Native's existing sign-in contract is deliberately read-only. Ownership and
 * sign-in moves continue in this server's authenticated web settings. */
export function NativeServerSignIn({ports, runtime, compactHeader = false, onOpenPage}: {
  ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; compactHeader?: boolean; onOpenPage: (page: 'people' | 'remote') => void;
}) {
  const theme = useTheme(), t = runtime.text.t, context = ports.context;
  const {api, lifetime, authority} = useSettingsRequestScope(context);
  const [answer, setAnswer] = useState<Readonly<{api: typeof api; value?: OwnerSignInSettings; error?: unknown}>>(), [revision, setRevision] = useState(0);
  const current = useCallback(() => lifetime.current() && context.account.capabilities.manageSignIn && context.account.account.role === 'owner'
    && context.account.account.primaryProfileId === authority.profileId, [lifetime, context, authority.profileId]);
  useEffect(() => {
    if (!current()) return;
    let active = true;
    const client = new OwnerSignInSettingsClient(api, {serverId: authority.serverId, isCurrent: () => active && current(), capabilities: () => context.account.capabilities});
    clients.current = client;
    setAnswer(undefined);
    void client.read().then(value => {if (active && current()) setAnswer({api, value});}, error => {if (active && current()) setAnswer({api, error});});
    return () => {active = false; if (clients.current === client) clients.current = undefined; client.dispose();};
  }, [api, authority, context, lifetime, revision, current]);
  const value = answer?.api === api && current() ? answer.value : undefined;
  const error = answer?.api === api && current() ? answer.error : undefined;
  const refresh = () => {if (current()) setRevision(next => next + 1);};
  // Finish now (frame 29): behind its confirmation, on the owner's own session; then the page reads again.
  const clients = useRef<OwnerSignInSettingsClient | undefined>(undefined);
  const [finishing, setFinishing] = useState(false), [finishBusy, setFinishBusy] = useState(false), [finishError, setFinishError] = useState<unknown>();
  const finish = async () => {
    const client = clients.current;
    if (!client || !current() || finishBusy) return;
    setFinishBusy(true); setFinishError(undefined);
    try {await client.finish(); if (current()) {setFinishing(false); refresh();}}
    catch (failure) {if (current()) setFinishError(failure);}
    finally {setFinishBusy(false);}
  };
  const openWeb = () => {
    if (!value || !current()) return;
    // Only the selected server's origin is used; no credential or move token is
    // added, and visiting a local server never opens Portico Services.
    void Linking.openURL(serverSignInSettingsURL(context.api.baseUrl)).catch(error => {if (current()) setAnswer({api, value, error});});
  };
  return <ScrollView testID="server-signin" contentContainerStyle={{...settingsPageInset(theme, compactHeader), gap: 14}}>
    <ServerHeader serverName={context.serverName} viewerRole="owner" audience="owner" variant="phone"/>
    {!compactHeader && <Text accessibilityRole="header" style={[settingsPageHeading(theme), {color: theme.colors.text}]}>{t('next.settingsSpec.server.signin.title')}</Text>}
    {error !== undefined && <StateView variant="inline" {...runtime.error(error, refresh, 'server-console')}/>}
    {!value && error === undefined && <Text style={[shellType(14, 400, 20), {color: theme.colors.textSecondary}]}>{t('next.settings.loading')}</Text>}
    {value && <>
      <View style={{backgroundColor: theme.colors.raised, borderRadius: 14, overflow: 'hidden'}}>
        <SettingsRow first label={t('accounts.settings.method')} value={t(value.signIn === 'local' ? 'accounts.settings.local' : 'accounts.settings.portico')}/>
        <SettingsRow label={t('accounts.settings.owner')} value={value.owner.username} description={value.owner.email}/>
      </View>
      {value.move && <View testID="server-signin-move" style={{backgroundColor: theme.colors.raised, borderRadius: 14, overflow: 'hidden'}}>
        {/* Frame 29: the direction, since when, how many still wait (of the total only when the server recorded one), Open People. */}
        <View style={{padding: 14, gap: 6}}>
          <Text style={[shellType(15, 700), {color: theme.colors.text}]}>{t(value.move.to === 'local' ? 'accounts.settings.movingToLocal' : 'accounts.settings.movingToPortico')}</Text>
          <Text style={[shellType(14, 600, 20), {color: theme.colors.text}]}>{value.move.total !== undefined ? t('accounts.settings.waiting', {waiting: value.move.waiting, total: value.move.total}) : t('accounts.settings.waitingCount', {count: value.move.waiting})}</Text>
          <Text style={[shellType(13, 400, 18), {color: theme.colors.textSecondary}]}>{t(value.move.to === 'local' ? 'accounts.settings.backInLocal' : 'accounts.settings.backInPortico')}</Text>
          <Text style={[shellType(13, 400, 18), {color: theme.colors.textTertiary}]}>{t('accounts.settings.movingSince', {date: defaultI18n.date(value.move.since)})}</Text>
          {value.move.total !== undefined && <View testID="server-signin-progress" accessibilityRole="progressbar" accessibilityValue={{min: 0, max: value.move.total, now: value.move.total - value.move.waiting}}
            style={{height: 4, backgroundColor: theme.colors.lineSoft, borderRadius: 2, marginTop: 4}}>
            <View style={{height: 4, borderRadius: 2, backgroundColor: theme.colors.accentStrong, width: `${Math.max(0, Math.min(1, (value.move.total - value.move.waiting) / value.move.total)) * 100}%`}}/>
          </View>}
          <Button size="compact" variant="glass" onPress={() => {if (current()) onOpenPage('people');}}>{t('accounts.settings.openPeople')}</Button>
        </View>
        {value.move.waiting > 0 && <SettingsRow label={t('accounts.settings.notMovedTitle')} description={t('accounts.settings.finishNowHelp')}
          control={<Button size="compact" variant="glass" onPress={() => {if (current()) {setFinishError(undefined); setFinishing(true);}}}>{t('accounts.settings.finishNow')}</Button>}/>}
      </View>}
      <BottomSheet open={finishing && !!value.move} onOpenChange={open => {if (!open && !finishBusy) setFinishing(false);}} fitContent testID="server-signin-finish"
        title={t('accounts.settings.finishNow')} description={value.move ? t('accounts.settings.finishNowConfirm', {count: value.move.waiting}) : undefined}
        footer={<View style={{gap: 10}}><Button loading={finishBusy} disabled={finishBusy} onPress={() => {void finish();}}>{t('accounts.settings.finishNow')}</Button><Button variant="glass" disabled={finishBusy} onPress={() => setFinishing(false)}>{t('next.settings.cancel')}</Button></View>}>
        {finishError !== undefined ? <StateView variant="inline" {...runtime.error(finishError, () => {void finish();}, 'server-console')}/> : null}
      </BottomSheet>
      <SettingsRow first label={t('settings.server.people')} onPress={() => {if (current()) onOpenPage('people');}}/>
      <SettingsRow first label={t('settings.server.remote')} onPress={() => {if (current()) onOpenPage('remote');}}/>
      {theme.isTV && <View testID="server-signin-web-qr" style={{alignItems: 'center', gap: 12}}>
        <QRCode value={serverSignInSettingsURL(context.api.baseUrl)} size={240}/>
        <Text style={[shellType(14, 400, 20), {color: theme.colors.textSecondary}]}>{t('settings.openOnWeb', {url: serverSignInSettingsURL(context.api.baseUrl)})}</Text>
      </View>}
      <Button testID="server-signin-open-web" variant="glass" onPress={openWeb}>{t('accounts.settings.changeOnWeb')}</Button>
      <Button size="compact" variant="text" icon="i-refresh" onPress={refresh}>{t('action.refresh')}</Button>
    </>}
  </ScrollView>;
}
