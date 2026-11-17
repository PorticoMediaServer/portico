import React from 'react';
import {Text} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button, TextField} from '../../kit/controls';
import {SettingsGroup, SettingsRow, SettingsSectionLabel} from '../../kit/shell';
import {nativeType, useTheme} from '../../kit/theme';
import {PhoneAccountForm} from './PhoneAccountForm';

export type FoundServer = Readonly<{origin: string; name: string; host: string; setupRequired: boolean}>;

/** auth.html frame 5, Find your server: servers on this network, an address for anywhere else, and the way back to a
 * Portico Account. Rows are the shared settings rows; the form is the shared phone account form. */
export function PhoneFindServer({busy, error, address, found, searching, failed, localNetworkNote, onAddressChange, onContinue, onChoose,
  onSearchAgain, onPortico, onInvitation, onBack}: {
  busy: boolean; error?: React.ReactNode; address: string; found: readonly FoundServer[]; searching: boolean; failed: boolean;
  /** iPhone and iPad ask for Local Network access before they can look. */
  localNetworkNote: boolean;
  onAddressChange: (value: string) => void; onContinue: () => void; onChoose: (origin: string) => void; onSearchAgain: () => void;
  onPortico: () => void; onInvitation: () => void; onBack: () => void;
}) {
  const theme = useTheme(), t = defaultI18n.t;
  const section = (label: string, busy = false) => <SettingsSectionLabel label={label} busy={busy} inset={2}/>;
  const quiet = [nativeType({size: 12.5, lineHeight: 18.75, weight: 400, tracking: 0}, theme.textScale), {color: theme.colors.textTertiary}];
  return <PhoneAccountForm testID="accounts-find-server" title={t('accounts.find.title')} body={t('accounts.find.lede')}
    detailsAction={{id: 'back', label: t('action.back'), disabled: busy, onPress: onBack}}
    secondaryAction={{id: 'find-continue', label: t('action.continue'), disabled: busy || !address.trim(), loading: busy, onPress: onContinue}}
    beforeActions={<>
      {error}
      {section(t('playOn.onThisNetwork'), searching)}
      {found.length > 0 && <SettingsGroup testID="accounts-find-nearby">{found.map((server, index) => <SettingsRow key={server.origin} first={index === 0}
        testID={`accounts-find-${server.host}`} icon="i-server" label={server.name} description={server.setupRequired ? t('accounts.find.newServer') : server.host}
        navigates={!server.setupRequired} onPress={server.setupRequired || busy ? undefined : () => onChoose(server.origin)}
        control={server.setupRequired ? <Button variant="glass" size="small" disabled={busy} onPress={() => onChoose(server.origin)}>{t('accounts.find.setUp')}</Button> : undefined}/>)}
      </SettingsGroup>}
      {!searching && found.length === 0 && <Text style={quiet}>{t(failed ? 'connection.discovery.failed' : 'connection.discovery.empty')}</Text>}
      {!searching && <Button variant="link" size="compact" disabled={busy} style={{alignSelf: 'flex-start', marginTop: 4}} onPress={onSearchAgain}>{t('web.chooser.tryAgain')}</Button>}
      {section(t('accounts.find.elsewhere'))}
      <TextField appearance="auth" testID="accounts-find-address" accessibilityLabel={t('accounts.signIn.address')} value={address} onChangeText={onAddressChange}
        editable={!busy} placeholder="192.168.1.20:32500" autoCapitalize="none" autoCorrect={false} keyboardType="url" returnKeyType="go"
        onSubmitEditing={() => {if (!busy && address.trim()) onContinue();}} containerStyle={{marginBottom: 12}}/>
    </>}
    details={<>
      {section(t('accounts.find.withAccount'))}
      <SettingsGroup><SettingsRow first testID="accounts-find-portico" icon="i-user" label={t('accounts.find.everyServer')}
        description={t('accounts.find.signInOnce')} navigates onPress={busy ? undefined : onPortico}/></SettingsGroup>
      {localNetworkNote && <Text style={[quiet, {marginTop: 14}]}>{t('accounts.find.permission')}</Text>}
      <Button testID="accounts-find-invitation" variant="link" size="compact" disabled={busy} style={{alignSelf: 'flex-start', marginTop: 10}}
        onPress={onInvitation}>{t('services.start.join')}</Button>
    </>}/>;
}
