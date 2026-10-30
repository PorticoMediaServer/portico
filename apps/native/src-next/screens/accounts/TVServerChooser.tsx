import React from 'react';
import {ScrollView, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {PhoneServerRow} from './PhoneServerChooser';
import type {nativeAccounts} from '../../platform/accounts';
import {ColourBackdrop} from '../../kit/artwork';
import {Button, Focusable} from '../../kit/controls';
import {FocusProvider, FocusRegion} from '../../kit/focus';
import {Icon} from '../../kit/Icon';
import {nativeType, useTheme} from '../../kit/theme';
import {useDestinationEntryFocus} from '../destinations/useEntryFocus';
import {useTVBusyEntryRevision} from '../../tv/auth/entry-revision';

type Snapshot = Awaited<ReturnType<typeof nativeAccounts.snapshot>>;

/** Connect24. Presence is omitted until a real directory observation exists. */
export function TVServerChooser(props: {
  snapshot?: Snapshot; busy: boolean; error?: React.ReactNode; owner: boolean; onBack?: () => void;
  onSelect: (row: PhoneServerRow) => void; onSignOut: (row: PhoneServerRow) => void;
  onRefresh: () => void; onAdd: () => void; certificates?: React.ReactNode; footer?: React.ReactNode;
}) {
  return <FocusProvider><Chooser {...props}/></FocusProvider>;
}
/** Connect24 starts focus on the first server row; Down moves server to server, then Refresh
 * servers and Use a local server. A server's Forget sits right of its row (Right reaches it, Left
 * returns) and is not on the vertical path. Back is the remote key. A pending switch,
 * sign-out or refresh detaches every control, so the chooser asks for that
 * start again when the run ends. */
function Chooser({snapshot, busy, error, owner, onSelect, onSignOut, onRefresh, onAdd, certificates, footer}: {
  snapshot?: Snapshot; busy: boolean; error?: React.ReactNode; owner: boolean; onBack?: () => void;
  onSelect: (row: PhoneServerRow) => void; onSignOut: (row: PhoneServerRow) => void;
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
  useDestinationEntryFocus({visible: !busy, intent: 'enter', revision: entryRevision},
    preferred ? {regionId: 'accounts.chooser.rows', childId: `server:${preferred}`} : {regionId: 'accounts.chooser.actions', childId: 'refresh'});
  const row = (value: PhoneServerRow, connected = false) => <View key={value.id} style={{flexDirection: 'row', alignItems: 'center', gap: 40, padding: 16, borderBottomWidth: 1, borderBottomColor: theme.colors.lineSoft}}>
    <Focusable focusId={`server:${value.id}`} disabled={busy || connected} accessibilityRole="button" hasTVPreferredFocus={!connected && value.id === preferred} accessibilityLabel={value.name} accessibilityState={{selected: connected}}
      onPress={() => onSelect(value)} focusKind="row" focusRadius={12} style={{flex: 1, minHeight: 64, flexDirection: 'row', alignItems: 'center', gap: 16}}>
      <Icon name="i-server" size={28} color={theme.colors.accentStrong}/>
      <View style={{flex: 1, gap: 6}}>
        <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{value.name}</Text>
        <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t(value.labelKey)}</Text>
      </View>
      {connected && owner && <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{t('accounts.settings.owner')}</Text>}
      {!connected && <Icon name="i-right" size={26} color={theme.colors.textTertiary}/>}
    </Focusable>
    <Button focusId={`signout:${value.id}`} variant="text" size="small" disabled={busy} onPress={() => onSignOut(value)}>{t('accounts.signOut.server', {server: value.name})}</Button>
  </View>;
  return <View testID="tv-server-chooser" style={{flex: 1, backgroundColor: theme.colors.ink}}>
    <ColourBackdrop shape="side" fill/>
    <ScrollView contentContainerStyle={{paddingHorizontal: theme.layout.gutter.tvAuth, paddingTop: 80, paddingBottom: 80, gap: 24}}>
      <View style={{gap: 12}}>
        <Text accessibilityRole="header" style={[nativeType({size: 44, lineHeight: 52, weight: 700, tracking: 0}, theme.textScale), {color: theme.colors.text}]}>{t('web.servers.switchServer')}</Text>
        <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('web.chooser.lede')}</Text>
        {error}
        {!snapshot && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('status.loading')}</Text>}
      </View>
      <FocusRegion id="accounts.chooser.rows" entry={preferred ? `server:${preferred}` : undefined} order={serverOrder} orientation="vertical" edges={{down: 'accounts.chooser.actions'}} enabled={!busy} style={{gap: 24}}>
      {selected && <View style={{gap: 12}}>
        <Text accessibilityRole="header" style={[theme.type.bodyStrong, {color: theme.colors.textSecondary}]}>{t('servers.connectedTo')}</Text>
        <View style={{backgroundColor: theme.colors.raised, borderRadius: 16}}>{row(selected, true)}</View>
      </View>}
      {groups.filter(group => group.rows.length).map(group => <View key={group.id} style={{gap: 12}}>
        <Text accessibilityRole="header" style={[theme.type.bodyStrong, {color: theme.colors.textSecondary}]}>{group.title}</Text>
        <View style={{backgroundColor: theme.colors.raised, borderRadius: 16}}>{group.rows.map(value => row(value))}</View>
      </View>)}
      </FocusRegion>
      {certificates}
      <FocusRegion id="accounts.chooser.actions" entry="refresh" edges={serverOrder.length ? {up: 'accounts.chooser.rows'} : undefined} enabled={!busy} style={{flexDirection: 'row', flexWrap: 'wrap', gap: 16}}>
        <Button focusId="refresh" variant="glass" hasTVPreferredFocus={!preferred} icon="i-refresh" disabled={busy} onPress={onRefresh}>{t('accounts.servers.refresh')}</Button>
        <Button focusId="add" variant="glass" disabled={busy} onPress={onAdd}>{t('accounts.signIn.local')}</Button>
      </FocusRegion>
      {footer}
    </ScrollView>
  </View>;
}
