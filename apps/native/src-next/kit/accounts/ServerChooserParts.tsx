import React, {type ReactNode} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {serverRowDetail, type ServerRow} from '../../pages/shared/accounts/useServerChooser';
import {Button, Focusable} from '../controls';
import {Icon} from '../Icon';
import {useTheme} from '../theme';

export function ServerChooserScroll({children}: {children: ReactNode}) {
  const theme = useTheme(), insets = useSafeAreaInsets();
  return <ScrollView contentContainerStyle={{paddingHorizontal: theme.gutter, paddingTop: theme.space.sm,
    paddingBottom: insets.bottom + theme.space.md, gap: theme.space.sm}}>{children}</ScrollView>;
}

export function ServerGroup({title, children}: {title: string; children: ReactNode}) {
  const theme = useTheme();
  return <View style={{gap: theme.space.xs}}>
    <Text accessibilityRole="header" style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{title}</Text>
    <View style={{borderRadius: theme.radii.artwork, backgroundColor: theme.colors.raised,
      overflow: 'hidden', borderWidth: theme.space.hairline, borderColor: theme.colors.lineStrong}}>{children}</View>
  </View>;
}

export function ServerChooserRow({server, selected = false, busy, onSelect, onMenu}: {server: ServerRow; selected?: boolean; busy: boolean; onSelect: () => void; onMenu: () => void}) {
  const theme = useTheme(), t = defaultI18n.t;
  return <View style={{flexDirection: 'row', alignItems: 'center', borderBottomWidth: 1, borderBottomColor: theme.colors.lineSoft}}>
    <Focusable testID={`phone-server-${server.id}`} disabled={busy} accessibilityRole="button" accessibilityState={{selected}}
      accessibilityLabel={server.name} accessibilityHint={serverRowDetail(server)} onPress={onSelect}
      style={{flex: 1, minHeight: 60, paddingVertical: 12, paddingLeft: 14, flexDirection: 'row', alignItems: 'center', gap: 12}}>
      <View style={{width: 30, height: 34, borderRadius: 8, alignItems: 'center', justifyContent: 'center', backgroundColor: theme.colors.raised}}><Icon name="i-server" size={18} color={theme.colors.accentStrong}/></View>
      <View style={{flex: 1, gap: 3}}><Text style={[selected ? theme.type.listLabel : theme.type.rowLabel, {color: theme.colors.text}]}>{server.name}</Text>
        <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{serverRowDetail(server)}</Text></View>
      {!selected && <Icon name="i-right" size={16} color={theme.colors.textTertiary}/>}
    </Focusable>
    <Button testID={`phone-server-menu-${server.id}`} variant="icon" iconOnly size="compact" icon="i-more" disabled={busy}
      accessibilityLabel={t('next.browse.native.moreFor', {title: server.name})} onPress={onMenu} style={{minWidth: 42, paddingHorizontal: 8}}/>
  </View>;
}
