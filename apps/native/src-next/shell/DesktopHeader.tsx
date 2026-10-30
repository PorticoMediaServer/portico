import {headerWordmark} from '../kit/shell/brand';
import React, {useRef, useState} from 'react';
import {Image, Text, View} from 'react-native';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {defaultI18n} from '../../../../packages/i18n/src';
import {Button, Focusable} from '../kit/controls';
import {Menu, type MenuEntry} from '../kit/overlays';
import {ProfileAvatar} from '../kit/shell';
import {useTheme} from '../kit/theme';
import {BlurView} from '../platform/native-blur';
import type {NativeShellSnapshot} from './runtime';
import type {ShellDestination} from './model';

/** Architecture ≥1024 uses the drawn desktop navigation with real library names. */
export function DesktopHeader({snapshot, avatarRef, scrolled, onNavigate, onProfile, activity}: {
  snapshot: NativeShellSnapshot; avatarRef: React.RefObject<View | null>; scrolled: boolean;
  onNavigate: (destination: ShellDestination) => void; onProfile: () => void; activity?: React.ReactNode;
}) {
  const theme = useTheme(), spec = theme.spec.navigation.top, overflowRef = useRef<View>(null), [overflow, setOverflow] = useState(false);
  const libraries = snapshot.libraries.slice(0, 6), remaining = snapshot.libraries.slice(6), destination = snapshot.destination;
  const notifications = snapshot.account.actions.find(action => action.id === 'notifications');
  const link = (id: string, label: string, next: ShellDestination) => {
    const selected = next.kind === destination.kind && (next.kind !== 'library' || destination.kind === 'library' && next.libraryId === destination.libraryId);
    return <Focusable key={id} accessibilityRole="button" accessibilityLabel={label} accessibilityState={{selected}} onPress={() => onNavigate(next)} focusRadius={6}
      style={{paddingVertical: spec.linkPaddingY, maxWidth: 144, flexShrink: 1}}>
      <Text numberOfLines={1} style={[theme.type.nav, {color: selected ? theme.colors.text : theme.colors.textSecondary}]}>{label}</Text>
      {selected && <View pointerEvents="none" style={{position: 'absolute', left: 0, right: 0, bottom: -2, height: spec.underlineHeight, borderRadius: 2, backgroundColor: theme.colors.accent}}/>}
    </Focusable>;
  };
  const menu: MenuEntry[] = remaining.map(library => ({id: library.id, label: library.name, onSelect: () => onNavigate({kind: 'library', libraryId: library.id})}));
  return <View style={{position: 'absolute', left: 0, right: 0, top: 0, height: spec.height, zIndex: theme.layers.navigation, paddingHorizontal: theme.gutter, flexDirection: 'row', alignItems: 'center', gap: spec.gap,
    backgroundColor: scrolled ? `${theme.colors.ink}E0` : undefined}}>
    {scrolled ? <BlurView pointerEvents="none" style={{position: 'absolute', inset: 0}} blurType="dark" blurAmount={spec.solidBlur} reducedTransparencyFallbackColor={theme.colors.ink}/>
      : <Svg pointerEvents="none" accessible={false} width="100%" height="100%" style={{position: 'absolute', inset: 0}}><Defs><LinearGradient id="native-desktop-header" x1="0" y1="0" x2="0" y2="1"><Stop offset="0" stopColor={theme.colors.ink} stopOpacity={spec.overArtAlpha}/><Stop offset="1" stopColor={theme.colors.ink} stopOpacity={0}/></LinearGradient></Defs><Rect width="100%" height="100%" fill="url(#native-desktop-header)"/></Svg>}
    <Image accessible accessibilityLabel={defaultI18n.t('next.platform.appName')} source={require('../../assets/wordmark.png')} resizeMode="contain" style={{...headerWordmark.desktop, flexShrink: 0}}/>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: spec.linkGap, flex: 1, minWidth: 0}}>
      {link('home', defaultI18n.t('web.shell.home'), {kind: 'home'})}
      {libraries.map(library => link(library.id, library.name, {kind: 'library', libraryId: library.id}))}
      {remaining.length > 0 && <Button ref={overflowRef} variant="text" size="compact" onPress={() => setOverflow(true)} accessibilityState={{expanded: overflow}}>{defaultI18n.t('next.controls.more')}</Button>}
      {snapshot.hasLiveTV && link('live', defaultI18n.t('search.chip.liveTV'), {kind: 'live'})}
    </View>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: spec.toolsGap}}>
      <Button variant="icon" icon="i-search" iconOnly size="compact" accessibilityLabel={defaultI18n.t('web.shell.search')} onPress={() => onNavigate({kind: 'search'})}/>
      <Button variant="icon" icon="i-bookmark" iconOnly size="compact" accessibilityLabel={defaultI18n.t('saved.title')} onPress={() => onNavigate({kind: 'saved'})}/>
      {notifications && <Button variant="icon" icon="i-bell" iconOnly size="compact" accessibilityLabel={notifications.label} onPress={notifications.onPress}/>}
      {snapshot.account.viewerRole === 'owner' && activity}
      <ProfileAvatar ref={avatarRef} profile={snapshot.profile} size={spec.avatarSize} unread={snapshot.unreadNotifications} onPress={onProfile} style={{marginLeft: spec.avatarMarginLeft}}/>
    </View>
    <Menu open={overflow} onOpenChange={setOverflow} title={defaultI18n.t('pins.libraries')} anchorRef={overflowRef} presentation="anchored" items={menu}/>
  </View>;
}
