import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button, FormError, TextField} from '../../../kit/controls';
import {NoServersButtons, NoServersCard, NoServersFrame, NoServersNote} from '../../../kit/accounts/NoServersParts';
import {useNoServersJoin} from '../../shared/accounts/useNoServersJoin';

const alwaysCurrent = () => true;

/** The empty directory on phones and tablets: join with an invitation (typed or scanned), find a server on this network,
 * refresh, share the setup link, or sign out. Discovery and invitation entry still prove their server. */
export function NoServersPage({busy, error, onFind, onRefresh, onShare, onSignOut, onBack, onJoin, onSettings, onDownloads, username, isCurrent = alwaysCurrent, notice}: {
  busy: boolean; error?: React.ReactNode; notice?: Readonly<{title: string; body: string}>; isCurrent?: () => boolean; onSettings?: () => void; onDownloads?: () => void; username?: string; onFind: () => void; onRefresh: () => void;
  onJoin: (invitation: string, address: string) => void; onShare: () => void; onSignOut: () => void; onBack?: () => void;
}) {
  const t = defaultI18n.t, join = useNoServersJoin({busy, isCurrent, onJoin});
  return <NoServersFrame title={notice?.title ?? t('web.hostedAccount.noServers')} body={notice?.body ?? t('web.chooser.noneBody')} error={error}
    back={onBack && <Button variant="text" disabled={busy} onPress={onBack}>{t('action.back')}</Button>}>
    {onDownloads && <Button variant="glass" disabled={busy} onPress={onDownloads}>{t('downloads.title')}</Button>}
    {onSettings && <Button variant="text" disabled={busy} onPress={onSettings}>{username ? username + ' · ' + t('settings.title') : t('settings.title')}</Button>}
    <NoServersCard title={t('web.directJoin.title')}>
      <TextField label={t('accounts.signIn.address')} value={join.address} onChangeText={join.changeAddress} autoCapitalize="none" autoCorrect={false} autoComplete="off" editable={!busy && !join.addressLocked}/>
      <TextField label={t('services.start.invitation')} placeholder={t('web.directJoin.codePlaceholder')} value={join.invitation} onChangeText={join.changeInvitation} autoCapitalize="none" autoCorrect={false} autoComplete="off" editable={!busy}/>
      <NoServersNote>{t('web.directJoin.entryBody')}</NoServersNote>
      {join.canScan && <Button variant="glass" disabled={busy || join.scanning} loading={join.scanning} onPress={join.scan}>{t('accounts.move.scanQR')}</Button>}
      {join.scanFailed && <FormError placement="inline">{t('error.unknown')}</FormError>}
      <Button disabled={busy || !join.canJoin} onPress={join.join}>{t('action.continue')}</Button>
    </NoServersCard>
    <NoServersButtons>
      <Button variant="glass" disabled={busy} onPress={onFind}>{t('connection.discovery.find')}</Button>
      <Button variant="text" disabled={busy} onPress={onRefresh}>{t('accounts.servers.refresh')}</Button>
    </NoServersButtons>
    <NoServersCard title={t('services.start.setup')} body={t('services.start.phoneSetupBody')}>
      <Button variant="glass" disabled={busy} onPress={onShare}>{t('services.start.shareLink')}</Button>
    </NoServersCard>
    <Button variant="text" disabled={busy} onPress={onSignOut}>{t('accounts.signOut.portico')}</Button>
  </NoServersFrame>;
}
