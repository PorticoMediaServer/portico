import React, {useEffect} from 'react';
import {BackHandler, Text, View} from 'react-native';
import QRCode from 'react-native-qrcode-svg';
import {catalogue, defaultI18n, enUS, type MessageId} from '../../../../../packages/i18n/src';
import {Button} from '../../kit/controls';
import {FocusProvider, FocusRegion} from '../../kit/focus';
import {stateSentenceParts} from '../../kit/states/sentence-parts';
import {nativeType, useTheme} from '../../kit/theme';
import {BlurView, glassRecipe} from '../../platform/native-blur';
import {useDestinationEntryFocus} from '../../screens/destinations/useEntryFocus';
import {useTVBusyEntryRevision} from './entry-revision';
import {TVAuthLayout} from './TVAuthLayout';

/** Display-only public URL. The account controller supplies its verified origin;
 * credentials, grants and sign-in codes never enter this presenter. */
export function publicTVHandoffAddress(value: string): {url: string; display: string; pathname: string; search: string} {
  const url = new URL(value);
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.hash) throw new Error('native_tv_handoff_url_invalid');
  const keys = [...url.searchParams.keys()];
  const management = url.searchParams.get('scope') === 'server' && url.searchParams.get('section') === 'live';
  const profile = url.searchParams.get('scope') === 'account' && url.searchParams.get('section') === 'profile';
  if (keys.length && (url.pathname !== '/settings' || keys.length !== 2 || new Set(keys).size !== 2
    || !(profile || management))) throw new Error('native_tv_handoff_url_invalid');
  return {url: url.href, display: `${url.host}${url.pathname}${url.search}`, pathname: url.pathname, search: url.search};
}
function useHandoffBack(onBack: () => void, busy = false) {
  useEffect(() => {const listener = BackHandler.addEventListener('hardwareBackPress', () => {if (!busy) onBack(); return true;});
    return () => listener.remove();}, [onBack, busy]);
}

function handoffCodeCard(theme: ReturnType<typeof useTheme>, address: ReturnType<typeof publicTVHandoffAddress>, reference: MessageId, nativeBlur: boolean, testID: string) {
  return <View style={{position: 'absolute', right: 140, top: 0, bottom: 0, width: 720, justifyContent: 'center'}}>
    <View testID={testID} style={{paddingTop: 52, paddingHorizontal: 56, paddingBottom: 48, borderRadius: 28,
      borderWidth: 1.5, borderColor: `${theme.colors.text}1A`, backgroundColor: '#0d1b25C7', alignItems: 'center'}}>
      {nativeBlur && <View pointerEvents="none" style={{position: 'absolute', inset: 0, borderRadius: 28, overflow: 'hidden'}}>
        <BlurView style={{position: 'absolute', inset: 0}} blurType="dark" blurAmount={30} reducedTransparencyFallbackColor="#0d1b25"/>
      </View>}
      <View style={{width: 300, height: 300, padding: 20, borderRadius: 20, backgroundColor: theme.colors.white, marginBottom: 34}}><QRCode value={address.url} size={260}/></View>
      <Text style={[nativeType({size: 30, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textSecondary}]}>{address.display}</Text>
      <Text style={[nativeType({size: 21, weight: 400, tracking: 0, lineHeight: 30.45}, theme.textScale), {color: theme.colors.textTertiary, textAlign: 'center'}]}>{defaultI18n.t(reference)}</Text>
    </View>
  </View>;
}

/** The actual QR/link part from tv-more24, without invented device rows or a sign-in code. */
export function TVManagementHandoff({url, onBack}: {url: string; onBack: () => void}) {
  const theme = useTheme(), address = publicTVHandoffAddress(url);
  const sentence = stateSentenceParts(catalogue(defaultI18n.locale)['settings.openOnWeb'] ?? enUS['settings.openOnWeb'], {url: address.display}, ['url']);
  useHandoffBack(onBack);
  return <View testID="tv-management-handoff" onAccessibilityEscape={onBack} style={{flexDirection: 'row', alignItems: 'center', gap: 36,
    paddingVertical: 26, paddingHorizontal: 30, borderRadius: 18, backgroundColor: theme.colors.raised, borderWidth: 1, borderColor: `${theme.colors.line}B3`, marginBottom: 30}}>
    <View style={{width: 224, height: 224, padding: 12, borderRadius: 14, backgroundColor: theme.colors.white}}><QRCode value={address.url} size={200}/></View>
    <View style={{flex: 1}}><Text style={[nativeType({size: 28, weight: 700, tracking: 0, lineHeight: 36.4}, theme.textScale), {color: theme.colors.text, marginBottom: 14}]}>{defaultI18n.t('account.tvHandoffTitle')}</Text>
      <Text style={[nativeType({size: 22, weight: 400, tracking: 0, lineHeight: 33}, theme.textScale), {color: theme.colors.textSecondary}]}>{sentence.map((part, index) => <Text key={index}
        style={part.strong ? {color: theme.colors.text, fontWeight: '700'} : undefined}>{part.text}</Text>)}</Text>
    </View>
  </View>;
}

export type TVRequiredSetupHandoffProps = Readonly<{
  serverName: string; requirement: 'password' | 'two-step'; url: string; portico?: boolean; busy: boolean;
  /** Canonical §31 copy; the session owner chooses the password-first result. */
  copy: Readonly<{body: MessageId; reference: MessageId}>;
  onSignInAgain: () => void; onBack: () => void;
}>;

/** Public-only blocked state. Restricted-session discard stays with the account
 * controller; this view has no session, enrollment, password or polling port. */
export function TVRequiredSetupHandoff(props: TVRequiredSetupHandoffProps) {
  return <FocusProvider><RequiredSetupHandoff {...props}/></FocusProvider>;
}
function RequiredSetupHandoff(props: TVRequiredSetupHandoffProps) {
  const theme = useTheme(), t = defaultI18n.t, address = publicTVHandoffAddress(props.url), portico = props.portico === true, material = glassRecipe();
  if (address.pathname !== (portico ? '/account' : '/settings/profile') || address.search) throw new Error('native_tv_required_setup_url_invalid');
  const id = `tv.auth.required.${portico ? 'portico' : 'local'}.${props.requirement}`;
  useHandoffBack(props.onBack, props.busy);
  const entryRevision = useTVBusyEntryRevision(props.busy);
  useDestinationEntryFocus({visible: !props.busy, intent: 'enter', revision: entryRevision}, {regionId: id, childId: 'sign-in-again'});
  const aside = handoffCodeCard(theme, address, props.copy.reference, material.nativeBlur, 'tv-required-setup-card');
  const name = portico ? t('account.portico') : props.serverName;
  return <TVAuthLayout testID="tv-required-setup" portico={portico} eyebrow={name} aside={aside}
    heading={t(props.requirement === 'password' ? 'web.direct.newPasswordTitle' : 'web.twoStep.turnOnTitle')}
    body={t(props.copy.body, {server: name})}>
    <FocusRegion id={id} entry="sign-in-again" order={['sign-in-again', 'back']} enabled={!props.busy} style={{flexDirection: 'row', gap: 20}}>
      <Button focusId="sign-in-again" disabled={props.busy} onPress={() => {if (!props.busy) props.onSignInAgain();}} style={{height: 72, paddingHorizontal: 38, borderRadius: 14}}>{t('web.hostedAccount.signInAgain')}</Button>
      <Button focusId="back" variant="glass" disabled={props.busy} onPress={() => {if (!props.busy) props.onBack();}} style={{height: 72, paddingHorizontal: 38, borderRadius: 14}}>{t('action.back')}</Button>
    </FocusRegion>
  </TVAuthLayout>;
}

export type TVForgotPINHandoffProps = Readonly<{
  profileName: string; url: string; onBack: () => void;
  /** The exact §34 sentence is separate from the management card's heading. */
  body: MessageId; reference?: MessageId;
}>;

/** §34 keeps its owner at the same account/picker level. There are no sign-in,
 * profile selection, sign-out, disposal or polling ports on this presenter. */
export function TVForgotPINHandoff(props: TVForgotPINHandoffProps) {
  return <FocusProvider><ForgotPINHandoff {...props}/></FocusProvider>;
}
function ForgotPINHandoff(props: TVForgotPINHandoffProps) {
  const theme = useTheme(), material = glassRecipe(), address = publicTVHandoffAddress(props.url), id = 'tv.auth.profile.forgot-pin';
  if (address.pathname !== '/settings/profile' || address.search) throw new Error('native_tv_forgot_pin_url_invalid');
  useHandoffBack(props.onBack);
  useDestinationEntryFocus({visible: true, intent: 'enter', revision: 0}, {regionId: id, childId: 'back'});
  return <TVAuthLayout testID="tv-forgot-pin-handoff" portico={false} eyebrow={props.profileName}
    heading={defaultI18n.t('pin.forgot')} body={defaultI18n.t(props.body)}
    aside={handoffCodeCard(theme, address, props.reference ?? 'next.auth.tv.handoffHint', material.nativeBlur, 'tv-forgot-pin-card')}>
    <FocusRegion id={id} entry="back" order={['back']} style={{flexDirection: 'row'}}>
      <Button focusId="back" onPress={props.onBack} style={{height: 72, paddingHorizontal: 38, borderRadius: 14}}>{defaultI18n.t('action.back')}</Button>
    </FocusRegion>
  </TVAuthLayout>;
}
