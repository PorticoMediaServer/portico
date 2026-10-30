import React, {useCallback, useEffect, useState} from 'react';
import {Linking, ScrollView, Text, View} from 'react-native';
import {ReadOnlySignInClient, type SignInSettings} from '../../../../../../packages/client-core/src/account-move';
import {serverSignInSettingsURL} from '../../../../../../packages/client-core/src/sign-in';
import QRCode from 'react-native-qrcode-svg';
import {Button} from '../../../kit/controls';
import {SettingsRow} from '../../../kit/shell';
import {ServerHeader} from '../../../kit/shell/ServerIdentity';
import {shellType} from '../../../kit/shell/visual';
import {StateView} from '../../../kit/states';
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
  const [answer, setAnswer] = useState<Readonly<{api: typeof api; value?: SignInSettings; error?: unknown}>>(), [revision, setRevision] = useState(0);
  const current = useCallback(() => lifetime.current() && context.account.capabilities.manageSignIn && context.account.account.role === 'owner'
    && context.account.account.primaryProfileId === authority.profileId, [lifetime, context, authority.profileId]);
  useEffect(() => {
    if (!current()) return;
    let active = true;
    const client = new ReadOnlySignInClient(api, {serverId: authority.serverId, isCurrent: () => active && current(), capabilities: () => context.account.capabilities});
    setAnswer(undefined);
    void client.read().then(value => {if (active && current()) setAnswer({api, value});}, error => {if (active && current()) setAnswer({api, error});});
    return () => {active = false; client.dispose();};
  }, [api, authority, context, lifetime, revision, current]);
  const value = answer?.api === api && current() ? answer.value : undefined;
  const error = answer?.api === api && current() ? answer.error : undefined;
  const refresh = () => {if (current()) setRevision(next => next + 1);};
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
      {value.move && <View style={{padding: 14, gap: 8, backgroundColor: theme.colors.raised, borderRadius: 14}}>
        <Text style={[shellType(15, 700), {color: theme.colors.text}]}>{value.move.stage === 'preview' ? t('accounts.settings.movePreview') : value.move.stage === 'complete' ? t('accounts.settings.moveComplete') : value.move.stage === 'cancelled' ? t('accounts.settings.moveCancelled') : t(value.move.to === 'local' ? 'accounts.settings.movingToLocal' : 'accounts.settings.movingToPortico')}</Text>
        <Text style={[shellType(14, 400, 20), {color: theme.colors.textSecondary}]}>{t('accounts.settings.waiting', {waiting: value.move.waiting, total: value.move.people})}</Text>
        {(value.move.stage === 'moving_to_local' || value.move.stage === 'moving_to_portico') && <>
          <Text style={[shellType(13, 400, 18), {color: theme.colors.textSecondary}]}>{t('accounts.settings.codesOnWeb')}</Text>
          <View testID="server-signin-progress" accessibilityRole="progressbar" accessibilityValue={{min: 0, max: value.move.people, now: value.move.people - value.move.waiting}}
            style={{height: 4, backgroundColor: theme.colors.lineSoft, borderRadius: 2}}>
            <View style={{height: 4, borderRadius: 2, backgroundColor: theme.colors.accentStrong, width: `${value.move.people ? (value.move.people - value.move.waiting) / value.move.people * 100 : 0}%`}}/>
          </View>
        </>}
      </View>}
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
