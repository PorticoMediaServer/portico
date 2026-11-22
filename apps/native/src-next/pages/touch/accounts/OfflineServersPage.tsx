import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls';
import {OfflineServerRow, OfflineServersCountdown, OfflineServersFrame, OfflineServersNote} from '../../../kit/accounts/OfflineServersParts';
import {useOfflineServers, type OfflineServersProps} from '../../shared/accounts/useOfflineServers';

/** Every server of the signed-in Portico Account not answering, on phones and tablets (a TV draws pages/tv/accounts). */
export function OfflineServersPage(props: OfflineServersProps) {
  const view = useOfflineServers(props), t = defaultI18n.t;
  return <OfflineServersFrame account={view.account} busy={view.busy}>
    {view.rows.map(row => <OfflineServerRow key={row.id} name={row.name} detail={row.detail} busy={view.busy} loading={row.loading} onRetry={row.retry}/>)}
    {view.countdown !== undefined && <OfflineServersCountdown>{view.countdown}</OfflineServersCountdown>}
    <OfflineServersNote>{t('accounts.tvOffline.help')}</OfflineServersNote>
    {view.ways.map(way => <Button key={way.id} variant="glass" disabled={view.busy} onPress={way.onPress}>{way.label}</Button>)}
    <Button variant="text" disabled={view.busy} onPress={view.refresh}>{t('accounts.servers.refresh')}</Button>
    <Button variant="text" disabled={view.busy} onPress={view.back}>{t('action.back')}</Button>
  </OfflineServersFrame>;
}
