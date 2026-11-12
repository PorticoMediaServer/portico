import React, {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {StateLayout} from '../../kit/states';
import {Button} from '../../kit/controls';
import {ProfilePicker} from '../../kit/artwork';
import {useTheme} from '../../kit/theme';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {HttpHostedApi} from '../../../../../packages/client-core/src';
import type {PickerProfile} from '../../../../../packages/client-core/src/session-lifecycle';
import {BootWaitLine} from '../../shell/StartupBoot';
import {TVForgotPINHandoff, TVPickerRememberRow, TVProfilePINForm, TVWhosWatching} from '../../tv/auth';
import {nativeSessionLifecycle} from '../../platform/native-session-lifecycle';
import {NativeProfileRecovery, temporaryRecoveryApi} from '../../platform/profile-recovery';
import {nativeAccounts} from '../../platform/accounts';
import {hostedUrl} from '../../platform/account-authority';
import {nativeNetworkFetch} from '../../platform/server-connections';
import {PhonePINEntry} from './PhonePINEntry';
import {profilePinFailureText} from './profile-pin-failure';
import {PhoneProfileRecovery} from './PhoneProfileRecovery';
import {RememberProfileRow} from './RememberProfileRow';
import {registerBack} from '../../kit/navigation/back';

const t = defaultI18n.t;

/**
 * Who's watching, and a profile's PIN, as the session lifecycle asks for them: at launch when no
 * profile is remembered, and when a PIN changed elsewhere (profile_pin_reentry). The drawn frames
 * are AccountEntry's profile stages (auth.html 13–17). "Remember this profile on this device" is
 * offered under the faces and under the PIN, only where the server allows remembering (auth.html
 * section 8, 2 Nov). A tick on the picker remembers a profile without a PIN at once, and carries to a
 * PIN profile's page, where the server applies it only after a correct PIN: nothing is sent before.
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
  // A new PIN screen starts empty; the tick is the person's and carries from the picker to the PIN page and back.
  useEffect(() => {setCode(''); setForgot(false);}, [profileId]);
  // TV, notes/motion.md S.3, as the in-app picker does: a refused PIN stays in the red boxes for 400 ms, then they empty
  // for the next try (each refusal is a new lifecycle state). Only the refused code is emptied: a digit pressed in that
  // time has already started the next one. The phone's field keeps what is typed.
  const refused = theme.isTV && state.kind === 'enteringPin' && state.rejected ? state : undefined;
  const codeNow = useRef(code); codeNow.current = code;
  useEffect(() => {
    if (!refused) return;
    const refusedCode = codeNow.current;
    const timer = setTimeout(() => setCode(current => current === refusedCode ? '' : current), 400);
    return () => clearTimeout(timer);
  }, [refused]);
  const choosing = state.kind === 'choosingProfile' || state.kind === 'enteringPin';
  useEffect(() => {if (!choosing) setRemember(false);}, [choosing]);
  useEffect(() => () => recovery?.controller.dispose(), [recovery]);
  const back = () => {
    if (recovery) {recovery.controller.dispose(); setRecovery(undefined); return;}
    if (forgot) {setForgot(false); return;}
    // Leaving the PIN page forgets its refusal, so a PIN page opened again starts with no message.
    if (state.kind === 'enteringPin') {lifecycle.actions.submitPin.reset(); lifecycle.otherProfiles();}
    else if (state.kind === 'choosingProfile' && state.switching) lifecycle.cancelSwitch();
  };
  useEffect(() => {
    const subscription = registerBack(() => {back(); return state.kind === 'enteringPin' || !!recovery || forgot || state.kind === 'choosingProfile' && state.switching;}, {scope: 'surface'});
    return () => subscription();
  });
  if (state.kind !== 'choosingProfile' && state.kind !== 'enteringPin') return null;

  const connection = ports.connection(state.server.serverId);
  const art = (id: string) => connection?.account.profiles.find(value => value.id === id)?.art ?? 'blue';
  const option = (value: PickerProfile) => ({id: value.id, name: value.name, initial: [...value.name][0] ?? '', color: art(value.id),
    primary: value.primary, pinRequired: value.pinRequired, disabled: busy});
  const busy = pick.phase === 'working' || submit.phase === 'working';
  // Each screen says only its own failure: a refused PIN stays on the PIN page and doesn't follow Back to the picker,
  // and the picker only says why a pick failed.
  const failed = state.kind === 'enteringPin' ? submit.phase === 'failed' ? submit.failure.cause : undefined
    : pick.phase === 'failed' ? pick.failure.cause : undefined;
  const failure = failed ? presentError(failed, state.kind === 'enteringPin' ? 'profiles' : 'sign-in') : undefined;
  const status = failure && !failure.silent ? <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.danger, marginBottom: 16}]}>{profilePinFailureText(failed) ?? failure.body}</Text> : null;
  const wait = <BootWaitLine active={busy} serverName={state.server.name} testID="lifecycle-profile-wait"/>;

  if (state.kind === 'choosingProfile') {
    const picker = <ProfilePicker focusScope={`tv.auth.profiles.${state.server.serverId}`} focusBusy={busy} profiles={state.profiles.map(option)}
      preferredProfileId={state.focus} onSelect={id => lifecycle.actions.pickProfile.start(id, remember && state.canRemember)}
      {...(theme.isTV ? {title: t('accounts.profiles.onServer', {server: state.server.name})} : {showTitle: false})}/>;
    const tick = state.canRemember ? theme.isTV ? <TVPickerRememberRow checked={remember} onChange={setRemember} busy={busy}/>
      : <RememberProfileRow focusId="lifecycle-remember-profile" checked={remember} onChange={setRemember} disabled={busy} marginTop={34}/> : null;
    if (theme.isTV) return <TVWhosWatching testID="lifecycle-profiles">{picker}{tick}{status}{wait}</TVWhosWatching>;
    return <StateLayout testID="lifecycle-profiles" title={t('accounts.profiles.onServer', {server: state.server.name})}
      body={t('accounts.session.' + state.server.signIn as 'accounts.session.portico' | 'accounts.session.local')}
      beforeActions={<>{picker}{tick}{status}{wait}</>}
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
    serverAddress={connection.api.baseUrl} server={{id: state.server.serverId, signIn: state.server.signIn}} onBack={back}/>;
  // Under the boxes in a line of its own height (TVCodeErrorLine): no margin of its own, so nothing moves when it shows.
  const pinStatus = failure && !failure.silent ? <Text style={[theme.type.caption, {color: theme.colors.danger}]}>{profilePinFailureText(failed) ?? failure.body}</Text> : undefined;
  if (theme.isTV) return <TVProfilePINForm profile={option(profile)} code={code} onCodeChange={setCode} busy={busy} error={pinStatus}
    {...(state.canRemember ? {remember: {checked: remember, onChange: setRemember}} : {})}
    hints={busy ? wait : undefined} onSubmit={send} onBack={back} onForgotPIN={() => {if (!busy) {setCode(''); setForgot(true);}}}/>;
  // A profile is always named; only an account that couldn't be read falls back to the plain title.
  return <StateLayout testID="lifecycle-profile-pin" title={profile.name ? t('profile.pinTitle', {name: profile.name}) : t('auth.enterProfilePin')} avoidKeyboard
    beforeActions={<View style={{alignSelf: 'stretch'}}>
      <PhonePINEntry value={code} onChange={setCode} onComplete={send} busy={busy} error={!!state.rejected}/>
      {status}
      {state.canRemember && <RememberProfileRow focusId="lifecycle-remember-profile" checked={remember} onChange={setRemember} disabled={busy}/>}
      <View style={{alignItems: 'center', marginTop: 8}}><Button variant="text" disabled={busy || !connection} onPress={openRecovery}>{t('pin.forgot')}</Button></View>
      {wait}
    </View>}
    primaryAction={{id: 'profile-pin', label: t('action.continue'), disabled: busy || !/^\d{4}$/.test(code), loading: busy, onPress: () => send(code)}}
    secondaryAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: back}}/>;
}
