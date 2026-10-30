import {useCallback, useEffect, useRef, useState} from 'react';
import {useTVKeyEvent} from '../../platform/tv-keys';
import Animated, {cancelAnimation, scrollTo, useAnimatedReaction, useAnimatedRef, useAnimatedScrollHandler, useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import {useTheme} from '../theme';
import {useMotion} from '../useMotion';
import {anchoredOffset} from './model';

/** Native focus search chooses the target. This hook animates only its containing scroll. */
export function useFocusAnchor(horizontal: boolean, ownsFocus = true, alignment: 'visible' | 'start' = 'visible') {
  const {isTV} = useTheme(), motion = useMotion();
  const scrollRef = useAnimatedRef<Animated.ScrollView>();
  const offset = useSharedValue(0), target = useSharedValue(0), driving = useSharedValue(false), requests = useSharedValue(0);
  const opacity = useSharedValue(1);
  const scrollStyle = useAnimatedStyle(() => ({opacity: opacity.value}));
  const [repeating, setRepeating] = useState(false);
  // Native repeat and focus notifications may arrive before React commits.
  // The timing decision must follow that stroke, not the previous render.
  const repeatStroke = useRef(false), destination = useRef(0);
  const last = useRef({key: '', at: 0});
  const release = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useTVKeyEvent(event => {
    if (!isTV || !ownsFocus || !(horizontal ? ['left', 'right'] : ['up', 'down']).includes(event.key)) return;
    if (release.current) clearTimeout(release.current);
    const ended = event.phase === 'long-end';
    const now = Date.now(), repeat = !ended && (event.repeat || event.phase === 'long-begin' || last.current.key === event.key && now - last.current.at < 180);
    last.current = ended ? {key: '', at: 0} : {key: event.key, at: now};
    repeatStroke.current = repeat;
    setRepeating(repeat);
    release.current = ended ? undefined : setTimeout(() => {
      repeatStroke.current = false; last.current = {key: '', at: 0}; setRepeating(false);
    }, 180);
  });
  // `requests` makes every move() observable: false→true on `driving` and an unchanged target coalesce within a frame, so a
  // jump to the offset the JS side believes it holds (E-tv-25: a stale 0 while native sits lower) never reached scrollTo.
  useAnimatedReaction(() => ({value: target.value, active: driving.value, requests: requests.value}), next => {
    if (next.active) scrollTo(scrollRef, horizontal ? next.value : 0, horizontal ? 0 : next.value, false);
  }, [horizontal]);
  const onScroll = useAnimatedScrollHandler(event => {offset.value = horizontal ? event.contentOffset.x : event.contentOffset.y;});
  const move = useCallback((next: number, immediate = false) => {
    destination.current = next;
    // Native focus and the destination move immediately; only the newly positioned
    // scrolling surface fades. Direct finger scrolling never enters this path.
    if (motion.reduce && Math.abs(next - offset.value) >= .5) {opacity.value = 0; opacity.value = withTiming(1, motion.timing());}
    requests.value += 1;
    driving.value = false;
    cancelAnimation(target);
    target.value = offset.value;
    driving.value = true;
    target.value = motion.transforms && !immediate ? withTiming(next, motion.timing('slow', repeatStroke.current ? 'linear' : 'inOut')) : next;
  }, [driving, motion, offset, opacity, requests, target]);
  const anchor = useCallback((start: number, size: number, viewport: number, extent: number, leading = 0, trailing = 0) => {
    move(alignment === 'start' ? Math.max(0, Math.min(Math.max(0, extent - viewport), start - leading))
      : anchoredOffset(start, size, viewport, offset.value, extent, leading, trailing));
  }, [alignment, move, offset]);
  const stop = useCallback(() => {driving.value = false; cancelAnimation(target); cancelAnimation(opacity); opacity.value = 1;}, [driving, opacity, target]);
  useEffect(() => {
    if (!ownsFocus) {
      stop(); repeatStroke.current = false; setRepeating(false); last.current = {key: '', at: 0};
      if (release.current) clearTimeout(release.current);
    }
  }, [ownsFocus, stop]);
  useEffect(() => {
    if (!motion.transforms && driving.value) {cancelAnimation(target); target.value = destination.current; opacity.value = 0; opacity.value = withTiming(1, motion.timing());}
    if (motion.transforms) {cancelAnimation(opacity); opacity.value = 1;}
  }, [driving, motion, opacity, target]);
  useEffect(() => () => {stop(); if (release.current) clearTimeout(release.current);}, [stop]);
  return {scrollRef, scrollStyle, offset, onScroll, move, anchor, stop, repeating};
}

export {anchoredOffset as focusAnchorOffset} from './model';
