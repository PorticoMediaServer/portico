import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls';
import {SignInFrame} from '../../../kit/frames';
import {OfflineServerRows} from '../../../kit/shell/OfflineServerRows';
import {SettingsHelp} from '../../../kit/settings/SettingsParts';
import {useOfflineServerOpening, type OfflineServerProps} from '../../shared/accounts/useOfflineServerOpening';

const t = defaultI18n.t;

export function OfflineServersPage(props: OfflineServerProps) {
  const actions = useOfflineServerOpening(props);
  return <SignInFrame testID="phone-offline-servers" back={{label: t('action.back'), disabled: props.busy, onPress: actions.back}}
    kicker={t('accounts.session.portico')} title={t('accounts.tvOffline.title')} lede={t('accounts.tvOffline.body')}>
    {actions.settings && <Button variant="text" disabled={props.busy} onPress={actions.settings}>
      {props.username ? props.username + ' · ' + t('settings.title') : t('settings.title')}
    </Button>}
    <OfflineServerRows rows={props.rows} busy={props.busy} activeId={props.activeId} retry={actions.retry} retryIn={props.retryIn}/>
    <SettingsHelp>{t('accounts.tvOffline.help')}</SettingsHelp>
    {actions.downloads && <Button variant="glass" disabled={props.busy} onPress={actions.downloads}>{t('downloads.title')}</Button>}
    {actions.settings && <Button variant="glass" disabled={props.busy} onPress={actions.settings}>{t('settings.row.porticoAccount')}</Button>}
    {actions.join && <Button variant="glass" disabled={props.busy} onPress={actions.join}>{t('services.start.join')}</Button>}
    {actions.find && <Button variant="glass" disabled={props.busy} onPress={actions.find}>{t('connection.discovery.find')}</Button>}
    <Button variant="text" disabled={props.busy} onPress={actions.refresh}>{t('accounts.servers.refresh')}</Button>
  </SignInFrame>;
}
