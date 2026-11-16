import React, {useRef} from 'react';
import {View} from 'react-native';
import {tvAccountSpec} from '@portico/design';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import {Button, TextField} from '../../kit/controls';
import {FocusProvider, FocusRegion, useFocusRegistry} from '../../kit/focus';
import {useDestinationEntryFocus} from '../../screens/destinations/useEntryFocus';
import {useTVBusyEntryRevision} from './entry-revision';
import {TVAccountFields, TVAuthLayout} from './TVAuthLayout';

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
  return <TextField focusId={id} label={label} accessibilityLabel={label} value={value} onChangeText={onChangeText} editable={!busy}
    secureTextEntry={password === true} textContentType={password ? 'password' : 'username'}
    autoComplete={password ? 'current-password' : 'username'} importantForAutofill="yes"
    autoCapitalize="none" autoCorrect={false} placeholder={placeholder} underlineColorAndroid="transparent"
    returnKeyType={password ? 'done' : 'next'} submitBehavior="blurAndSubmit" onSubmitEditing={onSubmitEditing}/>;
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
    title={props.kind === 'portico' ? t('auth.welcomeHome') : props.serverName}
    eyebrow={kind === 'local' ? t('accounts.signIn.to') : undefined}
    body={t(kind === 'portico' ? 'auth.accountLead' : 'auth.direct.lead')}>
    <FocusRegion id={id} entry={initial.current} order={['username', 'password', 'submit', 'back']} enabled={!busy}>
      {props.error && <View accessibilityRole="alert" style={{marginBottom: 16}}>{props.error}</View>}
      <TVAccountFields>
        <CredentialField id="username" label={t(kind === 'portico' ? 'auth.usernameOrEmail' : 'auth.username')} value={username}
          onChangeText={props.onUsernameChange} placeholder={props.kind === 'portico' ? t(props.usernamePlaceholder) : undefined} busy={busy}
          onSubmitEditing={() => {registry.focus({regionId: id, childId: 'password'});}}/>
        <CredentialField id="password" label={t('auth.password')} value={password} onChangeText={props.onPasswordChange} password busy={busy}
          onSubmitEditing={() => {registry.focus({regionId: id, childId: 'submit'});}}/>
      </TVAccountFields>
      <View style={{flexDirection: 'row', gap: tvAccountSpec.actionGap}}>
        <Button focusId="submit" disabled={busy || !username || !password} loading={busy ? true : undefined} onPress={submit}>{t(kind === 'portico' ? 'action.continue' : 'auth.signIn')}</Button>
        <Button focusId="back" variant="glass" disabled={busy} onPress={props.onBack}>{t('action.back')}</Button>
      </View>
    </FocusRegion>
  </TVAuthLayout>;
}
