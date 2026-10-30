import React, {useCallback, useEffect, useRef} from 'react';
import {View} from 'react-native';
import {heroSpec} from '@portico/design';
import Animated, {useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import {useTheme} from '../theme';
import {useMotion} from '../useMotion';
import {HeroSurface, type HeroProps} from './Hero';
import {useFocusPreviewDebounce} from './PreviewDebounce';
import type {HeroCandidate} from './types';

export type FocusPreviewHeroProps<T extends HeroCandidate> = Omit<HeroProps<T>, 'candidates' | 'activeIndex' | 'onIndexChange' | 'heroFocused' | 'renderActions'> & {
  focusedEntry?: T;
  repeating?: boolean;
  debounceMs?: number;
  onPreviewError?: (error: unknown, entry: T) => void;
  renderHint: (entry: T) => React.ReactNode;
};

/** A bounded focused record supplied by the consumer; no library read or Home composition. */
export function FocusPreviewHero<T extends HeroCandidate>({focusedEntry, repeating = false, debounceMs = 250, height, style, testID, renderHint, onPreviewError, ...props}: FocusPreviewHeroProps<T>) {
  const theme = useTheme(), motion = useMotion(), covered = useSharedValue(0);
  const coverStyle = useAnimatedStyle(() => ({opacity: covered.value}));
  const admitted = useRef(false);
  useEffect(() => {
    if (admitted.current) covered.value = withTiming(1, motion.timing('slow'));
  }, [covered, motion]);
  const onReady = props.onArtworkReady;
  const ready = useCallback((entry: T) => {
    // Until the first real foreground is decoded the prior spotlight remains
    // visible, including during the250ms dwell and failed preparations.
    admitted.current = true;
    covered.value = withTiming(1, motion.timing('slow'));
    onReady?.(entry);
  }, [covered, motion, onReady]);
  const settled = useFocusPreviewDebounce(focusedEntry, repeating, debounceMs);
  const staged = focusedEntry && settled?.id === focusedEntry.id ? focusedEntry : settled;
  return <View testID={testID} style={[{height: height ?? heroSpec.homeHeight.tv, position: 'relative'}, style]}>
    <Animated.View pointerEvents="none" style={[{position: 'absolute', inset: 0, backgroundColor: theme.colors.ink}, coverStyle]}/>
    <HeroSurface {...props} onArtworkReady={ready} entry={staged} variant="tv" copyTop={props.copyTop ?? 150} renderActions={renderHint} fade="slow" onArtworkError={onPreviewError} />
  </View>;
}
