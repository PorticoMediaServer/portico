import React, {type ReactNode} from 'react';
import {Text, View, useWindowDimensions, type StyleProp, type ViewStyle} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {Focusable} from '../controls/Focusable';
import {Button} from '../controls/Button';
import {BottomSheet} from '../overlays/BottomSheet';
import type {BottomSheetProps} from '../overlays/types';
import {Icon, type IconName} from '../Icon';
import {useTheme} from '../theme';
import {focusTestID} from '../focus/registry';
import {HighlightedText} from './HighlightedText';
import {alpha, shellType} from './visual';
import {ServerHeader} from './ServerIdentity';
import {canSeeServerPage, type ServerAudience, type ServerRole} from './authority';
import type {AccountAction} from './AccountShell';

export function LargeTitleHeader({title, trailing, style}: {title: string; trailing?: ReactNode; style?: StyleProp<ViewStyle>}) {
  const theme = useTheme();
  return <View style={[{paddingHorizontal: theme.gutter, paddingTop: 12, paddingBottom: 14, flexDirection: 'row', alignItems: 'center', gap: 12}, style]}><Text accessibilityRole="header" style={[shellType(theme.isTV ? 44 : 32, 800, 'normal', -.035, theme.textScale), {color: theme.colors.text, flex: 1}]}>{title}</Text>{trailing}</View>;
}
export function PushedNavBar({title, backLabel, onBack, overArtwork = false, trailing, testID}: {testID?: string; title?: string; backLabel: string; onBack: () => void; overArtwork?: boolean; trailing?: ReactNode}) {
  const theme = useTheme();
  return <View style={{height: 44, paddingHorizontal: 12, flexDirection: 'row', alignItems: 'center', gap: 8}}>
    <Button testID={controlID(testID, 'back')} variant={overArtwork ? 'icon' : 'text'} size="compact" icon="i-left" iconOnly={overArtwork} accessibilityLabel={backLabel} onPress={onBack} style={{height: 38, width: overArtwork ? 38 : undefined, paddingHorizontal: overArtwork ? 0 : 0}} textStyle={shellType(16, 600)}>{backLabel}</Button>
    {title && <Text numberOfLines={1} pointerEvents="none" style={[shellType(17, 700), {position: 'absolute', left: 76, right: 76, textAlign: 'center', color: theme.colors.text}]}>{title}</Text>}
    <View style={{marginLeft: 'auto'}}>{trailing}</View>
  </View>;
}
export function ShortcutTiles({actions, testID}: {testID?: string; actions: readonly AccountAction[]}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', gap: 8, marginHorizontal: 16, marginTop: 10}}>{actions.map(action => <Focusable key={action.id} testID={controlID(testID, 'shortcut', action.id)} accessibilityRole="button" accessibilityLabel={action.label} onPress={action.onPress} focusRadius={14} style={{flex: 1, height: 70, borderRadius: 14, backgroundColor: theme.colors.raised, alignItems: 'center', justifyContent: 'center', gap: 7, paddingHorizontal: 4}}>
    <Icon name={action.icon} size={24}/>
    {/* phone-app.html .sc4 draws four tiles; with Pending changes there are five, so a one-word label ("Notifications")
        shrinks a little on one line instead of breaking mid-word. Two-word labels wrap between the words. */}
    <Text {...(/\s/.test(action.label.trim()) ? {numberOfLines: 2} : {numberOfLines: 1, adjustsFontSizeToFit: true, minimumFontScale: .75})}
      style={[shellType(11.5, 700, 13.8), {color: theme.colors.textSecondary, textAlign: 'center'}]}>{action.label}</Text>
    {action.trailing && <View pointerEvents="none" style={{position: 'absolute', top: 8, right: 10, minWidth: 18, height: 18, paddingHorizontal: 5, borderRadius: 9, alignItems: 'center', justifyContent: 'center', backgroundColor: theme.colors.accentStrong}}>{action.trailing}</View>}
  </Focusable>)}</View>;
}

/** The accepted sheet owns gesture/back/focus lifecycles. Real settings content is supplied by the caller. */
export function SettingsSheet({profile, shortcuts, children, ...surface}: Omit<BottomSheetProps, 'title' | 'children'> & {profile: ReactNode; shortcuts: ReactNode; children: ReactNode}) {
  const theme = useTheme();
  const {height} = useWindowDimensions();
  const insets = useSafeAreaInsets();
  const page = surface.host === 'inline' && surface.presentation === 'page';
  return <BottomSheet {...surface} title={defaultI18n.t('web.shell.settings')} detents={surface.detents ?? [height - Math.max(insets.top, 62)]} contentStyle={[{backgroundColor: theme.colors.ink}, surface.contentStyle]} style={[{backgroundColor: theme.colors.ink}, page ? undefined : {borderTopLeftRadius: 16, borderTopRightRadius: 16}, surface.style]} headerAction={<Button testID={controlID(surface.testID, 'done')} variant="text" size="small" onPress={() => surface.onOpenChange(false, 'close-button')} style={{height: 32, paddingHorizontal: 8}} textStyle={shellType(16, 700)}>{defaultI18n.t('action.done')}</Button>}>
    {profile}{shortcuts}{children}
  </BottomSheet>;
}

/** Navigation carries a setting's state, never a summary count. Major issues must be known for this exact row. */
export type SettingsNavPage = Readonly<{id: string; label: string; icon: IconName; audience?: ServerAudience; cluster?: 'overview' | 'media' | 'access' | 'system'; stateValue?: string; majorIssue?: boolean}>;
export type SettingsNavProps = {
  testID?: string;
  accountPages: readonly SettingsNavPage[];
  serverPages: readonly SettingsNavPage[];
  viewerRole: ServerRole;
  serverName: string;
  selectedId: string;
  onNavigate: (id: string) => void;
  /** TV D-pad target for Right into the selected page; navigator supplies the real native handle. */
  nextFocusRight?: number;
  mode?: 'account' | 'server' | 'combined';
  search?: ReactNode;
  searchResults?: SettingsSearchResultsProps;
  style?: StyleProp<ViewStyle>;
};
export function SettingsNav({accountPages, serverPages, viewerRole, serverName, selectedId, onNavigate, nextFocusRight, mode, search, searchResults, style, testID}: SettingsNavProps) {
  const theme = useTheme();
  const currentMode = mode ?? (theme.isTV ? 'account' : 'combined');
  const pages = serverPages.filter(page => canSeeServerPage(viewerRole, page.audience ?? 'owner'));
  const row = (page: SettingsNavPage, server: boolean, index: number, previous?: SettingsNavPage) => <View key={page.id} style={{marginTop: previous && page.cluster !== previous.cluster && theme.platform === 'tablet' ? 10 : 0}}>
    <Focusable testID={controlID(testID, server ? 'server' : 'account', page.id)} accessibilityRole="button" accessibilityLabel={page.label} accessibilityState={{selected: page.id === selectedId}} onPress={() => onNavigate(page.id)} onFocus={theme.isTV ? () => onNavigate(page.id) : undefined} nextFocusRight={nextFocusRight} focusKind="row" focusRadius={theme.isTV ? 14 : 10} style={{height: theme.isTV ? currentMode === 'server' ? 50 : 56 : 44, paddingHorizontal: theme.isTV ? 20 : 14, gap: theme.isTV ? 18 : 12, borderRadius: theme.isTV ? 14 : 10, flexDirection: 'row', alignItems: 'center', backgroundColor: page.id === selectedId && theme.platform !== 'phone' ? alpha(theme.colors.accent, .16) : 'transparent'}}>
      {page.id === selectedId && theme.platform !== 'phone' && <View style={{position: 'absolute', left: 0, top: 14, bottom: 14, width: theme.isTV ? 4 : 3, borderRadius: 2, backgroundColor: theme.colors.accent}}/>}
      <View style={theme.isTV ? undefined : {width: 30, height: 30, borderRadius: 8, backgroundColor: server ? alpha(theme.colors.accent, .16) : theme.colors.raisedBright, alignItems: 'center', justifyContent: 'center'}}><Icon name={page.icon} size={theme.isTV ? 24 : 18} color={server || page.id === selectedId ? theme.colors.accentStrong : theme.colors.textTertiary}/></View>
      <Text numberOfLines={1} style={[shellType(theme.isTV ? 22 : 15.5), {color: theme.colors.text, flex: 1}]}>{page.label}</Text>
      {page.stateValue !== undefined && <Text style={[shellType(theme.isTV ? 18 : 14.5), {color: theme.colors.textTertiary}]}>{page.stateValue}</Text>}
      {page.majorIssue === true && <View accessible accessibilityRole="image" accessibilityLabel={defaultI18n.t('next.shell.health.attention')} style={{width: theme.isTV ? 10 : 7, height: theme.isTV ? 10 : 7, borderRadius: theme.isTV ? 5 : 3.5, backgroundColor: theme.colors.record}}/>}
      {!theme.isTV && <Icon name="i-right" size={18} color={theme.colors.lineStrong}/>}
    </Focusable>
    {index > 0 && !theme.isTV && <View pointerEvents="none" style={{position: 'absolute', left: 56, right: 0, top: 0, height: 1, backgroundColor: alpha(theme.colors.lineStrong, .45)}}/>}
  </View>;
  if (searchResults) return <View style={style}>{search}<SettingsSearchResults testID={controlID(testID, 'search')} {...searchResults}/></View>;
  return <View style={style}>{search}
    {currentMode !== 'server' && <><Text style={[shellType(theme.isTV ? 20 : 13.5, 700), {marginTop: 22, marginBottom: 8, marginLeft: 20, color: theme.colors.textTertiary}]}>{defaultI18n.t('settings.heading.account')}</Text><View style={{marginHorizontal: theme.isTV ? 0 : 16, borderRadius: 14, overflow: 'hidden', backgroundColor: theme.isTV ? 'transparent' : theme.colors.raised}}>{accountPages.map((page, index) => row(page, false, index, accountPages[index - 1]))}</View></>}
    {currentMode !== 'account' && pages.length > 0 && <View style={{marginTop: 20, marginHorizontal: theme.isTV ? 0 : 16, borderRadius: theme.isTV ? 20 : 14, overflow: 'hidden', backgroundColor: theme.colors.raised, borderWidth: 1, borderColor: alpha(theme.colors.accentStrong, .28)}}>
      <View style={{backgroundColor: alpha(theme.colors.accent, .1), borderBottomWidth: 1, borderBottomColor: alpha(theme.colors.accentStrong, .22)}}><ServerHeader serverName={serverName} viewerRole={viewerRole} audience={viewerRole === 'administrator' ? 'administrators' : 'owner'} variant={theme.isTV ? 'group' : 'phone'} style={theme.isTV ? {padding: 20} : undefined}/></View>
      {pages.map((page, index) => row(page, true, index, pages[index - 1]))}
    </View>}
  </View>;
}

export type SettingsSearchResultsProps = {
  testID?: string;
  matches: readonly import('../../../../../packages/client-core/src/presentation/settings-structure').SettingsMatch[];
  query: string;
  selectedRowId?: string;
  values?: Readonly<Record<string, string>>;
  onNavigate: (address: string, rowId: string) => void;
};
export function SettingsSearchResults({matches, query, selectedRowId, values = {}, onNavigate, testID}: SettingsSearchResultsProps) {
  const theme = useTheme();
  const groups = new Map<string, typeof matches[number][]>();
  for (const match of matches) {
    const group = groups.get(match.address);
    if (group) group.push(match); else groups.set(match.address, [match]);
  }
  return <View style={{marginTop: 18}}>
    {!matches.length && <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{defaultI18n.t('settings.search.none', {query})}</Text>}
    {Array.from(groups.entries()).map(([address, group]) => <View key={address} style={{marginTop: 12}}><View style={{flexDirection: 'row', alignItems: 'center', paddingHorizontal: 12, paddingBottom: 4}}><Text style={[shellType(theme.isTV ? 20 : 13, 700), {color: theme.colors.textSecondary, flex: 1}]}>{group[0].pageTitle}</Text><Text style={[shellType(12, 700), {color: theme.colors.textTertiary, paddingHorizontal: 7, paddingVertical: 2, borderRadius: 6, backgroundColor: alpha(theme.colors.text, .08)}]}>{defaultI18n.number(group.length)}</Text></View>
      {group.map(match => <Focusable key={match.rowId} testID={controlID(testID, 'match', match.address, match.rowId)} accessibilityRole="button" accessibilityLabel={match.label} accessibilityState={{selected: match.rowId === selectedRowId}} onPress={() => onNavigate(match.address, match.rowId)} focusKind="row" focusRadius={9} style={{paddingVertical: 8, paddingLeft: 40, paddingRight: 12, borderRadius: 9, backgroundColor: match.rowId === selectedRowId ? alpha(theme.colors.text, .1) : 'transparent'}}><HighlightedText text={match.label} query={query} style={[shellType(theme.isTV ? 22 : 14.5), {color: theme.colors.text}]}/>{values[match.rowId] !== undefined && <Text style={[shellType(theme.isTV ? 20 : 12.5), {color: theme.colors.textTertiary, marginTop: 1}]}>{values[match.rowId]}</Text>}</Focusable>)}
    </View>)}
  </View>;
}

const controlID = (prefix: string | undefined, ...parts: readonly string[]) => prefix ? focusTestID(prefix, JSON.stringify(parts)) : undefined;
