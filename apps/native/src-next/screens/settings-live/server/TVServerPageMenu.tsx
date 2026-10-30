import React from 'react';
import {ScrollView, Text, View} from 'react-native';
import {settingsSpec} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {nextIcons} from '../../../../../../packages/design/src/next-icons';
import {Focusable} from '../../../kit/controls';
import {FocusRegion} from '../../../kit/focus';
import {Icon} from '../../../kit/Icon';
import {ServerHeader} from '../../../kit/shell';
import {shellType} from '../../../kit/shell/visual';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';

export const tvServerPageOrder = ['general', 'dashboard', 'history', 'libraries', 'remote-sources', 'metadata', 'segments', 'live', 'people', 'signin', 'remote', 'integrations', 'streaming', 'storage'] as const;
export type TVServerPage = typeof tvServerPageOrder[number];
export const isTVServerPage = (value: unknown): value is TVServerPage => typeof value === 'string' && (tvServerPageOrder as readonly string[]).includes(value);
export function tvServerPageDefinition(id: TVServerPage) {
  const section = settingsSpec.find(value => value.scope === 'server' && value.id === id);
  if (!section) throw new Error('native_tv_server_page_missing');
  const icon = `i-${section.icon.replace(/^i-/, '')}`;
  return {id, label: section.title, icon: Object.hasOwn(nextIcons, icon) ? icon as React.ComponentProps<typeof Icon>['name'] : 'i-sliders' as const};
}
// The primary-profile admission and reader still enforce these roles. G35 places
// Troubleshooting off television for both roles; no message-log route is offered.
const administratorPages: ReadonlySet<TVServerPage> = new Set(['dashboard', 'people']);

/** Only pages with a real mounted reader enter the menu. Admission is still
 * checked by route composition; this list never grants a reader authority. */
export function TVServerPageMenu({role, page, serverName, runtime, column, rows, rail, onLayout, onSelect}: {
  role: 'owner' | 'administrator'; page: TVServerPage; serverName: string;
  runtime: NativeShellRuntime; column: string; rows: string; rail: string;
  onLayout?: React.ComponentProps<typeof FocusRegion>['onLayout'];
  onSelect: (page: TVServerPage) => void;
}) {
  const theme = useTheme(), t = runtime.text.t;
  return <View style={{width: 480, flexShrink: 0}}>
    <Text accessibilityRole="header" style={[shellType(44, 800), {color: theme.colors.text, marginLeft: 20, marginBottom: 24}]}>{t('next.shell.serverSettings')}</Text>
    <ServerHeader serverName={serverName} viewerRole={role} audience={role === 'owner' ? 'owner' : 'administrators'} variant="group" style={{padding: 20}}/>
    <ScrollView style={{flex: 1}} contentContainerStyle={{paddingHorizontal: 16, paddingBottom: 24}}><FocusRegion id={column} entry={page} orientation="vertical" edges={{left: rail, right: rows}} onLayout={onLayout}>
      {tvServerPageOrder.filter(id => role === 'owner' || administratorPages.has(id)).map(tvServerPageDefinition).map(item => <Focusable key={item.id} focusId={item.id} testID={`server-page-${item.id}`} focusKind="row" accessibilityRole="button" accessibilityLabel={t(item.label)} accessibilityState={{selected: page === item.id}}
        onFocus={() => {if (page !== item.id) onSelect(item.id);}} onPress={() => onSelect(item.id)} style={{minHeight: 56, paddingHorizontal: 20, borderRadius: 14, backgroundColor: page === item.id ? `${theme.colors.accent}29` : 'transparent', flexDirection: 'row', alignItems: 'center', gap: 16}}>
        {page === item.id && <View pointerEvents="none" style={{position: 'absolute', left: 0, top: 14, bottom: 14, width: 4, borderRadius: 2, backgroundColor: theme.colors.accent}}/>}
        <Icon name={item.icon} size={24} color={page === item.id ? theme.colors.accentStrong : theme.colors.textTertiary}/>
        <Text style={[shellType(22, 600), {color: page === item.id ? theme.colors.text : theme.colors.textSecondary}]}>{t(item.label)}</Text>
      </Focusable>)}
    </FocusRegion></ScrollView>
  </View>;
}
