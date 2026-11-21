import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {AccountDivider, AccountFields, AccountHint, AccountLinks} from '../../../kit/accounts/AccountParts';
import {Button, FormError, TextField} from '../../../kit/controls';
import {SignInFrame} from '../../../kit/frames';
import {useNoServersInvitation, type PhoneNoServersProps} from '../../shared/accounts/useNoServersInvitation';

const {t} = defaultI18n;

export function PhoneNoServers(props: PhoneNoServersProps) {
  const {busy, error, notice, username, onBack, onDownloads, onSettings, onFind, onRefresh, onShare, onSignOut} = props;
  const form = useNoServersInvitation(props);
  return <SignInFrame testID="phone-no-servers" back={onBack ? {label: t('action.back'), disabled: busy, onPress: onBack} : undefined}
    title={notice?.title ?? t('web.hostedAccount.noServers')} lede={notice?.body ?? t('web.chooser.noneBody')}>
    {error}
    {onDownloads && <Button variant="glass" disabled={busy} onPress={onDownloads}>{t('downloads.title')}</Button>}
    {onSettings && <Button variant="text" disabled={busy} onPress={onSettings}>{username ? username + ' · ' + t('settings.title') : t('settings.title')}</Button>}
    <AccountFields>
      <AccountDivider>{t('web.directJoin.title')}</AccountDivider>
      <TextField label={t('accounts.signIn.address')} value={form.address} onChangeText={form.changeAddress}
        autoCapitalize="none" autoCorrect={false} autoComplete="off" editable={!busy && !form.fromLink}/>
      <TextField label={t('services.start.invitation')} placeholder={t('web.directJoin.codePlaceholder')} value={form.invitation}
        onChangeText={form.changeInvitation} autoCapitalize="none" autoCorrect={false} autoComplete="off" editable={!busy}/>
      <AccountHint>{t('web.directJoin.entryBody')}</AccountHint>
      {form.scannerAvailable && <Button variant="glass" disabled={busy || form.scanning} loading={form.scanning} onPress={form.scan}>{t('accounts.move.scanQR')}</Button>}
      {form.scanFailed && <FormError placement="inline">{t('error.unknown')}</FormError>}
      <Button disabled={busy || !form.invitation.trim()} onPress={form.join}>{t('action.continue')}</Button>
    </AccountFields>
    <AccountLinks>
      <Button variant="glass" disabled={busy} onPress={onFind}>{t('connection.discovery.find')}</Button>
      <Button variant="text" disabled={busy} onPress={onRefresh}>{t('accounts.servers.refresh')}</Button>
    </AccountLinks>
    <AccountFields>
      <AccountDivider>{t('services.start.setup')}</AccountDivider>
      <AccountHint>{t('services.start.phoneSetupBody')}</AccountHint>
      <Button variant="glass" disabled={busy} onPress={onShare}>{t('services.start.shareLink')}</Button>
    </AccountFields>
    <Button variant="text" disabled={busy} onPress={onSignOut}>{t('accounts.signOut.portico')}</Button>
  </SignInFrame>;
}
