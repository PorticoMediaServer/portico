import React from 'react';
import {Image, ScrollView, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {MoveCollisionDetails} from '../../../../../packages/client-core/src/account-move';
import {ColourBackdrop} from '../../kit/artwork';
import {Button} from '../../kit/controls';
import {Icon, type IconName} from '../../kit/Icon';
import {nativeType, useTheme} from '../../kit/theme';

/** These are confirmed controller outcomes, never a result inferred from a tap.
 * Credential entry, grant adoption and profile selection stay with their owners. */
export type PhoneAccountMoveNoticeState =
  | Readonly<{kind: 'linked'; onChooseProfile: () => void}>
  | Readonly<{kind: 'to-portico'; serverName: string; onContinue: () => void; onCreateAccount: () => void}>
  | Readonly<{kind: 'to-local'; serverName: string; onEnterCode: () => void; onChooseServer: () => void}>
  | Readonly<{kind: 'collision'; details: MoveCollisionDetails; onDifferentAccount: () => void; onBack: () => void}>
  /** The move's own confirm: the server account, the server and the Portico Account, each from a
   * trusted source (the server's answer, its verified system name, the held account). Only Move moves. */
  | Readonly<{kind: 'confirm'; serverName: string; username: string; account: string; onMove: () => void; onNotYou: () => void; onCancel: () => void}>;

/** Connect 41c, 42a/c, 44a and 45a. No credential or move proof enters this view. */
export function PhoneAccountMoveNotice({state, busy = false}: {state: PhoneAccountMoveNoticeState; busy?: boolean}) {
  const theme = useTheme(), insets = useSafeAreaInsets(), t = defaultI18n.t;
  const linked = state.kind === 'linked', collision = state.kind === 'collision', local = state.kind === 'to-local', confirm = state.kind === 'confirm';
  const color = linked ? theme.colors.healthy : collision ? theme.colors.danger : local || confirm ? theme.colors.accentStrong : theme.colors.amber;
  const icon: IconName = linked ? 'i-check' : collision ? 'i-userx' : local ? 'i-server' : 'i-person';
  const title = linked ? t('accounts.move.linked') : collision ? t('accounts.move.collision')
    : state.kind === 'confirm' ? t('accounts.move.porticoQuestion', {username: state.username, server: state.serverName, account: state.account})
    : t(local ? 'accounts.move.toLocal' : 'accounts.move.toPortico', {server: state.serverName});
  const body = linked ? t('accounts.move.linkedBody') : state.kind === 'confirm' ? t('accounts.move.porticoConfirm', {username: state.username, server: state.serverName, account: state.account}) : collision
    ? t('accounts.move.collisionBody', {server: state.details.serverName, username: state.details.existingUsername})
    : t(local ? 'accounts.move.askForCode' : 'accounts.move.linkBody');
  const action = state.kind === 'linked' ? {label: t('accounts.move.chooseProfile'), press: state.onChooseProfile}
    : state.kind === 'collision' ? {label: t('accounts.move.differentAccount'), press: state.onDifferentAccount}
    : state.kind === 'to-local' ? {label: t('accounts.move.haveCode'), press: state.onEnterCode}
    : state.kind === 'confirm' ? {label: t('accounts.move.porticoAction'), press: state.onMove}
    : {label: t('accounts.signIn.continuePortico'), press: state.onContinue};
  const secondary = state.kind === 'to-portico' ? {label: t('accounts.move.createAccount'), press: state.onCreateAccount}
    : state.kind === 'collision' ? {label: t('accounts.signIn.back'), press: state.onBack}
    : state.kind === 'confirm' ? {label: t('accounts.invitation.notYou'), press: state.onNotYou} : undefined;
  const safePadding = Math.max(112, insets.top + 42, insets.bottom + 64);
  return <View testID={`accounts-move-${state.kind}`} style={{flex: 1, backgroundColor: theme.colors.ink}}>
    <ColourBackdrop shape="top"/>
    <Image source={require('../../../assets/wordmark.png')} accessibilityLabel={t('accounts.brand')} resizeMode="contain"
      style={{position: 'absolute', left: 22, top: Math.max(70, insets.top), width: 17 * 512 / 117, height: 17}}/>
    <ScrollView contentContainerStyle={{flexGrow: 1, paddingHorizontal: 30, paddingVertical: safePadding}}>
      <View style={{flex: 1, justifyContent: 'center', alignItems: 'center'}}>
        <View style={{width: 76, height: 76, borderRadius: 38, backgroundColor: `${color}${local ? '24' : '1F'}`, alignItems: 'center', justifyContent: 'center', marginBottom: 22}}>
          <View pointerEvents="none" style={{position: 'absolute', inset: -10, borderRadius: 48, borderWidth: 10, borderColor: `${color}0D`}}/>
          <Icon name={icon} size={34} color={color}/>
        </View>
        <Text accessibilityRole="header" style={[nativeType({size: 26, lineHeight: 29.12, weight: 800, tracking: -.03}, theme.textScale),
          {color: theme.colors.text, textAlign: 'center', marginBottom: 10}]}>{title}</Text>
        <Text style={[nativeType({size: 15, lineHeight: 22.5, weight: 400, tracking: 0}, theme.textScale),
          {color: theme.colors.textSecondary, textAlign: 'center', marginBottom: 24}]}>{body}</Text>
        <Button disabled={busy} loading={busy || undefined} onPress={action.press}
          style={{alignSelf: 'stretch', height: 48, borderRadius: 11, marginBottom: 10}} textStyle={{fontSize: 16 * theme.textScale}}>{action.label}</Button>
        {secondary && <Button variant="link" disabled={busy} onPress={secondary.press} style={{alignSelf: 'stretch'}}>{secondary.label}</Button>}
      </View>
    </ScrollView>
    {state.kind === 'to-local' && <View style={{position: 'absolute', bottom: Math.max(44, insets.bottom + 16), left: 30, right: 30}}>
      <Button variant="link" disabled={busy} onPress={state.onChooseServer}>{t('accounts.signIn.anotherServer')}</Button>
    </View>}
    {state.kind === 'confirm' && <View style={{position: 'absolute', bottom: Math.max(44, insets.bottom + 16), left: 30, right: 30}}>
      <Button variant="link" disabled={busy} onPress={state.onCancel}>{t('action.cancel')}</Button>
    </View>}
  </View>;
}
