import React, {useRef} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import {Button, TextField} from '../../kit/controls';
import {FocusProvider, FocusRegion, useFocusRegistry} from '../../kit/focus';
import {nativeType, useTheme} from '../../kit/theme';
import {useDestinationEntryFocus} from '../../screens/destinations/useEntryFocus';
import {useTVBusyEntryRevision} from './entry-revision';
import {TVAuthLayout} from './TVAuthLayout';

export type TVCredentialFormProps = Readonly<{
  username: string; password: string;
  onUsernameChange: (value: string) => void; onPasswordChange: (value: string) => void;
  busy: boolean; error?: React.ReactNode; onSubmit: () => void; onBack: () => void;
}> & Readonly<
  {kind: 'portico'; serverName?: string; usernamePlaceholder: MessageId} |
  {kind: 'local'; serverName: string; usernamePlaceholder?: never}
>;

function CredentialField({id, label, value, onChangeText, password, placeholder, busy, onSubmitEditing}: {
  id: string; label: string; value: string; onChangeText: (value: string) => void; password?: boolean;
  placeholder?: string; busy: boolean; onSubmitEditing: () => void;
}) {
  const theme = useTheme();
  return <View style={{gap: 8, width: 860}}>
    <Text style={[nativeType({size: 22, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary}]}>{label}</Text>
    <TextField focusId={id} accessibilityLabel={label} value={value} onChangeText={onChangeText} editable={!busy}
      secureTextEntry={password === true} textContentType={password ? 'password' : 'username'}
      autoComplete={password ? 'current-password' : 'username'} importantForAutofill="yes"
      autoCapitalize="none" autoCorrect={false} placeholder={placeholder} placeholderTextColor={theme.colors.textTertiary}
      cursorColor={theme.colors.accentStrong} selectionColor={theme.colors.accentStrong} underlineColorAndroid="transparent"
      returnKeyType={password ? 'done' : 'next'} submitBehavior="blurAndSubmit" onSubmitEditing={onSubmitEditing}
      containerStyle={{width: 860}} fieldStyle={{height: 84, borderRadius: 16, backgroundColor: `${theme.colors.ink}9E`, borderWidth: 1.5}}
      style={[nativeType({size: 32, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale),
        {paddingHorizontal: 28, paddingVertical: 0, color: theme.colors.text}]}/>
  </View>;
}

/** Presentation only. The account owner keeps credentials, transactions, error strings and Back target. */
export function TVCredentialForm(props: TVCredentialFormProps) {
  return <FocusProvider><CredentialForm {...props}/></FocusProvider>;
}
function CredentialForm(props: TVCredentialFormProps) {
  const {kind, username, password, busy} = props, registry = useFocusRegistry(), id = `tv.auth.${kind}.credentials`;
  const initial = useRef(!username ? 'username' : !password ? 'password' : 'submit');
  const entryRevision = useTVBusyEntryRevision(busy);
  useDestinationEntryFocus({visible: !busy, intent: 'enter', revision: entryRevision}, {regionId: id, childId: initial.current});
  const t = defaultI18n.t, submit = () => {if (!busy && username && password) props.onSubmit();};
  return <TVAuthLayout testID={`tv-${kind}-credentials`} portico={kind === 'portico'}
    heading={props.kind === 'portico' ? t('auth.welcomeHome') : props.serverName}
    eyebrow={kind === 'local' ? t('accounts.signIn.to') : undefined}
    body={t(kind === 'portico' ? 'auth.accountLead' : 'auth.direct.lead')}>
    <FocusRegion id={id} entry={initial.current} order={['username', 'password', 'submit', 'back']} enabled={!busy}>
      {props.error && <View accessibilityRole="alert" style={{marginBottom: 16}}>{props.error}</View>}
      <View style={{width: 860, gap: 22}}>
        <CredentialField id="username" label={t(kind === 'portico' ? 'auth.usernameOrEmail' : 'auth.username')} value={username}
          onChangeText={props.onUsernameChange} placeholder={props.kind === 'portico' ? t(props.usernamePlaceholder) : undefined} busy={busy}
          onSubmitEditing={() => {registry.focus({regionId: id, childId: 'password'});}}/>
        <CredentialField id="password" label={t('auth.password')} value={password} onChangeText={props.onPasswordChange} password busy={busy}
          onSubmitEditing={() => {registry.focus({regionId: id, childId: 'submit'});}}/>
      </View>
      <View style={{flexDirection: 'row', gap: 20, marginTop: 32}}>
        <Button focusId="submit" disabled={busy || !username || !password} loading={busy ? true : undefined} onPress={submit}
          style={{height: 72, paddingHorizontal: 38, borderRadius: 14}}>{t(kind === 'portico' ? 'action.continue' : 'auth.signIn')}</Button>
        <Button focusId="back" variant="glass" disabled={busy} onPress={props.onBack} style={{height: 72, paddingHorizontal: 38, borderRadius: 14}}>{t('action.back')}</Button>
      </View>
    </FocusRegion>
  </TVAuthLayout>;
}
