import {headerWordmark} from '../kit/shell/brand';
import React, {useCallback, useMemo, useRef, useSyncExternalStore} from 'react';
import {motionRules} from '@portico/design';
import {findNodeHandle, Image, ScrollView, Text, View} from 'react-native';
import Svg, {Defs, LinearGradient, Rect, Stop, Path} from 'react-native-svg';
import Animated, {useAnimatedStyle, useSharedValue, withDelay, withTiming} from 'react-native-reanimated';
import {defaultI18n} from '../../../../packages/i18n/src';
import type {Library} from '../../../../packages/client-core/src';
import {Focusable} from '../kit/controls';
import {FocusRegion, focusTestID, useFocusRegistry, type FocusAddress, type FocusHost} from '../kit/focus';
import {sameDestination, tvFocusIds} from './tv-focus';
import {ProfileFace, type ProfileOption} from '../kit/artwork';
import {Icon, type IconName} from '../kit/Icon';
import {useTheme} from '../kit/theme';
import {useMotion} from '../kit/useMotion';
import {libraryIcon, visibleLibraries, type ShellAuthority, type ShellDestination} from './model';

type RailEntry = {key: string; label: string; icon: IconName; destination: ShellDestination};
export function TVRail({libraries, hasLiveTV, destination, profile, unread = false, onNavigate, onProfile, profileRef, onExpandedChange, authority, returnTo, dimmed}: {
  authority: ShellAuthority; returnTo?: FocusAddress; libraries: readonly Library[]; hasLiveTV: boolean; destination: ShellDestination; profile: ProfileOption;
  /** The authorised Inbox unread flag already used by the phone and desktop rails. */
  unread?: boolean;
  onNavigate: (destination: ShellDestination) => void; onProfile: () => void;
  profileRef: React.RefObject<View | null>; onExpandedChange: (expanded: boolean) => void; dimmed?: boolean;
}) {
  const theme = useTheme(), motion = useMotion(), spec = theme.spec.navigation.tv;
  const registry = useFocusRegistry();
  const ids = tvFocusIds(authority);
  const returnRegionId = returnTo?.regionId, returnChildId = returnTo?.childId;
  const getReturnSnapshot = useMemo(() => {
    let previous: Readonly<{enabled: boolean; host?: FocusHost}> | undefined;
    return () => {
      const enabled = returnRegionId === undefined || registry.config(returnRegionId)?.enabled !== false;
      const host = enabled && returnRegionId !== undefined && returnChildId !== undefined ? registry.child({regionId: returnRegionId, childId: returnChildId}) : undefined;
      if (previous && previous.enabled === enabled && previous.host === host) return previous;
      return previous = {enabled, host};
    };
  }, [registry, returnChildId, returnRegionId]);
  const subscribeReturn = useCallback((listener: () => void) => {
    if (returnRegionId === undefined) return () => {};
    let previous = getReturnSnapshot();
    return registry.subscribeRegion(returnRegionId, () => {
      const next = getReturnSnapshot();
      if (next === previous) return;
      previous = next; listener();
    });
  }, [getReturnSnapshot, registry, returnRegionId]);
  const {enabled: returnEnabled, host: returnHost} = useSyncExternalStore(subscribeReturn, getReturnSnapshot, getReturnSnapshot);
  const returnRegion = returnTo ? returnEnabled ? returnTo.regionId : undefined : ids.spotlight;
  const returnHandle = returnHost ? findNodeHandle(returnHost as View) : null;
  // The native engine handles Right directly, including the remembered child.
  // Without its live host, keep the region guide's normal memory/fallback path.
  const returnProps = returnHandle == null ? {} : {nextFocusRight: returnHandle};
  const expanded = useSharedValue(0), labelOpacity = useSharedValue(0), [open, setOpen] = React.useState(false);
  const change = (value: boolean) => {
    setOpen(value); onExpandedChange(value);
    // motion.html 4.2: panel/page share standard/out; labels use token delay.
    expanded.value = motion.transforms ? withTiming(value ? 1 : 0, motion.timing('standard')) : value ? 1 : 0;
    labelOpacity.value = value ? withDelay(motion.transforms ? motionRules.staggerMs[0] : 0, withTiming(1, motion.timing('standard'))) : withTiming(0, motion.timing('quick'));
  };
  const changeRef = useRef(change); changeRef.current = change;
  const focusChanged = useCallback((owns: boolean) => changeRef.current(owns), []);
  React.useEffect(() => {if (!motion.transforms) expanded.value = open ? 1 : 0;}, [expanded, motion.transforms, open]);
  const panel = useAnimatedStyle(() => ({transform: [{translateX: (expanded.value - 1) * (spec.openWidth - spec.width)}]}));
  const counter = useAnimatedStyle(() => ({transform: [{translateX: (1 - expanded.value) * (spec.openWidth - spec.width)}]}));
  const openShade = useAnimatedStyle(() => ({opacity: expanded.value}));
  const labels = useAnimatedStyle(() => ({opacity: labelOpacity.value, transform: motion.transforms ? [{translateX: -8 * (1 - labelOpacity.value)}] : []}));
  const entries: RailEntry[] = [
    {key: 'search', icon: 'i-search', label: defaultI18n.t('web.shell.search'), destination: {kind: 'search'}},
    {key: 'home', icon: 'i-home', label: defaultI18n.t('web.shell.home'), destination: {kind: 'home'}},
    ...visibleLibraries(libraries, true).map(library => ({key: 'library:' + library.id, label: library.name, icon: libraryIcon(library.kind), destination: {kind: 'library' as const, libraryId: library.id}})),
    ...(hasLiveTV ? [{key: 'live', icon: 'i-live' as const, label: defaultI18n.t('search.chip.liveTV'), destination: {kind: 'live' as const}}] : []),
    {key: 'saved', icon: 'i-bookmark', label: defaultI18n.t('saved.title'), destination: {kind: 'saved'}},
  ];
  const item = (entry: RailEntry) => {
    const selected = sameDestination(entry.destination, destination);
    return <Focusable key={entry.key} {...returnProps} focusId={entry.key} testID={focusTestID(ids.rail, entry.key)} accessibilityRole="button" accessibilityLabel={entry.label} accessibilityState={{selected}} onPress={() => onNavigate(entry.destination)} focusRadius={spec.itemRadius} focusKind="button"
      style={({focused}) => ({height: spec.itemSize, width: spec.openItemWidth, paddingHorizontal: 17, flexDirection: 'row', alignItems: 'center', gap: spec.labelGap, borderRadius: spec.itemRadius, backgroundColor: focused ? `${theme.colors.text}0F` : undefined})}>
      {({focused}) => <>{selected && <View pointerEvents="none" style={{position: 'absolute', left: -16, top: 18, bottom: 18, width: 4, borderRadius: 2, backgroundColor: theme.colors.accent}}/>}
        <Icon name={entry.icon} size={30} color={selected || focused ? theme.colors.text : theme.colors.textTertiary}/>
        {/* The mock's 320-wide item fits short names; a long library name shrinks to 80% before it truncates. */}
        <Animated.Text numberOfLines={1} adjustsFontSizeToFit minimumFontScale={.8} style={[theme.type.bodyStrong, {color: selected || focused ? theme.colors.text : theme.colors.textTertiary, flexShrink: 1}, labels]}>{entry.label}</Animated.Text></>}
    </Focusable>;
  };
  return <View pointerEvents="box-none" style={{position: 'absolute', left: 0, top: 0, bottom: 0, width: open ? spec.openWidth : spec.width, zIndex: theme.layers.navigation}}>
    {/* motion.html 4.2: the panel's darkness follows the same shared value as its slide, so the
        closed and open gradients cross-fade instead of stepping when the state flips. */}
    <Animated.View pointerEvents="none" style={[{position: 'absolute', inset: 0, width: spec.openWidth}, panel]}>
      <Svg width="100%" height="100%" style={{position: 'absolute', inset: 0}}><Defs><LinearGradient id="native-rail" x1="0" y1="0" x2="1" y2="0"><Stop offset="0" stopColor={theme.colors.ink} stopOpacity={.78}/><Stop offset={.45} stopColor={theme.colors.ink} stopOpacity={.42}/><Stop offset="1" stopColor={theme.colors.ink} stopOpacity={0}/></LinearGradient></Defs><Rect width="100%" height="100%" fill="url(#native-rail)"/></Svg>
      <Animated.View style={[{position: 'absolute', inset: 0}, openShade]}>
        <Svg width="100%" height="100%"><Defs><LinearGradient id="native-rail-open" x1="0" y1="0" x2="1" y2="0"><Stop offset="0" stopColor={theme.colors.ink} stopOpacity={.99}/><Stop offset={.66} stopColor={theme.colors.ink} stopOpacity={.96}/><Stop offset="1" stopColor={theme.colors.ink} stopOpacity={0}/></LinearGradient></Defs><Rect width="100%" height="100%" fill="url(#native-rail-open)"/></Svg>
      </Animated.View>
    </Animated.View>
    <FocusRegion id={ids.rail} entry={ids.destination(destination)} entryPolicy="entry" orientation="vertical"
      order={[...entries.map(entry => entry.key), 'settings', ids.profile]} edges={{right: returnRegion}} onFocusChange={focusChanged}
      accessibilityLabel={defaultI18n.t('next.home.native.mainNavigation')} style={{flex: 1}}>
    <Animated.View style={[{flex: 1, width: spec.openWidth, paddingLeft: spec.insetLeft, paddingTop: spec.paddingTop, paddingBottom: spec.paddingBottom}, panel]}>
      <Animated.View style={[{flex: 1}, counter, dimmed && !open ? {opacity: .5} : null]}>
        <View accessible={false} style={{height: 40, marginLeft: 16, marginBottom: 64}}>
          {open ? <Image source={require('../../assets/wordmark.png')} resizeMode="contain" style={{...headerWordmark.tvRail, flexShrink: 0, position: 'absolute', top: (40 - headerWordmark.tvRail.height) / 2}}/> : <RailBrandSymbol/>}
        </View>
        <ScrollView style={{flex: 1, marginLeft: -24}} contentContainerStyle={{gap: spec.itemGap, paddingLeft: 24}} showsVerticalScrollIndicator={false}>{entries.map(item)}</ScrollView>
        <View style={{gap: spec.itemGap, paddingTop: 8}}>
          {item({key: 'settings', label: defaultI18n.t('settings.title'), icon: 'i-settings', destination: {kind: 'settings'}})}
          <Focusable ref={profileRef} {...returnProps} focusId={ids.profile} testID={focusTestID(ids.rail, ids.profile)} accessibilityRole="button" accessibilityLabel={defaultI18n.t('next.home.native.profileAndServer')} onPress={onProfile} focusRadius={14}
            style={{height: 76, width: spec.openItemWidth, paddingHorizontal: 12, flexDirection: 'row', alignItems: 'center', gap: 22}}>
            <View style={{width: 40, height: 40}}>
              <ProfileFace {...profile} size={40} scaleOnFocus={false}/>
              {unread && <View pointerEvents="none" style={{position: 'absolute', top: 0, right: 0, width: 7, height: 7, borderRadius: 4,
                backgroundColor: theme.colors.accentStrong, borderWidth: 2, borderColor: theme.colors.ink}}/>}
            </View><Animated.View style={[{flex: 1}, labels]}><Text numberOfLines={1} style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{profile.name}</Text><Text style={[theme.type.caption, {color: theme.colors.textTertiary, marginTop: 2}]}>{defaultI18n.t('next.home.native.profileAndServer')}</Text></Animated.View>
          </Focusable>
        </View>
      </Animated.View>
    </Animated.View></FocusRegion>
  </View>;
}

/** The canonical brandSymbol asset paths, using the elements supplied by the Metro SVG adapter. */
function RailBrandSymbol() {
  return <Svg width={28} height={40} viewBox="206 184 304 405" accessible={false}>
    <Path d="M228.0 567.0V206.0H354.3530578613281Q395.70135498046875 206.0 425.38002014160156 221.2514419555664Q455.0586853027344 236.5028839111328 471.0293426513672 265.1856155395508Q487.0 293.86834716796875 487.0 333.6389465332031Q487.0 372.9089660644531 471.0293426513672 401.5841369628906Q455.0586853027344 430.2593078613281 425.38002014160156 445.51832580566406Q395.70135498046875 460.77734375 354.3530578613281 460.77734375H324.0V408.46429443359375H352.413818359375Q392.6810607910156 408.46429443359375 413.2369079589844 388.8592529296875Q433.7927551269531 369.25421142578125 433.7927551269531 333.6389465332031Q433.7927551269531 297.52313232421875 413.2369079589844 277.91807556152344Q392.6810607910156 258.3130187988281 352.413818359375 258.3130187988281H280.7454833984375V408.46429443359375H281.0V460.77734375H280.7454833984375V567.0Z" fill="#FFFFFF"/>
    <Path d="M324 298 L324 369 Q324 374 329 371 L392 338 Q398 333 392 328 L329 296 Q324 293 324 298 Z" fill="#FFFFFF"/>
  </Svg>;
}
