import {headerWordmark} from '../kit/shell/brand';
import React, {useState} from 'react';
import {Image, ScrollView, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import Svg, {Defs, LinearGradient, Rect, Stop, SvgXml} from 'react-native-svg';
import {brandSymbol} from '../../assets/brandSymbol';
import {defaultI18n} from '../../../../packages/i18n/src';
import type {Library} from '../../../../packages/client-core/src';
import {Focusable} from '../kit/controls';
import {Icon, type IconName} from '../kit/Icon';
import {ProfileFace, type ProfileOption} from '../kit/artwork';
import {nativeType, useTheme} from '../kit/theme';
import {BlurView} from '../platform/native-blur';
import {mediumLibraryIcon, mediumRailMetrics, mediumRailPadding, visibleLibraries, type ShellDestination} from './model';
import {sameDestination} from './tv-focus';

type RailEntry = Readonly<{key: string; label: string; icon: IconName; destination: ShellDestination}>;

/** foldable1/tablet: the 84 pt medium rail replaces the phone tab bar. Search,
 * Home, Live TV, Saved, Settings and Profile stay put; only the libraries scroll. */
export function MediumRail({libraries, hasLiveTV, destination, profile, unread, serverAction, onNavigate, onProfile, profileRef, short, expanded = false, onToggle, serverName, notificationAction}: {
  expanded?: boolean; onToggle?: () => void; serverName?: string;
  libraries: readonly Library[]; hasLiveTV: boolean; destination: ShellDestination; profile: ProfileOption; unread: boolean;
  /** Owner/administrator-only Server activity, icon-only as tablet 2/3 draw it. */
  serverAction?: Readonly<{id: string; label: string; onPress: () => void}>;
  /** The real account Notifications action, with the unread badge on the bell. */
  notificationAction?: Readonly<{label: string; onPress: () => void}>;
  onNavigate: (destination: ShellDestination) => void; onProfile: () => void; profileRef: React.RefObject<View | null>; short: boolean;
}) {
  const theme = useTheme(), insets = useSafeAreaInsets(), padding = mediumRailPadding(insets.top, insets.bottom);
  const [libraryViewport, setLibraryViewport] = useState(0), [libraryContent, setLibraryContent] = useState(0);
  const overflow = libraryContent > libraryViewport + 1;
  const top: RailEntry[] = [
    {key: 'search', icon: 'i-search', label: defaultI18n.t('web.shell.search'), destination: {kind: 'search'}},
    {key: 'home', icon: 'i-home', label: defaultI18n.t('web.shell.home'), destination: {kind: 'home'}},
  ];
  const libraryEntries: RailEntry[] = visibleLibraries(libraries, false).map(library => ({key: 'library:' + library.id, label: library.name,
    icon: mediumLibraryIcon(library.kind), destination: {kind: 'library' as const, libraryId: library.id}}));
  const below: RailEntry[] = [
    ...(hasLiveTV ? [{key: 'live', icon: 'i-tv' as const, label: defaultI18n.t('search.chip.liveTV'), destination: {kind: 'live' as const}}] : []),
    {key: 'saved', icon: 'i-bookmark', label: defaultI18n.t('saved.title'), destination: {kind: 'saved'}},
  ];
  const item = (entry: RailEntry) => {
    const selected = sameDestination(entry.destination, destination);
    return <Focusable key={entry.key} testID={`medium-rail-${entry.key}`} accessibilityRole="button" accessibilityLabel={entry.label}
      accessibilityState={{selected}} onPress={() => onNavigate(entry.destination)} focusRadius={mediumRailMetrics.itemRadius}
      style={{width: expanded ? 224 : mediumRailMetrics.itemWidth, minHeight: expanded ? 44 : 48, paddingTop: 8, paddingBottom: 6, borderRadius: mediumRailMetrics.itemRadius,
        flexDirection: expanded ? 'row' : 'column', paddingHorizontal: expanded ? 12 : 0, alignItems: 'center', justifyContent: expanded ? 'flex-start' : 'center', gap: expanded ? 12 : 4, backgroundColor: selected ? `${theme.colors.text}1A` : undefined}}>
      <Icon name={entry.icon} size={expanded ? 20 : mediumRailMetrics.iconSize} color={selected ? theme.colors.text : theme.colors.textTertiary}/>
      <Text numberOfLines={expanded ? 1 : 2} style={[nativeType({size: expanded ? 15 : 10.5, weight: 600, tracking: expanded ? 0 : -0.005, lineHeight: 'normal'}, theme.textScale), {lineHeight: expanded ? 20 : 13.5, color: selected ? theme.colors.text : theme.colors.textTertiary, maxWidth: expanded ? 168 : mediumRailMetrics.itemWidth, textAlign: expanded ? 'left' : 'center'}]}>{entry.label}</Text>
    </Focusable>;
  };
  const hairline = <View accessible={false} style={{width: expanded ? 200 : 40, height: 1, backgroundColor: theme.colors.line, marginVertical: expanded ? 9 : 6}}/>;
  return <View testID="medium-rail" accessibilityLabel={defaultI18n.t('next.home.native.mainNavigation')}
    style={{position: 'absolute', left: 0, top: 0, bottom: 0, width: expanded ? 248 : mediumRailMetrics.width, zIndex: theme.layers.navigation, overflow: 'hidden',
      borderRightWidth: 1, borderRightColor: theme.colors.line}}>
    <BlurView pointerEvents="none" style={{position: 'absolute', inset: 0}} blurType="dark" blurAmount={18} reducedTransparencyFallbackColor={theme.colors.raised}/>
    <View pointerEvents="none" style={{position: 'absolute', inset: 0, backgroundColor: theme.colors.ink, opacity: .72}}/>
    <View style={{flex: 1, alignItems: 'center', paddingTop: padding.top, paddingBottom: padding.bottom}}>
      {onToggle && !expanded && <View style={{height: 44, alignItems: 'center', justifyContent: 'center'}}><SvgXml xml={brandSymbol} width={19} height={28}/></View>}
      {onToggle && <View style={{width: '100%', height: expanded ? 48 : 44, paddingHorizontal: expanded ? 24 : 0, flexDirection: 'row', alignItems: 'center', justifyContent: expanded ? 'space-between' : 'center'}}>
        {expanded && <Image accessible accessibilityLabel={defaultI18n.t('next.platform.appName')} source={require('../../assets/wordmark.png')} resizeMode="contain" style={{...headerWordmark.tabletRail, flexShrink: 0}}/>}
        <Focusable testID="sidebar-toggle" accessibilityRole="button" accessibilityLabel={defaultI18n.t(expanded ? 'web.shell.collapse' : 'web.shell.expand')} accessibilityState={{expanded}} onPress={onToggle} focusRadius={12} style={{width: 44, height: 44, alignItems: 'center', justifyContent: 'center'}}>
          <Svg width={22} height={22} viewBox="0 0 24 24" color={theme.colors.textSecondary}><Rect x={3.5} y={4.5} width={17} height={15} rx={3} fill="none" stroke="currentColor" strokeWidth={2}/><Rect x={8.5} y={4.5} width={1.5} height={15} fill="currentColor"/></Svg>
        </Focusable>
      </View>}
      {top.map(item)}
      {expanded && <Text style={[nativeType({size: 12, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {alignSelf: 'flex-start', marginTop: 14, marginLeft: 24, marginBottom: 4, color: theme.colors.textTertiary}]}>{defaultI18n.t('web.shell.libraries')}</Text>}
      <View style={{flexGrow: 0, flexShrink: 1, minHeight: 0, width: '100%', marginTop: short ? 2 : 0, gap: short ? 2 : 0}}>
        <ScrollView style={{flexGrow: 0, flexShrink: 1, width: '100%'}} contentContainerStyle={{alignItems: 'center', gap: short ? 2 : mediumRailMetrics.gap}}
          showsVerticalScrollIndicator={false} onLayout={event => setLibraryViewport(event.nativeEvent.layout.height)}
          onContentSizeChange={(_width, height) => setLibraryContent(height)}>
          {libraryEntries.map(item)}
        </ScrollView>
        {overflow && <View pointerEvents="none" accessible={false} style={{position: 'absolute', left: 0, right: 0, bottom: -1, height: mediumRailMetrics.fadeHeight}}>
          <Svg width="100%" height="100%"><Defs><LinearGradient id="medium-rail-fade" x1="0" y1="0" x2="0" y2="1"><Stop offset="0" stopColor={theme.colors.ink} stopOpacity={0}/><Stop offset="1" stopColor={theme.colors.ink} stopOpacity={.92}/></LinearGradient></Defs>
            <Rect width="100%" height="100%" fill="url(#medium-rail-fade)"/></Svg>
        </View>}
      </View>
      {hairline}
      {below.map(item)}
      {notificationAction && <Focusable testID="medium-rail-notifications" accessibilityRole="button" accessibilityLabel={notificationAction.label} onPress={notificationAction.onPress} focusRadius={mediumRailMetrics.itemRadius}
        style={{width: expanded ? 224 : mediumRailMetrics.itemWidth, minHeight: expanded ? 44 : 48, paddingTop: expanded ? 0 : 8, paddingBottom: expanded ? 0 : 6,
          borderRadius: mediumRailMetrics.itemRadius, flexDirection: expanded ? 'row' : 'column', gap: expanded ? 12 : 0, paddingHorizontal: expanded ? 12 : 0, alignItems: 'center', justifyContent: expanded ? 'flex-start' : 'center'}}>
        <View style={{width: expanded ? 20 : mediumRailMetrics.iconSize, height: expanded ? 20 : mediumRailMetrics.iconSize, alignItems: 'center', justifyContent: 'center'}}>
          <Icon name="i-bell" size={expanded ? 20 : mediumRailMetrics.iconSize} color={theme.colors.textTertiary}/>
          {unread && <View accessible={false} style={{position: 'absolute', top: -1, right: -2, width: 7, height: 7, borderRadius: 4, backgroundColor: theme.colors.accentStrong, borderWidth: 2, borderColor: theme.colors.ink}}/>}
        </View>
        {expanded && <Text numberOfLines={1} style={[nativeType({size: 15, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textSecondary}]}>{notificationAction.label}</Text>}
      </Focusable>}
      {hairline}
      {serverAction && <Focusable testID="medium-rail-server" accessibilityRole="button" accessibilityLabel={serverAction.label} onPress={serverAction.onPress} focusRadius={mediumRailMetrics.itemRadius}
        style={{width: expanded ? 224 : mediumRailMetrics.itemWidth, minHeight: expanded ? 44 : 48, paddingTop: 8, paddingBottom: 6, borderRadius: mediumRailMetrics.itemRadius, flexDirection: expanded ? 'row' : 'column', gap: expanded ? 12 : 0, paddingHorizontal: expanded ? 12 : 0, alignItems: 'center', justifyContent: expanded ? 'flex-start' : 'center'}}>
        <Icon name="i-activity" size={expanded ? 20 : mediumRailMetrics.iconSize} color={theme.colors.textTertiary}/>{expanded && <Text style={[nativeType({size: 15, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textSecondary}]}>{serverAction.label}</Text>}
      </Focusable>}
      <View style={{marginTop: 'auto', alignItems: 'center'}}>
        {item({key: 'settings', icon: 'i-settings', label: defaultI18n.t('settings.title'), destination: {kind: 'settings'}})}
        <Focusable testID="medium-rail-profile" accessibilityRole="button" accessibilityLabel={defaultI18n.t('next.home.native.profileAndServer')} onPress={onProfile} focusRadius={mediumRailMetrics.itemRadius}
          style={{width: expanded ? 224 : mediumRailMetrics.itemWidth, minHeight: expanded ? 60 : 48, marginTop: expanded ? 6 : 0, backgroundColor: expanded ? `${theme.colors.text}0F` : undefined, paddingTop: expanded ? 0 : 8, paddingBottom: expanded ? 0 : 6, borderRadius: 14, flexDirection: expanded ? 'row' : 'column', paddingHorizontal: expanded ? 10 : 0, alignItems: 'center', justifyContent: expanded ? 'flex-start' : 'center', gap: expanded ? 12 : 4}}>
          <View ref={profileRef} collapsable={false} style={{width: expanded ? 38 : 30, height: expanded ? 38 : 30}}><ProfileFace {...profile} size={expanded ? 38 : 30} locked={profile.pinRequired} scaleOnFocus={false}/></View>
          {expanded ? <View style={{flex: 1, minWidth: 0}}><Text numberOfLines={1} style={[nativeType({size: 14, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{profile.name}</Text>{serverName && <Text numberOfLines={1} style={[nativeType({size: 12, weight: 400, tracking: 0, lineHeight: 'normal'}, theme.textScale), {marginTop: 1, color: theme.colors.textTertiary}]}>{serverName}</Text>}</View> : <Text numberOfLines={1} style={[theme.type.captionStrong, {fontSize: 10.5, lineHeight: 13.5, color: theme.colors.textTertiary}]}>{defaultI18n.t('web.shell.profile')}</Text>}
          {expanded && <Icon name="i-updown" size={18} color={theme.colors.textTertiary}/>}
        </Focusable>
      </View>
    </View>
  </View>;
}
