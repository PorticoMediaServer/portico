import React, {useState} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {nativeAccounts} from '../../platform/accounts';
import {Button, Focusable} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {Menu} from '../../kit/overlays';
import {shellType} from '../../kit/shell/visual';
import {useTheme} from '../../kit/theme';
import {Quiet} from '../../kit/states';
import {StandardBackdrop} from '../../kit/artwork';

type Snapshot = Awaited<ReturnType<typeof nativeAccounts.snapshot>>;
export type PhoneServerRow = Snapshot['servers'][number];

/** Connect22's phone list. Session capture, switching and sign-out remain with ServersRoute. */
export function PhoneServerChooser({snapshot, busy, error, onBack, onSelect, onSignOut, onRefresh, onAdd, footer, certificates}: {
  snapshot?: Snapshot; busy: boolean; error?: React.ReactNode; onBack?: () => void;
  onSelect: (server: PhoneServerRow) => void; onSignOut: (server: PhoneServerRow) => void;
  onRefresh: () => void; onAdd: () => void; footer?: React.ReactNode; certificates?: React.ReactNode;
}) {
  const theme = useTheme(), insets = useSafeAreaInsets(), t = defaultI18n.t;
  const [menu, setMenu] = useState<PhoneServerRow>();
  const current = snapshot?.servers.find(server => server.id === snapshot.selectedServerId);
  const groups = [
    {id: 'portico', label: 'accounts.servers.title' as const, rows: snapshot?.servers.filter(server => server.id !== current?.id && server.signIn === 'portico') ?? []},
    {id: 'local', label: 'accounts.servers.local' as const, rows: snapshot?.servers.filter(server => server.id !== current?.id && server.signIn !== 'portico') ?? []},
  ];
  const row = (server: PhoneServerRow, selected = false) => <View key={server.id} style={{flexDirection: 'row', alignItems: 'center', borderBottomWidth: 1, borderBottomColor: theme.colors.lineSoft}}>
    <Focusable testID={`phone-server-${server.id}`} disabled={busy} accessibilityRole="button" accessibilityState={{selected}}
      accessibilityLabel={server.name} accessibilityHint={t(server.labelKey)} onPress={() => onSelect(server)}
      style={{flex: 1, minHeight: 60, paddingVertical: 12, paddingLeft: 14, flexDirection: 'row', alignItems: 'center', gap: 12}}>
      <View style={{width: 30, height: 34, borderRadius: 8, alignItems: 'center', justifyContent: 'center', backgroundColor: theme.colors.raised}}><Icon name="i-server" size={18} color={theme.colors.accentStrong}/></View>
      <View style={{flex: 1, gap: 3}}><Text style={[shellType(15, selected ? 700 : 600), {color: theme.colors.text}]}>{server.name}</Text>
        <Text style={[shellType(12.5, 400, 17), {color: theme.colors.textSecondary}]}>{t(server.labelKey)}</Text></View>
      {!selected && <Icon name="i-right" size={16} color={theme.colors.textTertiary}/>}
    </Focusable>
    <Button testID={`phone-server-menu-${server.id}`} variant="icon" iconOnly size="compact" icon="i-more" disabled={busy}
      accessibilityLabel={t('next.browse.native.moreFor', {title: server.name})} onPress={() => setMenu(server)} style={{minWidth: 42, paddingHorizontal: 8}}/>
  </View>;
  return <View testID="phone-server-chooser" style={{flex: 1, paddingTop: insets.top + 12}}><StandardBackdrop/>
    <View style={{height: 5, width: 38, borderRadius: 3, alignSelf: 'center', backgroundColor: theme.colors.textTertiary, marginBottom: 8}}/>
    <View style={{height: 40, flexDirection: 'row', alignItems: 'center', paddingHorizontal: 12}}>
      <View style={{width: 90}}>{onBack && <Button testID="phone-server-back" variant="text" size="compact" icon="i-left" disabled={busy} onPress={onBack}>{t('next.shell.settings')}</Button>}</View>
      <Text accessibilityRole="header" style={[shellType(17, 700), {flex: 1, color: theme.colors.text, textAlign: 'center'}]}>{t('web.servers.switchServer')}</Text><View style={{width: 90}}/>
    </View>
    <ScrollView contentContainerStyle={{paddingHorizontal: 18, paddingTop: 18, paddingBottom: insets.bottom + 24}}>
      {error != null && error !== false && <View style={{marginBottom: 18}}>{error}</View>}
      {!snapshot && <Quiet><Text accessibilityRole="text" style={[shellType(14, 400), {color: theme.colors.textSecondary}]}>{t('status.loading')}</Text></Quiet>}
      {current && <><Text style={[shellType(13.5, 600), {color: theme.colors.textSecondary, marginBottom: 8}]}>{t('web.chooser.current')}</Text>
        <View style={{borderRadius: 16, backgroundColor: theme.colors.raised, overflow: 'hidden', borderWidth: 1, borderColor: theme.colors.lineStrong}}>{row(current, true)}</View></>}
      {groups.filter(group => group.rows.length).map(group => <View key={group.id} style={{marginTop: 20}}>
        <Text style={[shellType(13.5, 600), {color: theme.colors.textSecondary, marginBottom: 8}]}>{t(group.label)}</Text>
        <View style={{borderRadius: 16, backgroundColor: theme.colors.raised, overflow: 'hidden'}}>{group.rows.map(server => row(server))}</View>
      </View>)}
      <View style={{gap: 10, marginTop: 18}}>
        <Button testID="phone-server-refresh" variant="glass" size="compact" icon="i-refresh" disabled={busy} onPress={onRefresh}>{t('accounts.servers.refresh')}</Button>
        <Button testID="phone-server-add" variant="glass" size="compact" disabled={busy} onPress={onAdd}>{t('accounts.signIn.local')}</Button>
      </View>
      {certificates && <View style={{marginTop: 20}}>{certificates}</View>}
      {footer != null && footer !== false && <View style={{marginTop: 20}}>{footer}</View>}
    </ScrollView>
    <Menu open={Boolean(menu)} onOpenChange={open => {if (!open) setMenu(undefined);}} presentation="sheet" title={menu?.name}
      items={menu ? [{id: 'sign-out', label: t('accounts.signOut.server', {server: menu.name}), onSelect: () => {const server = menu; setMenu(undefined); onSignOut(server);}}] : []}/>
  </View>;
}
