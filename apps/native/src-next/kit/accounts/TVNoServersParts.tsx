import React, {type ReactNode} from 'react';
import {ScrollView, Text, View} from 'react-native';
import QRCode from 'react-native-qrcode-svg';
import {tvAccountSpec} from '@portico/design';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {NativeTVKeyboard} from '../../screens/search/TVKeyboard';
import {useDestinationEntryFocus} from '../../screens/destinations/useEntryFocus';
import type {TVNoServersProps, useTVNoServers} from '../../pages/shared/accounts/useTVNoServers';
import {Button, Focusable, TextField} from '../controls';
import {FocusRegion} from '../focus';
import {TVAccountActions, TVAccountFields} from '../frames/TVAuthLayout';
import {Icon} from '../Icon';
import {useTheme} from '../theme';

const {t} = defaultI18n;
type Form = ReturnType<typeof useTVNoServers>;
const addressKeys = ['abcdefghi', 'jklmnopqr', 'stuvwxyz.', '012345678', '9:/@-_'].map(row => [...row]);
const invitationKeys = ['ABCDEFGHI', 'JKLMNOPQR', 'STUVWXYZ-', '123456789'].map(row => [...row]);
const downloadUrl = 'https://getportico.tv/download/';

export function TVNoServersOverview({children, notice, actions}: {children: ReactNode; notice?: ReactNode; actions: ReactNode}) {
  const theme = useTheme();
  return <ScrollView contentContainerStyle={{paddingHorizontal: theme.layout.gutter.tvAuth, paddingTop: tvAccountSpec.wordmark.top + tvAccountSpec.wordmark.height + tvAccountSpec.ledeGap,
    paddingBottom: tvAccountSpec.footerBottom, gap: theme.space.lg}}>
    <View style={{gap: theme.space.sm}}>
      <Text style={[theme.type.accountEyebrow, {color: theme.colors.accentStrong}]}>{t('services.start.ready')}</Text>
      <Text accessibilityRole="header" style={[theme.type.accountTitle, {color: theme.colors.text}]}>{t('services.start.welcome')}</Text>
      <Text style={[theme.type.accountLede, {color: theme.colors.textSecondary, maxWidth: tvAccountSpec.column}]}>{t('services.start.body')}</Text>
    </View>
    {notice}
    {children}
    {actions}
  </ScrollView>;
}

export function DoorTiles({busy, onJoin}: {busy: boolean; onJoin: () => void}) {
  const theme = useTheme();
  useDestinationEntryFocus({visible: true, intent: 'enter', revision: 0}, {regionId: 'accounts.empty.door', childId: 'join'});
  const card = {flex: 1, minWidth: 0, padding: theme.space.xl, gap: theme.space.md,
    borderRadius: theme.baseRadii.panel, backgroundColor: theme.colors.raised,
    borderWidth: theme.space.hairline, borderColor: theme.colors.line} as const;
  const title = [theme.type.display, {color: theme.colors.text}];
  const body = [theme.type.accountLede, {color: theme.colors.textSecondary}];
  return <FocusRegion id="accounts.empty.door" entry="join" order={['join', 'setup']} orientation="horizontal"
    style={{flexDirection: 'row', gap: theme.space.lg, alignItems: 'stretch'}}>
    <Focusable focusId="join" focusKind="card" focusRadius={theme.baseRadii.panel} disabled={busy}
      focusableWhenDisabled onPress={onJoin} accessibilityRole="button" style={card}>
      <Icon name="i-link" size={tvAccountSpec.rowIcon.glyph} color={theme.colors.accentStrong}/>
      <Text style={title}>{t('services.start.join')}</Text>
      <Text style={body}>{t('services.start.joinBody')}</Text>
    </Focusable>
    <Focusable focusId="setup" focusKind="card" focusRadius={theme.baseRadii.panel} disabled={busy}
      focusableWhenDisabled accessibilityRole="button" accessibilityLabel={t('services.start.setup')} style={card}>
      <Icon name="i-server" size={tvAccountSpec.rowIcon.glyph} color={theme.colors.accentStrong}/>
      <Text style={title}>{t('services.start.setup')}</Text>
      <View style={{flexDirection: 'row', alignItems: 'flex-start', gap: theme.space.md}}>
        <Text style={[...body, {flex: 1}]}>{t('services.start.phoneSetupBody')}</Text>
        <View accessible accessibilityLabel={downloadUrl} style={{padding: theme.space.sm,
          borderRadius: theme.baseRadii.control, backgroundColor: theme.colors.white}}>
          <QRCode value={downloadUrl} size={188}/>
        </View>
      </View>
    </Focusable>
  </FocusRegion>;
}

export function TVNoServersNotice({notice, busy, current, children}: Pick<TVNoServersProps, 'notice' | 'busy'> & {current: () => boolean; children?: ReactNode}) {
  const theme = useTheme();
  return <>
    {notice && <View accessibilityRole="alert" style={{gap: theme.space.sm, padding: theme.space.md,
      borderRadius: theme.baseRadii.control, backgroundColor: theme.colors.raised}}>
      <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{notice.title}</Text>
      <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{notice.body}</Text>
      {notice.onRetry && <Button variant="text" disabled={busy} onPress={() => {if (current()) notice.onRetry?.();}}>{t('web.chooser.tryAgain')}</Button>}
    </View>}
    {children}
  </>;
}

export function TVJoinInvitationFields({form, busy, onFind, onSignOut}: Pick<TVNoServersProps, 'busy' | 'onFind' | 'onSignOut'> & {form: Form}) {
  const theme = useTheme(), addressField = form.field === 'address';
  return <FocusRegion id="accounts.empty.fields" entry="address" order={['address', 'invitation', 'join', 'find', 'sign-out']}>
    <TVAccountFields>
      <TextField focusId="address" label={t('accounts.signIn.address')} value={form.address} onFocus={form.focusAddress}
        showSoftInputOnFocus={false} editable={!busy} autoCapitalize="none" autoCorrect={false} onChangeText={form.changeAddress}/>
      <TextField focusId="invitation" label={t('services.start.invitation')} value={form.invitation} onFocus={form.focusInvitation}
        showSoftInputOnFocus={false} editable={!busy} autoCapitalize="none" autoCorrect={false} autoComplete="off" onChangeText={form.changeInvitation}/>
      <NativeTVKeyboard id={`accounts.empty.keyboard.${form.field}`} value={addressField ? form.address : form.invitation}
        rows={addressField ? addressKeys : invitationKeys} columns={9} width={tvAccountSpec.fieldsWidth} lead={addressField ? undefined : '0'} leftRegionId="accounts.empty.fields"
        activation={{visible: form.current() && !busy, intent: 'enter', revision: form.entryRevision * 2 + (addressField ? 0 : 1)}}
        onChangeText={addressField ? form.changeAddress : form.changeInvitation}/>
      <Button focusId="join" disabled={busy || !form.invitation.trim()} onPress={form.join}>{t('services.start.joinAction')}</Button>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.md}}>
        <Button focusId="find" variant="text" disabled={busy} onPress={() => {if (form.current()) onFind();}}>{t('connection.discovery.find')}</Button>
        <Button focusId="sign-out" variant="text" disabled={busy} onPress={() => {if (form.current()) onSignOut();}}>{t('accounts.signOut.portico')}</Button>
      </View>
    </TVAccountFields>
  </FocusRegion>;
}

export function TVJoinInvitationHelp() {
  const theme = useTheme();
  return <View style={{position: 'absolute', left: theme.layout.gutter.tvAuth + tvAccountSpec.column + theme.space.lg, right: theme.layout.gutter.tvAuth,
    top: tvAccountSpec.wordmark.top + tvAccountSpec.wordmark.height + tvAccountSpec.ledeGap, gap: theme.space.md}}>
    <Text accessibilityRole="header" style={[theme.type.display, {color: theme.colors.text}]}>{t('accounts.invitation.tvCodeHelpTitle')}</Text>
    <Text style={[theme.type.accountLede, {color: theme.colors.textSecondary}]}>{t('accounts.invitation.tvCodeHelpBody')}</Text>
    <Text style={[theme.type.accountLede, {color: theme.colors.textSecondary}]}>{t('accounts.invitation.tvCodeHelpPhone')}</Text>
  </View>;
}

export function TVNoServersActions({busy, current, onFind, onSignOut}: Pick<TVNoServersProps,'busy' | 'onFind' | 'onSignOut'> & {current: () => boolean}) {
  return <TVAccountActions id="accounts.empty.actions" order={['find', 'sign-out']}>
    <Button focusId="find" variant="text" disabled={busy} onPress={() => {if (current()) onFind();}}>{t('connection.discovery.find')}</Button>
    <Button focusId="sign-out" variant="text" disabled={busy} onPress={() => {if (current()) onSignOut();}}>{t('accounts.signOut.portico')}</Button>
  </TVAccountActions>;
}
