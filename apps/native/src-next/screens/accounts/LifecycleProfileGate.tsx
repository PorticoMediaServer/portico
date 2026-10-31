import React, {useEffect, useState, useSyncExternalStore} from 'react';
import {BackHandler, Text, View} from 'react-native';
import {StateLayout} from '../../kit/states';
import {Button, Focusable} from '../../kit/controls';
import {ProfilePicker} from '../../kit/artwork';
import {Icon} from '../../kit/Icon';
import {useTheme} from '../../kit/theme';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {HttpHostedApi} from '../../../../../packages/client-core/src';
import {routeOrigin} from '../../../../../packages/client-core/src/route-identity';
import type {PickerProfile} from '../../../../../packages/client-core/src/session-lifecycle';
import {BootWaitLine} from '../../shell/StartupBoot';
import {TVForgotPINHandoff, TVProfilePINForm, TVWhosWatching} from '../../tv/auth';
import {nativeSessionLifecycle} from '../../platform/native-session-lifecycle';
import {NativeProfileRecovery, temporaryRecoveryApi} from '../../platform/profile-recovery';
import {nativeAccounts} from '../../platform/accounts';
import {hostedUrl} from '../../platform/account-authority';
import {nativeNetworkFetch} from '../../platform/server-connections';
import {PhonePINEntry} from './PhonePINEntry';
import {PhoneProfileRecovery} from './PhoneProfileRecovery';

const t = defaultI18n.t;

/**
 * Who's watching, and a profile's PIN, as the session lifecycle asks for them: at launch when no
 * profile is remembered, and when a PIN changed elsewhere (profile_pin_reentry). The drawn frames
 * are AccountEntry's profile stages (auth.html 13–17); "Remember this profile on this device" is
 * offered under the PIN when the server allows remembering (auth.html, 31 Oct).
 */
export function LifecycleProfileGate() {
  const theme = useTheme();
  const {lifecycle, ports} = nativeSessionLifecycle();
  const snapshot = useSyncExternalStore(lifecycle.subscribe, lifecycle.getSnapshot, lifecycle.getSnapshot);
  const pick = useSyncExternalStore(lifecycle.actions.pickProfile.subscribe, lifecycle.actions.pickProfile.getSnapshot, lifecycle.actions.pickProfile.getSnapshot);
  const submit = useSyncExternalStore(lifecycle.actions.submitPin.subscribe, lifecycle.actions.submitPin.getSnapshot, lifecycle.actions.submitPin.getSnapshot);
  const [code, setCode] = useState(''), [remember, setRemember] = useState(false), [forgot, setForgot] = useState(false);
  const [recovery, setRecovery] = useState<{controller: NativeProfileRecovery; id: string; name: string}>();
  const state = snapshot.state;
  const profileId = state.kind === 'enteringPin' ? state.profile.id : undefined;
  // A new PIN screen starts empty and unticked, as drawn.
  useEffect(() => {setCode(''); setRemember(false); setForgot(false);}, [profileId]);
  useEffect(() => () => recovery?.controller.dispose(), [recovery]);
  const back = () => {
    if (recovery) {recovery.controller.dispose(); setRecovery(undefined); return;}
    if (forgot) {setForgot(false); return;}
    if (state.kind === 'enteringPin') lifecycle.otherProfiles();
    else if (state.kind === 'choosingProfile' && state.switching) lifecycle.cancelSwitch();
  };
  useEffect(() => {
    const subscription = BackHandler.addEventListener('hardwareBackPress', () => {back(); return state.kind === 'enteringPin' || !!recovery || forgot || state.kind === 'choosingProfile' && state.switching;});
    return () => subscription.remove();
  });
  if (state.kind !== 'choosingProfile' && state.kind !== 'enteringPin') return null;

  const connection = ports.connection(state.server.serverId);
  const art = (id: string) => connection?.account.profiles.find(value => value.id === id)?.art ?? 'blue';
  const option = (value: PickerProfile) => ({id: value.id, name: value.name, initial: [...value.name][0] ?? '', color: art(value.id),
    primary: value.primary, pinRequired: value.pinRequired, disabled: busy});
  const busy = pick.phase === 'working' || submit.phase === 'working';
  const failed = submit.phase === 'failed' ? submit.failure.cause : pick.phase === 'failed' ? pick.failure.cause : undefined;
  const failure = failed ? presentError(failed, state.kind === 'enteringPin' ? 'profiles' : 'sign-in') : undefined;
  const status = failure && !failure.silent ? <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.danger, marginBottom: 16}]}>{failure.body}</Text> : null;
  const wait = <BootWaitLine active={busy} serverName={state.server.name} testID="lifecycle-profile-wait"/>;

  if (state.kind === 'choosingProfile') {
    const picker = <ProfilePicker focusScope={`tv.auth.profiles.${state.server.serverId}`} focusBusy={busy} profiles={state.profiles.map(option)}
      preferredProfileId={state.focus} onSelect={id => lifecycle.actions.pickProfile.start(id)}
      {...(theme.isTV ? {title: t('accounts.profiles.onServer', {server: state.server.name})} : {showTitle: false})}/>;
    if (theme.isTV) return <TVWhosWatching testID="lifecycle-profiles">{picker}{status}{wait}</TVWhosWatching>;
    return <StateLayout testID="lifecycle-profiles" title={t('accounts.profiles.onServer', {server: state.server.name})}
      body={t('accounts.session.' + state.server.signIn as 'accounts.session.portico' | 'accounts.session.local')}
      beforeActions={<>{picker}{status}{wait}</>}
      secondaryAction={state.switching ? {id: 'back', label: t('action.back'), disabled: busy, onPress: back} : undefined}/>;
  }

  const profile = state.profile;
  const send = (value: string) => {if (/^\d{4}$/.test(value) && !busy) lifecycle.actions.submitPin.start(value, remember && state.canRemember);};
  const openRecovery = () => {
    if (!connection || busy) return;
    const session = connection.session, pin = connection.api.getRouteConnection()?.pin;
    if (!pin) return;
    const authorityCurrent = () => {
      const live = connection.api.getRouteConnection()?.pin, now = lifecycle.getSnapshot().state;
      return (now.kind === 'enteringPin' || now.kind === 'choosingProfile') && now.viewer.familyId === session.sessionFamilyId
        && !!live && live.serverId === pin.serverId && live.publicKey === pin.publicKey && live.fingerprint === pin.fingerprint;
    };
    setRecovery({id: profile.id, name: profile.name, controller: new NativeProfileRecovery({api: connection.api, snapshot: connection.account, profileId: profile.id,
      selectedFamilyId: session.sessionFamilyId, kind: state.server.signIn, authorityCurrent,
      temporary: grant => temporaryRecoveryApi(connection.api, grant),
      portico: async () => {
        const credentials = nativeAccounts.accountCredentials(); await credentials.synchronize();
        const account = await credentials.accessSession();
        if (!account) throw new Error('Sign in to your Portico Account first.');
        return {hosted: new HttpHostedApi(hostedUrl, account.accessToken, nativeNetworkFetch), origin: hostedUrl,
          accountCurrent: () => {const live = credentials.getSnapshot().session; return !!live && live.account.id === account.account.id && live.familyId === account.familyId;}};
      }})});
  };
  if (recovery && connection) return <PhoneProfileRecovery controller={recovery.controller} profileName={recovery.name} username={connection.account.account.username}
    onOpen={value => {recovery.controller.dispose(); setRecovery(undefined); setCode(value); send(value);}}
    onClose={() => {recovery.controller.dispose(); setRecovery(undefined); setCode('');}}/>;
  if (theme.isTV && forgot && connection) return <TVForgotPINHandoff profileName={profile.name}
    url={new URL('/settings/profile', routeOrigin(connection.api.baseUrl)).href} body="next.auth.tv.forgotPINBody" onBack={back}/>;
  if (theme.isTV) return <TVProfilePINForm profile={option(profile)} code={code} onCodeChange={setCode} busy={busy} error={status}
    {...(state.canRemember ? {remember: {checked: remember, onChange: setRemember}} : {})}
    hints={busy ? wait : undefined} onSubmit={send} onBack={back} onForgotPIN={() => {if (!busy) {setCode(''); setForgot(true);}}}/>;
  return <StateLayout testID="lifecycle-profile-pin" title={t('profile.pinTitle', {name: profile.name})} avoidKeyboard
    beforeActions={<View style={{alignSelf: 'stretch'}}>
      <PhonePINEntry value={code} onChange={setCode} onComplete={send} busy={busy} error={!!state.rejected}/>
      {status}
      {state.canRemember && <Focusable focusId="lifecycle-remember-profile" accessibilityRole="checkbox" accessibilityState={{checked: remember}} accessibilityLabel={t('pin.rememberOnDevice')}
        onPress={() => setRemember(value => !value)} focusRadius={6} focusTreatment="none" style={{flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: 12, paddingVertical: 8, marginTop: 10}}>
        <View style={{width: 24, height: 24, borderRadius: 7, borderWidth: 1.5, alignItems: 'center', justifyContent: 'center',
          borderColor: remember ? theme.colors.accentStrong : theme.colors.lineStrong, backgroundColor: remember ? theme.colors.accentStrong : 'transparent'}}>
          {remember && <Icon name="i-check" size={16} color={theme.colors.ink}/>}
        </View>
        <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('pin.rememberOnDevice')}</Text>
      </Focusable>}
      <View style={{alignItems: 'center', marginTop: 8}}><Button variant="text" disabled={busy || !connection} onPress={openRecovery}>{t('pin.forgot')}</Button></View>
      {wait}
    </View>}
    primaryAction={{id: 'profile-pin', label: t('action.continue'), disabled: busy || !/^\d{4}$/.test(code), loading: busy, onPress: () => send(code)}}
    secondaryAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: back}}/>;
}
