import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls';
import {Menu} from '../../../kit/overlays';
import {BrowseFrame} from '../../../kit/frames';
import {StateView} from '../../../kit/states';
import {AccountFields} from '../../../kit/accounts/AccountParts';
import {ServerChooserRow, ServerChooserScroll, ServerGroup} from '../../../kit/accounts/ServerChooserParts';
import {serverMenuItems, useServerChooser, type ServerChooserProps, type ServerRow} from '../../shared/accounts/useServerChooser';

export function ServerChooserPage({snapshot, busy, error, onBack, onSelect, onSignOut, onForget, onRefresh, onAdd, footer, certificates}: ServerChooserProps) {
  const t = defaultI18n.t;
  const {menu, setMenu, current, groups} = useServerChooser(snapshot);
  const row = (server: ServerRow, selected = false) => <ServerChooserRow key={server.id} server={server} selected={selected}
    busy={busy} onSelect={() => onSelect(server)} onMenu={() => setMenu(server)}/>;
  return <BrowseFrame testID="phone-server-chooser" header={{title: t('web.servers.switchServer'),
    back: onBack ? {label: t('next.shell.settings'), onPress: onBack, testID: 'phone-server-back', disabled: busy} : undefined}} grid={<>
    <ServerChooserScroll>
      {error}
      {!snapshot && <StateView variant="inline" title={t('status.loading')}/>}
      {current && <ServerGroup title={t('web.chooser.current')}>{row(current, true)}</ServerGroup>}
      {groups.filter(group => group.rows.length).map(group => <ServerGroup key={group.id} title={t(group.label)}>{group.rows.map(server => row(server))}</ServerGroup>)}
      <AccountFields>
        <Button testID="phone-server-refresh" variant="glass" size="compact" icon="i-refresh" disabled={busy} onPress={onRefresh}>{t('accounts.servers.refresh')}</Button>
        <Button testID="phone-server-add" variant="text" size="compact" disabled={busy} onPress={onAdd}>{t('accounts.signIn.local')}</Button>
      </AccountFields>
      {certificates}
      {footer}
    </ServerChooserScroll>
    <Menu open={!!menu} onOpenChange={open => {if (!open) setMenu(undefined);}} presentation="sheet" title={menu?.name}
      items={menu ? serverMenuItems(menu, {signOut: server => {setMenu(undefined); onSignOut(server);},
        forget: server => {setMenu(undefined); onForget(server);}}) : []}/>
  </>}/>;
}
