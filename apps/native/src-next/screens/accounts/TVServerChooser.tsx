import React, {useCallback, useState} from 'react';
import {findNodeHandle, Text, View} from 'react-native';
import {rowSpec, tvAccountSpec} from '@portico/design';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {serverRowDetail, type PhoneServerRow} from './PhoneServerChooser';
import type {nativeAccounts} from '../../platform/accounts';
import {TVAuthLayout} from '../../tv/auth/TVAuthLayout';
import {Button} from '../../kit/controls';
import {FocusProvider, FocusRegion} from '../../kit/focus';
import {TVAccountRow, TVAccountRowColumn} from '../../kit/controls/TVAccountRow';
import {useTheme} from '../../kit/theme';
import {useDestinationEntryFocus} from '../destinations/useEntryFocus';
import {useTVBusyEntryRevision} from '../../tv/auth/entry-revision';

type Snapshot = Awaited<ReturnType<typeof nativeAccounts.snapshot>>;

/** Connect24. Presence is omitted until a real directory observation exists. */
export function TVServerChooser(props: {
  snapshot?: Snapshot; busy: boolean; error?: React.ReactNode; owner: boolean; onBack?: () => void;
  onSelect: (row: PhoneServerRow) => void; onSignOut: (row: PhoneServerRow) => void; onForget: (row: PhoneServerRow) => void;
  onRefresh: () => void; onAdd: () => void; certificates?: React.ReactNode; footer?: React.ReactNode;
}) {
  return <FocusProvider><Chooser {...props}/></FocusProvider>;
}
/** Connect24 starts focus on the first server row; Down moves server to server, then Refresh
 * servers and Use a local server, then the footer's Link a TV. The two actions sit side by side as
 * drawn, so Down from Refresh is pointed at Use a local server (the caption's order); Up from
 * either returns to the server rows. A server's Sign out (only while its sign-in is live) and Forget sit right
 * of its row (Right reaches them, Left returns) and are not on the vertical path. Back is the remote key. A pending switch,
 * sign-out or refresh detaches every control, so the chooser asks for that start again when the
 * run ends. */
function Chooser({snapshot, busy, error, owner, onSelect, onSignOut, onForget, onRefresh, onAdd, certificates, footer}: {
  snapshot?: Snapshot; busy: boolean; error?: React.ReactNode; owner: boolean; onBack?: () => void;
  onSelect: (row: PhoneServerRow) => void; onSignOut: (row: PhoneServerRow) => void; onForget: (row: PhoneServerRow) => void;
  onRefresh: () => void; onAdd: () => void; certificates?: React.ReactNode; footer?: React.ReactNode;
}) {
  const theme = useTheme(), t = defaultI18n.t;
  const selected = snapshot?.servers.find(row => row.id === snapshot.selectedServerId);
  const groups = [
    {id: 'portico', title: t('servers.yours'), rows: snapshot?.servers.filter(row => row.id !== selected?.id && row.signIn === 'portico') ?? []},
    {id: 'local', title: t('accounts.servers.local'), rows: snapshot?.servers.filter(row => row.id !== selected?.id && row.signIn === 'local') ?? []},
  ];
  const preferred = groups.flatMap(group => group.rows)[0]?.id;
  // Vertical order is the choosable servers only; each Forget is reached sideways.
  const serverOrder = groups.flatMap(group => group.rows).map(value => `server:${value.id}`);
  const entryRevision = useTVBusyEntryRevision(busy);
  // Down from Refresh servers goes to Use a local server beside it, not past it to the footer.
  const [addHandle, setAddHandle] = useState<number | undefined>(undefined);
  const bindAdd = useCallback((view: View | null) => {
    setAddHandle(view === null ? undefined : findNodeHandle(view) ?? undefined);
  }, []);
  useDestinationEntryFocus({visible: !busy, intent: 'enter', revision: entryRevision},
    preferred ? {regionId: 'accounts.chooser.rows', childId: `server:${preferred}`} : {regionId: 'accounts.chooser.actions', childId: 'refresh'});
  const row = (value: PhoneServerRow, connected = false) => <TVAccountRow key={value.id} title={value.name} detail={serverRowDetail(value)} icon="i-server" active={connected}
    focusId={`server:${value.id}`} disabled={busy || connected} preferredFocus={!connected && value.id === preferred}
    accessibilityLabel={value.name} accessibilityState={{selected: connected}} onPress={() => onSelect(value)}
    badge={connected && owner ? t('accounts.settings.owner') : undefined}>
    {value.signedIn && <Button focusId={`signout:${value.id}`} variant="glass" size="small" disabled={busy} accessibilityLabel={t('accounts.signOut.server', {server: value.name})} onPress={() => onSignOut(value)}>{t('action.signOut')}</Button>}
    <Button focusId={`forget:${value.id}`} variant="glass" size="small" disabled={busy} accessibilityLabel={t('connection.identity.forget', {server: value.name})} onPress={() => onForget(value)}>{t('connection.identity.forgetAction')}</Button>
  </TVAccountRow>;
  return <TVAuthLayout testID="tv-server-chooser" title={t('web.servers.switchServer')} body={t('web.chooser.lede')}>
    {/* One face width and one chevron column for every server and certificate row on the page. */}
    <TVAccountRowColumn><View style={{gap: tvAccountSpec.fieldGap}}>
      {error}
      {!snapshot && <Text style={[theme.type.accountLede, {color: theme.colors.textSecondary}]}>{t('status.loading')}</Text>}
      <FocusRegion id="accounts.chooser.rows" entry={preferred ? `server:${preferred}` : undefined} order={serverOrder} orientation="vertical" edges={{down: 'accounts.chooser.actions'}} enabled={!busy} style={{gap: 24}}>
      {selected && <View style={{gap: 12}}>
        <Text accessibilityRole="header" style={[theme.type.bodyStrong, {color: theme.colors.textSecondary}]}>{t('servers.connectedTo')}</Text>
        <View style={{backgroundColor: theme.colors.raised, borderRadius: rowSpec.group.radius.tv}}>{row(selected, true)}</View>
      </View>}
      {groups.filter(group => group.rows.length).map(group => <View key={group.id} style={{gap: 12}}>
        <Text accessibilityRole="header" style={[theme.type.bodyStrong, {color: theme.colors.textSecondary}]}>{group.title}</Text>
        <View style={{backgroundColor: theme.colors.raised, borderRadius: rowSpec.group.radius.tv}}>{group.rows.map(value => row(value))}</View>
      </View>)}
      </FocusRegion>
      {certificates}
      <FocusRegion id="accounts.chooser.actions" entry="refresh" order={['refresh', 'add']} edges={serverOrder.length ? {up: 'accounts.chooser.rows'} : undefined} enabled={!busy} style={{flexDirection: 'row', flexWrap: 'wrap', gap: 16}}>
        <Button focusId="refresh" variant="glass" preferredFocus={!preferred} icon="i-refresh" disabled={busy} onPress={onRefresh} nextFocusDown={addHandle}>{t('accounts.servers.refresh')}</Button>
        <Button ref={bindAdd} focusId="add" variant="glass" disabled={busy} onPress={onAdd}>{t('accounts.signIn.local')}</Button>
      </FocusRegion>
      {footer}
    </View></TVAccountRowColumn>
  </TVAuthLayout>;
}
