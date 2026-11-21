import React from 'react';
import type {View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls';
import {TVAccountRow, TVAccountRowColumn} from '../../../kit/controls/TVAccountRow';
import {FocusProvider, FocusRegion} from '../../../kit/focus';
import {TVAccountActionRow, TVAccountActions, TVAuthLayout} from '../../../kit/frames/TVAuthLayout';
import {AccountFields} from '../../../kit/accounts/AccountParts';
import {ServerGroup, TVSettingsChooserRegion} from '../../../kit/accounts/ServerChooserParts';
import {StateView} from '../../../kit/states';
import {serverRowDetail, type ServerRow, type Snapshot} from '../../shared/accounts/useServerChooser';
import {useTVServerChooser} from '../../shared/accounts/useTVServerChooser';

/** The Switch server Settings page's chooser region: Settings' rows enter through it (SettingsPreferences names it). */
export const TV_SERVER_SETTINGS_REGION = 'accounts.chooser';

/** Connect24. Presence is omitted until a real directory observation exists. */
export function TVServerChooserPage(props: ChooserProps) {
  return <FocusProvider><TVAuthLayout testID="tv-server-chooser" title={defaultI18n.t('web.servers.switchServer')}
    body={defaultI18n.t('web.chooser.lede')}>
    <TVAccountRowColumn><Chooser {...props}/></TVAccountRowColumn>
  </TVAuthLayout></FocusProvider>;
}
/** connect-24: a TV's Switch server Settings page draws the same chooser in Settings' page column, under the page's title
 * and line. Settings' list keeps focus when the page opens; Right enters the servers, Left from them returns to the list. */
export function TVServerSettingsPanel(props: ChooserProps) {
  return <TVAccountRowColumn><Chooser {...props} embedded/></TVAccountRowColumn>;
}
type ChooserProps = {
  snapshot?: Snapshot; busy: boolean; error?: React.ReactNode; owner: boolean; onBack?: () => void;
  onSelect: (row: ServerRow) => void; onSignOut: (row: ServerRow) => void; onForget: (row: ServerRow) => void;
  onRefresh: () => void; onAdd: () => void;
  /** Trusted certificates; given the chooser's binder and Refresh servers' handle, their links take part in its moves. */
  certificates?: React.ReactNode | ((bind: (id: string) => (view: View | null) => void, refresh?: number) => React.ReactNode);
  /** The foot (Link a TV, Sign out of Portico Account); given a binder, its controls can be named by the chooser's moves. */
  footer?: React.ReactNode | ((bind: (id: string) => (view: View | null) => void) => React.ReactNode);
};
/** Connect24 starts focus on the first server row; Down moves server to server, then Refresh
 * servers and Use a local server, then the footer's Link a TV. The two actions sit side by side as
 * drawn, so Down from Refresh is pointed at Use a local server (the caption's order); Up from
 * either returns to the server rows. A server's Sign out (only while its sign-in is live) and Forget sit right
 * of its row (Right reaches them, Left returns) and are not on the vertical path. Back is the remote key. A pending switch,
 * sign-out or refresh detaches every control, so the chooser asks for that start again when the
 * run ends. */
function Chooser(props: ChooserProps & {embedded?: boolean}) {
  const {snapshot, busy, error, owner, onSelect, onSignOut, onForget, onRefresh, onAdd, certificates, footer} = props;
  const embedded = props.embedded ?? false;
  const t = defaultI18n.t;
  const chooser = useTVServerChooser(snapshot, busy, embedded);
  const {selected, groups, preferred, serverOrder, addHandle, bindAdd, refreshHandle, bindRefresh, handles, bindHandle} = chooser;
  const lastServer = serverOrder.at(-1);
  const foot = typeof footer === 'function' ? footer(bindHandle) : footer;
  // In Settings every control is in one region whose Left goes to Settings' list, so the buttons beside a row point Left
  // along the row themselves (Forget to Sign out, Sign out to its row; a connected row can't be chosen, so its Sign out
  // keeps the list).
  const left = (id: string | undefined) => embedded && id ? handles[id] : undefined;
  // Apple TV moves Up and Down by position, and the buttons beside the connected row and the two actions sit outside each
  // other's column, so in Settings the vertical moves are named too (TV lane 9: Up and Down from Refresh servers did
  // nothing): Up from the actions goes to the last server (else the connected row's Sign out or Forget), Down from those
  // buttons to the first server (else Refresh servers), and Down from Use a local server to Link a TV. Trusted
  // certificates, when there are any, sit between the servers and the actions: the moves pass through their Refresh.
  const connectedTop = selected ? (selected.signedIn ? `signout:${selected.id}` : `forget:${selected.id}`) : undefined;
  const certificatesLink = embedded ? handles['certificates-refresh'] : undefined;
  const belowServers = certificatesLink ?? refreshHandle;
  const actionsUp = embedded ? certificatesLink ?? handles[lastServer ?? connectedTop ?? ''] : undefined;
  const connectedDown = embedded ? (serverOrder[0] ? handles[serverOrder[0]] : belowServers) : undefined;
  const certificatesNode = typeof certificates === 'function' ? certificates(bindHandle, embedded ? refreshHandle : undefined) : certificates;
  const row = (value: ServerRow, connected = false) => <TVAccountRow key={value.id} title={value.name} detail={serverRowDetail(value)}
    icon="i-server" active={connected} rowRef={embedded && !connected ? bindHandle(`server:${value.id}`) : undefined}
    focusId={`server:${value.id}`} disabled={busy || connected} preferredFocus={!embedded && !connected && value.id === preferred}
    // In Settings, Down from the last server is pointed at the certificates' Refresh, else Refresh servers: by position it
    // would reach Use a local server.
    nextFocusDown={embedded && `server:${value.id}` === lastServer ? belowServers : undefined}
    accessibilityLabel={value.name} accessibilityState={{selected: connected}} onPress={() => onSelect(value)}
    badge={connected && owner ? t('accounts.settings.owner') : undefined}>
    {value.signedIn && <Button ref={embedded ? bindHandle(`signout:${value.id}`) : undefined} focusId={`signout:${value.id}`}
      variant="glass" size="small" disabled={busy} nextFocusLeft={left(connected ? undefined : `server:${value.id}`)}
      nextFocusDown={connected ? connectedDown : undefined}
      accessibilityLabel={t('accounts.signOut.server', {server: value.name})} onPress={() => onSignOut(value)}>
      {t('action.signOut')}
    </Button>}
    <Button ref={embedded ? bindHandle(`forget:${value.id}`) : undefined} focusId={`forget:${value.id}`} variant="glass"
      size="small" disabled={busy} nextFocusDown={connected ? connectedDown : undefined}
      nextFocusLeft={left(value.signedIn ? `signout:${value.id}` : connected ? undefined : `server:${value.id}`)}
      accessibilityLabel={t('connection.identity.forget', {server: value.name})} onPress={() => onForget(value)}>
      {t('connection.identity.forgetAction')}
    </Button>
  </TVAccountRow>;
  const servers = <AccountFields>
    {selected && <ServerGroup title={t('servers.connectedTo')}>{row(selected, true)}</ServerGroup>}
    {groups.filter(group => group.rows.length).map(group => <ServerGroup key={group.id} title={group.title}>
      {group.rows.map(value => row(value))}
    </ServerGroup>)}
  </AccountFields>;
  const refresh = <Button ref={bindRefresh} focusId="refresh" variant="glass" preferredFocus={!embedded && !preferred}
    icon="i-refresh" disabled={busy} nextFocusUp={actionsUp}
    onPress={onRefresh} nextFocusDown={addHandle}>{t('accounts.servers.refresh')}</Button>;
  // In Settings, Left from Use a local server goes to Refresh servers beside it, not past it to Settings' list.
  const add = <Button ref={bindAdd} focusId="add" variant="glass" disabled={busy} nextFocusLeft={embedded ? refreshHandle : undefined}
    nextFocusUp={actionsUp} nextFocusDown={embedded ? handles['link-tv'] : undefined} onPress={onAdd}>{t('accounts.signIn.local')}</Button>;
  if (embedded) {
    // In Settings every control is a stop of this one region, as This device's rows are: Right from the list enters the
    // first server (Refresh servers when there is none). Down runs server to server, then Refresh and Use a local server;
    // Sign out and Forget sit right of their row; the certificates' Refresh and Link a TV are named by the moves above.
    return <TVSettingsChooserRegion id={TV_SERVER_SETTINGS_REGION} entry={preferred ? `server:${preferred}` : 'refresh'}
      order={[...serverOrder, 'refresh', 'add']} orientation="vertical" enabled={!busy}>
      <AccountFields>
        {error}
        {!snapshot && <StateView variant="inline" title={t('status.loading')}/>}
        {servers}
        {certificatesNode}
        <TVAccountActionRow>{refresh}{add}</TVAccountActionRow>
        {foot}
      </AccountFields>
    </TVSettingsChooserRegion>;
  }
  return <AccountFields>
    {error}
    {!snapshot && <StateView variant="inline" title={t('status.loading')}/>}
    <FocusRegion id="accounts.chooser.rows" entry={preferred ? `server:${preferred}` : undefined} order={serverOrder}
      orientation="vertical" edges={{down: 'accounts.chooser.actions'}} enabled={!busy}>
      {servers}
    </FocusRegion>
    {certificatesNode}
    <TVAccountActions id="accounts.chooser.actions" entry="refresh" order={['refresh', 'add']}
      edges={serverOrder.length ? {up: 'accounts.chooser.rows'} : undefined} enabled={!busy}>
      {refresh}{add}
    </TVAccountActions>
    {foot}
  </AccountFields>;
}
