import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls';
import {FocusProvider, FocusRegion} from '../../../kit/focus';
import {BrowseFrame, SignInFrame} from '../../../kit/frames';
import {OfflineServerRows} from '../../../kit/shell/OfflineServerRows';
import {SettingsHelp} from '../../../kit/settings/SettingsParts';
import {useTheme} from '../../../kit/theme';
import {useDestinationEntryFocus} from '../../../screens/destinations/useEntryFocus';
import {useTVBusyEntryRevision} from '../../../tv/auth/entry-revision';
import {useOfflineServerOpening, type OfflineServerProps} from './useOfflineServerOpening';

const t = defaultI18n.t;

export function OfflineServersPage(props: OfflineServerProps) {
  const theme = useTheme(), actions = useOfflineServerOpening(props);
  if (theme.isTV) return <FocusProvider><TVOfflinePage {...props} actions={actions}/></FocusProvider>;
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

function TVOfflinePage({rows, busy, activeId, retryIn, retryProgress, actions}: OfflineServerProps & {
  actions: ReturnType<typeof useOfflineServerOpening>;
}) {
  const theme = useTheme(), first = rows[0], entry = first ? `retry:${first.id}` : undefined;
  const revision = useTVBusyEntryRevision(busy);
  useDestinationEntryFocus({visible: !busy, intent: 'enter', revision}, entry ? {regionId: 'accounts.offline.rows', childId: entry} : undefined);
  return <BrowseFrame testID="tv-offline-servers" header={{title: t('accounts.tvOffline.title')}}>
    <SettingsHelp>{t('accounts.session.portico')}</SettingsHelp>
    <SettingsHelp>{t('accounts.tvOffline.body')}</SettingsHelp>
    <FocusRegion id="accounts.offline.rows" entry={entry} enabled={!busy} style={{gap: theme.space.sm}}>
      <OfflineServerRows rows={rows} busy={busy} activeId={activeId} retry={actions.retry} retryIn={retryIn} retryProgress={retryProgress}/>
    </FocusRegion>
    <SettingsHelp>{t('accounts.tvOffline.help')}</SettingsHelp>
    <Button variant="text" disabled={busy} onPress={actions.refresh}>{t('accounts.servers.refresh')}</Button>
  </BrowseFrame>;
}
