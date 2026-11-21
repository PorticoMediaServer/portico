import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls';
import {TVAccountRow, TVAccountRowColumn} from '../../../kit/controls/TVAccountRow';
import {FocusProvider, FocusRegion} from '../../../kit/focus';
import {TVAccountActionRow, TVAccountActions, TVAuthLayout} from '../../../kit/frames/TVAuthLayout';
import {AccountFields} from '../../../kit/accounts/AccountParts';
import {ServerGroup} from '../../../kit/accounts/ServerChooserParts';
import {StateView} from '../../../kit/states';
import {serverRowDetail, type ServerRow, type Snapshot} from '../../shared/accounts/useServerChooser';
import {useTVServerChooser} from '../../shared/accounts/useTVServerChooser';

/** Connect24. Presence is omitted until a real directory observation exists. */
export function TVServerChooserPage(props: ChooserProps) {
  return <FocusProvider><TVAuthLayout testID="tv-server-chooser" title={defaultI18n.t('web.servers.switchServer')} body={defaultI18n.t('web.chooser.lede')}>
    <TVAccountRowColumn><Chooser {...props}/></TVAccountRowColumn>
  </TVAuthLayout></FocusProvider>;
}
/** connect-24: a TV's Switch server Settings page draws the same chooser in Settings' page column, under the page's title
 * and line. Settings' list keeps focus when the page opens; Right enters the servers, Left from them returns to the list. */
export function TVServerSettingsPanel(props: ChooserProps) {
  return <TVAccountRowColumn><Chooser {...props} embedded/></TVAccountRowColumn>;
}
/** The Switch server Settings page's chooser region: Settings' rows enter through it (SettingsPreferences names it). */
const TV_SERVER_SETTINGS_REGION = 'accounts.chooser';
type ChooserProps = {
  snapshot?: Snapshot; busy: boolean; error?: React.ReactNode; owner: boolean; onBack?: () => void;
  onSelect: (row: ServerRow) => void; onSignOut: (row: ServerRow) => void; onForget: (row: ServerRow) => void;
  onRefresh: () => void; onAdd: () => void; certificates?: React.ReactNode; footer?: React.ReactNode;
};
/** Connect24 starts focus on the first server row; Down moves server to server, then Refresh
 * servers and Use a local server, then the footer's Link a TV. The two actions sit side by side as
 * drawn, so Down from Refresh is pointed at Use a local server (the caption's order); Up from
 * either returns to the server rows. A server's Sign out (only while its sign-in is live) and Forget sit right
 * of its row (Right reaches them, Left returns) and are not on the vertical path. Back is the remote key. A pending switch,
 * sign-out or refresh detaches every control, so the chooser asks for that start again when the
 * run ends. */
function Chooser({snapshot, busy, error, owner, onSelect, onSignOut, onForget, onRefresh, onAdd, certificates, footer, embedded = false}: ChooserProps & {embedded?: boolean}) {
  const t = defaultI18n.t;
  const {selected, groups, preferred, serverOrder, addHandle, bindAdd, refreshHandle, bindRefresh} = useTVServerChooser(snapshot, busy, embedded);
  const lastServer = serverOrder.at(-1);
  const row = (value: ServerRow, connected = false) => <TVAccountRow key={value.id} title={value.name} detail={serverRowDetail(value)} icon="i-server" active={connected}
    focusId={`server:${value.id}`} disabled={busy || connected} preferredFocus={!embedded && !connected && value.id === preferred}
    nextFocusDown={embedded && `server:${value.id}` === lastServer ? refreshHandle : undefined}
    accessibilityLabel={value.name} accessibilityState={{selected: connected}} onPress={() => onSelect(value)}
    badge={connected && owner ? t('accounts.settings.owner') : undefined}>
    {value.signedIn && <Button focusId={`signout:${value.id}`} variant="glass" size="small" disabled={busy} accessibilityLabel={t('accounts.signOut.server', {server: value.name})} onPress={() => onSignOut(value)}>{t('action.signOut')}</Button>}
    <Button focusId={`forget:${value.id}`} variant="glass" size="small" disabled={busy} accessibilityLabel={t('connection.identity.forget', {server: value.name})} onPress={() => onForget(value)}>{t('connection.identity.forgetAction')}</Button>
  </TVAccountRow>;
  const servers = <AccountFields>
    {selected && <ServerGroup title={t('servers.connectedTo')}>{row(selected, true)}</ServerGroup>}
    {groups.filter(group => group.rows.length).map(group => <ServerGroup key={group.id} title={group.title}>
      {group.rows.map(value => row(value))}
    </ServerGroup>)}
  </AccountFields>;
  const refresh = <Button ref={bindRefresh} focusId="refresh" variant="glass" preferredFocus={!embedded && !preferred} icon="i-refresh" disabled={busy}
    onPress={onRefresh} nextFocusDown={addHandle}>{t('accounts.servers.refresh')}</Button>;
  const add = <Button ref={bindAdd} focusId="add" variant="glass" disabled={busy} onPress={onAdd}>{t('accounts.signIn.local')}</Button>;
  if (embedded) {
    // In Settings every control is a stop of this one region, as This device's rows are: Right from the list enters the
    // first server (Refresh servers when there is none). Down runs server to server, then Refresh and Use a local server;
    // Sign out and Forget sit right of their row, and the certificates and Link a TV are below, reached by position.
    return <FocusRegion id={TV_SERVER_SETTINGS_REGION} entry={preferred ? `server:${preferred}` : 'refresh'}
      order={[...serverOrder, 'refresh', 'add']} orientation="vertical" enabled={!busy}>
      <AccountFields>
        {error}
        {!snapshot && <StateView variant="inline" title={t('status.loading')}/>}
        {servers}
        {certificates}
        <TVAccountActionRow>{refresh}{add}</TVAccountActionRow>
        {footer}
      </AccountFields>
    </FocusRegion>;
  }
  return <AccountFields>
    {error}
    {!snapshot && <StateView variant="inline" title={t('status.loading')}/>}
    <FocusRegion id="accounts.chooser.rows" entry={preferred ? `server:${preferred}` : undefined} order={serverOrder}
      orientation="vertical" edges={{down: 'accounts.chooser.actions'}} enabled={!busy}>
      {servers}
    </FocusRegion>
    {certificates}
    <TVAccountActions id="accounts.chooser.actions" entry="refresh" order={['refresh', 'add']}
      edges={serverOrder.length ? {up: 'accounts.chooser.rows'} : undefined} enabled={!busy}>
      {refresh}{add}
    </TVAccountActions>
    {footer}
  </AccountFields>;
}
