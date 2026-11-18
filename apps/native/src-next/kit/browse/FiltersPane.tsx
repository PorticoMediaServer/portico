import React, {useEffect, useRef, type ReactNode} from 'react';
import {Pressable, ScrollView, StyleSheet, Text, View} from 'react-native';
import Animated, {useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import {frameSpec} from '@portico/design';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button, Focusable} from '../controls';
import {Icon} from '../Icon';
import {useTheme} from '../theme';
import {useMotion} from '../useMotion';
import {alpha, browseType} from './presentation';

/** The Filters pane's own drawn numbers. tablet.html #4/#5 draw 24 pt padding, a 26 pt title and 50 pt footer buttons over the
 * tablet's status bar and home indicator; the reading pane keeps its older numbers by passing none. */
export type FiltersPaneGeometry = Readonly<{padding: number; titleSize: number; titleTracking: number; headerTop: number; headerBottom: number; footerTop: number; footerBottom: number; gap: number; buttonHeight?: number; safeTop: boolean; safeBottom: boolean}>;
export const libraryFiltersTabletPane: FiltersPaneGeometry = Object.freeze({padding: 24, titleSize: 26, titleTracking: -.03, headerTop: 16, headerBottom: 6, footerTop: 16, footerBottom: 28, gap: 12, buttonHeight: 50, safeTop: true, safeBottom: true});
const readingPane: FiltersPaneGeometry = Object.freeze({padding: 20, titleSize: 22, titleTracking: -.02, headerTop: 18, headerBottom: 12, footerTop: 20, footerBottom: 20, gap: 10, safeTop: false, safeBottom: false});

/** The Filters panel beside or over a grid (tablet 4 and 5; moved from screens/library `LibraryFilters`, which draws it):
 * the title and a round close, the count line, the fields scrolling, and Clear all | Done at the foot. BrowseFrame places it. */
export function FiltersPane({summary, onClose, onClear, onDone, children, geometry, testID}: {
  /** "6 movies match", or "6 of 80 movies". */ summary?: ReactNode; onClose: () => void; onClear: () => void; onDone: () => void;
  children: ReactNode; /** Omitted keeps the reading pane's own numbers. */ geometry?: FiltersPaneGeometry; testID?: string;
}) {
  const theme = useTheme(), insets = useSafeAreaInsets(), g = geometry ?? readingPane;
  const button = g.buttonHeight ? {height: g.buttonHeight} : undefined;
  return <View testID={testID} style={{flex: 1}}>
    <View style={{paddingHorizontal: g.padding, paddingTop: (g.safeTop ? insets.top : 0) + g.headerTop, paddingBottom: g.headerBottom, gap: 4}}>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 8}}>
        <Text accessibilityRole="header" style={[browseType(theme, g.titleSize, 800, g.titleTracking), {color: theme.colors.text, flex: 1}]}>{defaultI18n.t('next.browse.native.filters')}</Text>
        <Focusable testID={testID && `${testID}-close`} focusId={testID && `${testID}-close`} accessibilityRole="button" accessibilityLabel={defaultI18n.t('next.overlays.close')}
          onPress={onClose} style={{width: 44, height: 44, alignItems: 'center', justifyContent: 'center', marginRight: -10}}>
          <Icon name="i-x" size={20} color={theme.colors.textSecondary}/>
        </Focusable>
      </View>
      {summary ? <Text style={[browseType(theme, 14.5, 500), {color: theme.colors.textTertiary}]}>{summary}</Text> : null}
    </View>
    <ScrollView style={{flex: 1}} contentContainerStyle={{paddingHorizontal: g.padding, paddingTop: 8, paddingBottom: 24}}>{children}</ScrollView>
    <View style={{flexDirection: 'row', gap: g.gap, paddingHorizontal: g.padding, paddingTop: g.footerTop, borderTopWidth: 1, borderTopColor: theme.colors.line,
      paddingBottom: g.safeBottom ? Math.max(g.footerBottom, insets.bottom + 8) : g.footerBottom}}>
      <Button testID={testID && `${testID}-clear`} variant="glass" onPress={onClear} style={button}>{defaultI18n.t('next.browse.native.clearAll')}</Button>
      <Button testID={testID && `${testID}-done`} variant="primary" onPress={onDone} style={{flex: 1, ...button}}>{defaultI18n.t('next.controls.done')}</Button>
    </View>
  </View>;
}

/** tablet 4: the pane docked beside the grid, which keeps the rest of the width (moved from screens/library). */
export function FiltersDock({children, testID}: {children: ReactNode; testID?: string}) {
  const theme = useTheme();
  return <View testID={testID} style={{width: frameSpec.browse.filtersWidth.expanded, backgroundColor: theme.colors.raised, borderLeftWidth: 1, borderLeftColor: alpha(theme.colors.text, .07)}}>{children}</View>;
}

/** tablet 5 (moved from screens/library): upright, the pane slides in from the right over the dimmed grid and leaves the
 * same way. It keeps its contents until the exit lands. Timings are the kit's side-panel pair (320 in, 240 out); reduced
 * motion keeps only the fade. */
export function FiltersOver({open, onDismiss, onExited, children}: {open: boolean; onDismiss: () => void; onExited: () => void; children?: ReactNode}) {
  const theme = useTheme(), motion = useMotion(), progress = useSharedValue(0), width = frameSpec.browse.filtersWidth.medium;
  const exited = useRef(onExited); exited.current = onExited;
  useEffect(() => {progress.value = withTiming(open ? 1 : 0, {...motion.timing('slow', open ? 'out' : 'in'), duration: motion.reduce ? motion.duration('standard') : open ? 320 : 240});}, [open, motion, progress]);
  useEffect(() => {if (open) return; const timer = setTimeout(() => exited.current(), motion.reduce ? motion.duration('standard') : 240); return () => clearTimeout(timer);}, [open, motion]);
  const pane = useAnimatedStyle(() => ({transform: motion.transforms ? [{translateX: (1 - progress.value) * width}] : [], opacity: motion.reduce ? progress.value : 1}));
  const scrim = useAnimatedStyle(() => ({opacity: progress.value * .62}));
  return <View style={{position: 'absolute', inset: 0, zIndex: theme.layers.navigation}}>
    <Animated.View style={[StyleSheet.absoluteFill, {backgroundColor: '#03080c'}, scrim]}>
      <Pressable accessible={false} onPress={onDismiss} style={StyleSheet.absoluteFill}/>
    </Animated.View>
    <Animated.View testID="library-filters-over" accessibilityViewIsModal importantForAccessibility="yes"
      style={[{position: 'absolute', top: 0, bottom: 0, right: 0, width, maxWidth: '100%', backgroundColor: theme.colors.raised,
        shadowColor: '#000', shadowOffset: {width: -20, height: 0}, shadowOpacity: .6, shadowRadius: 40}, pane]}>{children}</Animated.View>
  </View>;
}
