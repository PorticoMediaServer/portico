import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {View} from 'react-native';
import Animated, {useAnimatedStyle, useSharedValue, withTiming} from 'react-native-reanimated';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {useTheme} from '../theme';
import {MediaBar} from './MediaBar';
import {NowPlayingCard} from './NowPlayingCard';
import {DockNotice} from './DockNotice';
import {sameDockFence, commandAllowed, commandIsCurrent, currentOccupant, dockBarFrame, expandedDockMetrics} from './model';
import {alpha, dockStyles} from './visual';
import {useDockPresence} from './useDockPresence';
import {useDockClearanceReport} from './DockClearance';
import {useMotion} from '../useMotion';
import type {DockCommand, DockHostProps, DockRect, DockRenderOccupant} from './types';

/** Render an admitted core occupant or a validated remote view, without engine effects. */
export function DockHost({dock, snapshot, presentation, castView, onAction, bottomInset, contentInset, visible = true, tvExpanded, onTVExpandedChange, returnFocusRef, onVideoSlot, morph, takeVideoExit, notice, onNoticeDismiss, style, testID}: DockHostProps) {
  const theme = useTheme(), motion = useMotion(), safe = useSafeAreaInsets(), frame = dockBarFrame(theme.platform, bottomInset, safe.bottom, contentInset);
  const [expanded, setExpanded] = useState(false), [menu, setMenu] = useState(false);
  const readOccupant=useCallback(():DockRenderOccupant|null=>castView?.isCurrent(castView.occupant.playback.fence)?castView.occupant:currentOccupant(dock,dock.getSnapshot(),presentation),[castView,dock,presentation]);
  const occupant = useMemo(() => castView?.isCurrent(castView.occupant.playback.fence)?castView.occupant:currentOccupant(dock,snapshot,presentation), [castView,dock,snapshot,presentation]);
  const wanted = visible && (!theme.isTV || occupant?.mode === 'music' || occupant?.mode === 'audiobook') ? occupant : null;
  const frameNow = useRef(frame); frameNow.current = frame;
  const originalVideoSlot = useRef<{fence: DockCommand['fence']; bounds: DockRect; frame: typeof frame} | undefined>(undefined);
  const takeOriginalVideo = useCallback((fence: import('../../../../../packages/player-core/src').PlaybackFence) => {
    const original = originalVideoSlot.current;
    if (!original || !sameDockFence(original.fence, fence)) return;
    const sameFrame = () => Object.keys(original.frame).every(key => original.frame[key as keyof typeof frame] === frameNow.current[key as keyof typeof frame]);
    if (!sameFrame()) return;
    const paint = takeVideoExit?.(fence, original.bounds);
    return paint ? Object.freeze({...paint, paintCurrent: () => sameFrame() && paint.paintCurrent()}) : undefined;
  }, [takeVideoExit]);
  const presence = useDockPresence(wanted, theme.isTV);
  const reportVideoSlot = useCallback((rect: DockRect, slotCurrent?: () => boolean) => {
    const latest = readOccupant();
    if (visible && !theme.isTV && wanted?.mode === 'video' && presence.slotAdmission?.isCurrent() && latest?.mode === 'video' &&
      sameDockFence(latest.playback.fence, wanted.playback.fence)) {
      originalVideoSlot.current = {fence: wanted.playback.fence, bounds: Object.freeze({...rect}), frame: Object.freeze({...frameNow.current})};
      const originalCurrent = slotCurrent ? () => {
        const current = readOccupant();
        return slotCurrent() && !!current && current.mode === 'video' && sameDockFence(current.playback.fence, wanted.playback.fence)
          && presence.slotAdmission?.isCurrent() === true;
      } : undefined;
      onVideoSlot?.(rect, originalCurrent);
    }
  }, [readOccupant, visible, theme.isTV, presence.slotAdmission, wanted, onVideoSlot]);
  const action = useCallback((command: DockCommand) => {
    const latest = readOccupant();
    if(command.fence.kind==='cast-view'){
      if(castView?.isCurrent(command.fence)&&latest?.playback.fence.kind==='cast-view'&&sameDockFence(command.fence,latest.playback.fence)&&commandAllowed(latest,command.type))onAction(command);
      return;
    }
    const restoring = command.type === 'restore-audio' || command.type === 'restore-video';
    if (commandIsCurrent(dock, command) && (restoring || (latest && commandAllowed(latest, command.type)))) onAction(command);
  }, [readOccupant,castView,dock,onAction]);
  const interactionCurrent = useCallback(() => {
    const latest = readOccupant();
    return visible && !!wanted && !!latest && latest.mode === wanted.mode && sameDockFence(latest.playback.fence, wanted.playback.fence)
      && latest.remote?.id === wanted.remote?.id && latest.remote?.sequence === wanted.remote?.sequence;
  }, [readOccupant,visible,wanted]);
  const setTVExpanded = useCallback((value: boolean) => {setExpanded(value); onTVExpandedChange?.(value);}, [onTVExpandedChange]);
  // The touch page dim fades in on the authored short duration instead of
  // appearing with the dock insertion. TV keeps its own existing contract until
  // the shared TV motion is supplied; the surface, safe areas and focus/input
  // ownership are untouched.
  const dim = useSharedValue(0), dimWanted = !theme.isTV && menu;
  useEffect(() => {dim.value = withTiming(dimWanted ? 1 : 0, motion.timing('standard'));}, [dim, dimWanted, motion]);
  const dimStyle = useAnimatedStyle(() => ({opacity: dim.value}));
  const shown = presence.shown;
  const barHeight = theme.platform === 'web' ? expandedDockMetrics.height : theme.platform === 'tablet' ? 66 : 62;
  useDockClearanceReport(!theme.isTV && shown ? frame.bottom + barHeight : 0);
  if (!shown) return null;
  const tvOpen = tvExpanded ?? expanded;
  return <View pointerEvents="box-none" style={[dockStyles.fill, {zIndex: theme.layers.player}, style]}>
    {theme.isTV
      ? tvOpen && <View pointerEvents="none" style={[dockStyles.fill, {backgroundColor: alpha(theme.colors.ink, .5)}]}/>
      : <Animated.View pointerEvents="none" style={[dockStyles.fill, dimStyle, {backgroundColor: alpha(theme.colors.ink, .5)}]}/>}
    <Animated.View pointerEvents={wanted ? 'box-none' : 'none'} style={[dockStyles.fill, presence.style]}>{theme.isTV ? <NowPlayingCard testID={testID} occupant={shown} onAction={action} visible={!!wanted && visible} expanded={!!wanted && tvOpen} onExpandedChange={setTVExpanded} returnFocusRef={returnFocusRef}
      style={{position: 'absolute', top: 44, right: theme.layout.gutter.tvRight}}/> :
      <MediaBar testID={testID} occupant={shown} onAction={action} visible={!!wanted && visible} onMenuOpenChange={setMenu} onVideoSlot={reportVideoSlot} slotAdmission={presence.slotAdmission} isCurrent={interactionCurrent} morph={morph} takeVideoExit={takeOriginalVideo}
        style={{position: 'absolute', left: frame.left, right: frame.right, bottom: frame.bottom, ...(frame.maxWidth ? {maxWidth: frame.maxWidth} : {})}}/>}</Animated.View>
    {!theme.isTV && notice && onNoticeDismiss && <DockNotice notice={notice} onAction={action} onDismiss={onNoticeDismiss} bottomOffset={frame.bottom + (theme.platform === 'web' ? expandedDockMetrics.height : theme.platform === 'tablet' ? 66 : 62) + 10}/>}
  </View>;
}
