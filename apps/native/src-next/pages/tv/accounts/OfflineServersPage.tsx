import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls';
import {FocusProvider} from '../../../kit/focus';
import {BrowseFrame} from '../../../kit/frames';
import {OfflineServerRows} from '../../../kit/shell/OfflineServerRows';
import {SettingsHelp} from '../../../kit/settings/SettingsParts';
import {useDestinationEntryFocus} from '../../../screens/destinations/useEntryFocus';
import {useTVBusyEntryRevision} from '../../../tv/auth/entry-revision';
import {useOfflineServerOpening, type OfflineServerProps} from '../../shared/accounts/useOfflineServerOpening';

const t = defaultI18n.t;

export function TVOfflineServersPage(props: OfflineServerProps) {
  return <FocusProvider><OfflineBody {...props}/></FocusProvider>;
}

function OfflineBody(props: OfflineServerProps) {
  const {rows, busy, activeId, retryIn, retryProgress} = props, actions = useOfflineServerOpening(props);
  const first = rows[0], entry = first ? `retry:${first.id}` : undefined;
  const revision = useTVBusyEntryRevision(busy);
  useDestinationEntryFocus({visible: !busy, intent: 'enter', revision}, entry ? {regionId: 'accounts.offline.rows', childId: entry} : undefined);
  return <BrowseFrame testID="tv-offline-servers" header={{title: t('accounts.tvOffline.title')}}>
    <SettingsHelp>{t('accounts.session.portico')}</SettingsHelp>
    <SettingsHelp>{t('accounts.tvOffline.body')}</SettingsHelp>
    <OfflineServerRows rows={rows} busy={busy} activeId={activeId} retry={actions.retry} retryIn={retryIn} retryProgress={retryProgress}
      focus={{id: 'accounts.offline.rows', entry}}/>
    <SettingsHelp>{t('accounts.tvOffline.help')}</SettingsHelp>
    <Button variant="text" disabled={busy} onPress={actions.refresh}>{t('accounts.servers.refresh')}</Button>
  </BrowseFrame>;
}
