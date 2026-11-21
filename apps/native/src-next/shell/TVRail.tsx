import {headerWordmark} from '../kit/shell/brand';
import {BrandSymbol} from '../kit/shell/BrandSymbol';
import React, {useCallback, useMemo, useRef, useSyncExternalStore} from 'react';
import {motionRules} from '@portico/design';
import {findNodeHandle, Image, Platform, ScrollView, Text, View} from 'react-native';
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
import {ListEdgeFade, useListEdges, useWholeItems} from '../kit/layout/ListEdgeFade';
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
  // Without a remembered element: Home's spotlight on Home; elsewhere the platform's own search to the right.
  const returnRegion = returnTo ? returnEnabled ? returnTo.regionId : undefined : destination.kind === 'home' ? ids.spotlight : undefined;
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
  // Apple TV enters the closed rail by geometry (react-native-tvos places a nextFocusLeft guide inside a focused card that has
  // grown, so it is never used): Left lands on whichever item is level with the card. Entering from the page goes to the
  // current page's item, as Android TV's nextFocusLeft does.
  const opened = useRef(false);
  const focusChanged = useCallback((owns: boolean) => {
    // A frame later and only if focus is still in the rail: focus that only passed through it (dropped there while a page
    // opened, then asked onto that page's start) must not be pulled back to the rail.
    if (owns && !opened.current && Platform.OS === 'ios') requestAnimationFrame(() => {if (registry.capture()?.regionId === ids.rail) registry.focusRegion(ids.rail);});
    opened.current = owns; changeRef.current(owns);
  }, [registry, ids.rail]);
  React.useEffect(() => {if (!motion.transforms) expanded.value = open ? 1 : 0;}, [expanded, motion.transforms, open]);
  const panel = useAnimatedStyle(() => ({transform: [{translateX: (expanded.value - 1) * (spec.openWidth - spec.width)}]}));
  const counter = useAnimatedStyle(() => ({transform: [{translateX: (1 - expanded.value) * (spec.openWidth - spec.width)}]}));
  const labels = useAnimatedStyle(() => ({opacity: labelOpacity.value, transform: motion.transforms ? [{translateX: -8 * (1 - labelOpacity.value)}] : []}));
  // Search and Home are pinned at the top, Live TV and Saved above Settings: destinations never need a scroll to find.
  // Only the libraries scroll, between them, fading at whichever end has more (lead ruling, 16 Nov).
  const pinnedTop: RailEntry[] = [
    {key: 'search', icon: 'i-search', label: defaultI18n.t('web.shell.search'), destination: {kind: 'search'}},
    {key: 'home', icon: 'i-home', label: defaultI18n.t('web.shell.home'), destination: {kind: 'home'}},
  ];
  const libraryEntries: RailEntry[] = visibleLibraries(libraries, true).map(library => ({key: 'library:' + library.id, label: library.name, icon: libraryIcon(library.kind), destination: {kind: 'library' as const, libraryId: library.id}}));
  const pinnedBottom: RailEntry[] = [
    ...(hasLiveTV ? [{key: 'live', icon: 'i-live' as const, label: defaultI18n.t('search.chip.liveTV'), destination: {kind: 'live' as const}}] : []),
    {key: 'saved', icon: 'i-bookmark', label: defaultI18n.t('saved.title'), destination: {kind: 'saved'}},
  ];
  const entries = [...pinnedTop, ...libraryEntries, ...pinnedBottom], {edges, scroll} = useListEdges();
  const whole = useWholeItems('y', 'blank', libraryEntries.map(entry => entry.key), spec.itemGap, spec.itemGap);
  const item = (entry: RailEntry, onFocus?: () => void, blank = false) => {
    const selected = sameDestination(entry.destination, destination);
    return <Focusable key={entry.key} {...returnProps} focusId={entry.key} testID={focusTestID(ids.rail, entry.key)} accessibilityRole="button" accessibilityLabel={entry.label} accessibilityState={{selected}} onPress={() => onNavigate(entry.destination)} onFocus={onFocus} focusRadius={spec.itemRadius} focusKind="button"
      style={({focused}) => ({height: spec.itemSize, width: spec.openItemWidth, paddingHorizontal: 17, flexDirection: 'row', alignItems: 'center', gap: spec.labelGap, borderRadius: spec.itemRadius, backgroundColor: focused ? `${theme.colors.text}0F` : undefined})}>
      {({focused}) => <>{selected && !blank && <View pointerEvents="none" style={{position: 'absolute', left: -16, top: 18, bottom: 18, width: 4, borderRadius: 2, backgroundColor: theme.colors.accent}}/>}
        <Icon name={entry.icon} size={30} color={blank && !focused ? 'transparent' : selected || focused ? theme.colors.text : theme.colors.textTertiary}/>
        {/* The mock's 320-wide item fits short names; a long library name shrinks to 80% before it truncates. */}
        <Animated.Text numberOfLines={1} adjustsFontSizeToFit minimumFontScale={.8} style={[theme.type.bodyStrong, {color: blank && !focused ? 'transparent' : selected || focused ? theme.colors.text : theme.colors.textTertiary, flexShrink: 1}, labels]}>{entry.label}</Animated.Text></>}
    </Focusable>;
  };
  return <View pointerEvents="box-none" style={{position: 'absolute', left: 0, top: 0, bottom: 0, width: open ? spec.openWidth : spec.width, zIndex: theme.layers.navigation}}>
    {/* The rail's scrim is the shell's one rail scrim (NativeShell `tvRailScrim`), not drawn here. */}
    <FocusRegion id={ids.rail} entry={ids.destination(destination)} entryPolicy="entry" orientation="vertical"
      order={[...entries.map(entry => entry.key), 'settings', ids.profile]} edges={{right: returnRegion}} onFocusChange={focusChanged}
      accessibilityLabel={defaultI18n.t('next.home.native.mainNavigation')} style={{flex: 1}}>
    <Animated.View style={[{flex: 1, width: spec.openWidth, paddingLeft: spec.insetLeft, paddingTop: spec.paddingTop, paddingBottom: spec.paddingBottom}, panel]}>
      {/* Closed, the column ends at the page's 200 gutter: anything wider (the 512-wide entry list) lies over the page's first
          column, and tvOS refuses focus to what it covers. Open, it takes the panel's width. */}
      {/* Its clip starts left of the items by the current mark's room, so the current page's accent bar (tv.html: 3 px, left
          of the icon) shows on every page with the rail closed, not only open (r7 S6). */}
      <Animated.View style={[{flex: 1}, counter, open ? null : {width: tvRailClosedColumn + spec.markRoom, marginLeft: -spec.markRoom, paddingLeft: spec.markRoom, overflow: 'hidden'}, dimmed && !open ? {opacity: .5} : null]}>
        <View accessible={false} style={{height: 40, marginLeft: 16, marginBottom: 64}}>
          {open ? <Image source={require('../../assets/wordmark.png')} resizeMode="contain" style={{...headerWordmark.tvRail, flexShrink: 0, position: 'absolute', top: (40 - headerWordmark.tvRail.height) / 2}}/> : <BrandSymbol width={28} height={40}/>}
        </View>
        <View style={{gap: spec.itemGap}}>{pinnedTop.map(entry => item(entry))}</View>
        {/* A library name is shown whole or not at all, never cut under the fade (owner, 20 Nov 20:30). */}
        {/* The libraries scroll, so they clip. The list owns the item gaps above and below it (room for the focus ring) and
            glow room at the sides, and reaches no further: past the gaps it would paint under the pinned entries. Where it
            continues, the kit's edge fade says so. */}
        {libraryEntries.length > 0 && <View testID="tv-rail-libraries" style={{flexShrink: 1, marginLeft: -24, marginRight: -tvRailGlow}}>
          <ScrollView {...scroll} ref={whole.scroll.ref} scrollEventThrottle={whole.scroll.scrollEventThrottle} onScroll={event => {scroll.onScroll?.(event); whole.scroll.onScroll(event);}}
            onLayout={event => {scroll.onLayout?.(event); whole.scroll.onLayout?.(event);}} style={{flexGrow: 0, flexShrink: 1}} contentContainerStyle={{gap: spec.itemGap, paddingLeft: 24, paddingRight: tvRailGlow, paddingVertical: spec.itemGap}} showsVerticalScrollIndicator={false}>{libraryEntries.map(entry => <View key={entry.key} {...whole.item(entry.key)}>{item(entry, () => whole.focus(entry.key), whole.cut(entry.key))}</View>)}</ScrollView>
          {/* Closed, the fades cover the icons only: the list's box runs on under the page's first column (its glow room and
              the column's last pixels), and a fade there drew a dark block with a hard foot across a focused card's glow. */}
          {(edges.top || edges.bottom) && <View pointerEvents="none" style={{position: 'absolute', top: 0, bottom: 0, left: 0, ...(open ? {right: tvRailGlow} : {width: tvRailClosedFade})}}>
            {edges.top && <ListEdgeFade testID="tv-rail-fade-top" edge="top" length={spec.itemSize} opacity={0.9}/>}
            {edges.bottom && <ListEdgeFade testID="tv-rail-fade-bottom" edge="bottom" length={spec.itemSize} opacity={0.9}/>}
          </View>}
        </View>}
        <View style={{gap: spec.itemGap, marginTop: libraryEntries.length ? 0 : spec.itemGap}}>{pinnedBottom.map(entry => item(entry))}</View>
        <View style={{flex: 1}}/>
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

/** Closed, the rail's column (from its 72 inset) ends at the page's 200 gutter. */
const tvRailClosedColumn = 200 - 72; // the page gutter less the rail's insetLeft (spec.navigation.tv)
/** Room around the rail list for a focused item's glow. */
const tvRailGlow = 40;
/** Closed, the edge fades' width from the list's left edge: its 24 of glow room, an item's 17 padding and 30 icon, and 8 more. */
const tvRailClosedFade = 24 + 17 + 30 + 8;

