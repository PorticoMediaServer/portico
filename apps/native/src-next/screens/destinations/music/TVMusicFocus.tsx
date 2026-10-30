import React, {useEffect, useLayoutEffect, useMemo, useRef, useState} from 'react';
import {BackHandler, findNodeHandle, View} from 'react-native';
import {useTVKeyEvent} from '../../../platform/tv-keys';
import {createTVSeekRepeat} from '../../../platform/tv-seek-repeat';
import {sameFence} from '../../../../../../packages/player-core/src/identity';
import type {DockCommand, DockOccupant} from '../../../kit/dock/types';
import {playbackPosition, seekBounds, seekTo} from '../../../kit/dock/model';
import {Focusable} from '../../../kit/controls';
import {FocusRegion, type FocusRegionProps} from '../../../kit/focus';
import {useTheme} from '../../../kit/theme';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {useDestinationEntryFocus} from '../../destinations/useEntryFocus';

/** These regions only compose the TV screen. Touch keeps its existing views. */
export function MusicFocusRegion({children, ...props}: FocusRegionProps) {
  const theme = useTheme();
  return theme.isTV ? <FocusRegion {...props}>{children}</FocusRegion> : <View style={props.style}>{children}</View>;
}

function Entry({region, visible, child}: {region: string; visible: boolean; child: string}) {
  useDestinationEntryFocus({visible, intent: 'enter', revision: 0}, {regionId: region, childId: child});
  return null;
}

/** F-MUSIC-1: opening starts on Pause. Track changes never request focus again.
 * Back folds this occupant; its parent retains the original playback fence. */
export function TVMusicFocus({id, visible, interactive = true, entry, onClose, children}: {id: string; visible: boolean; interactive?: boolean; entry: string; onClose: () => void; children: React.ReactNode}) {
  const theme = useTheme(), latest = useRef({visible, onClose}); latest.current = {visible, onClose};
  useLayoutEffect(() => {
    if (!theme.isTV) return;
    const back = BackHandler.addEventListener('hardwareBackPress', () => {
      if (!latest.current.visible) return false;
      latest.current.onClose(); return true;
    });
    return () => back.remove();
  }, [theme.isTV]);
  if (!theme.isTV) return <>{children}</>;
  return <FocusRegion id={id} hold enabled={interactive} style={{position: 'absolute', inset: 0}}>
    <Entry region={`${id}.transport`} child={entry} visible={visible && interactive}/>{children}
  </FocusRegion>;
}

/** F-MUSIC-15/16: the actual seek window is a D-pad stop between Pause and
 * Signal path. The adapter still validates every command's captured fence. */
export function TVMusicSeek({occupant, onAction, enabled, testID}: {occupant: DockOccupant; onAction: (command: DockCommand) => void; enabled: boolean; testID: string}) {
  const theme = useTheme(), node = useRef<View>(null), live = useRef(false), focused = useRef(false);
  const [host, setHost] = useState<number>(), latest = useRef({occupant, onAction, enabled}); latest.current = {occupant, onAction, enabled};
  useLayoutEffect(() => {live.current = true; setHost(findNodeHandle(node.current) ?? undefined); return () => {live.current = false; focused.current = false;};}, []);
  const seekPosition = useRef<number | undefined>(undefined), seekFence = useRef(occupant.presentation.fence);
  if (!sameFence(seekFence.current, occupant.presentation.fence)) {seekFence.current = occupant.presentation.fence; seekPosition.current = undefined;}
  const adjust = (direction: -1 | 1, seconds?: number) => {
    const current = latest.current;
    if (!live.current || !current.enabled || !focused.current && seconds !== undefined) return false;
    const step = seconds ?? (direction < 0 ? current.occupant.presentation.skipBackSeconds : current.occupant.presentation.skipForwardSeconds ?? current.occupant.presentation.skipBackSeconds);
    if (!(step > 0) || !Number.isFinite(step)) return false;
    const before = seconds === undefined ? playbackPosition(current.occupant) : seekPosition.current ?? playbackPosition(current.occupant);
    const command = seekTo(current.occupant, before + direction * step);
    if (!command || command.positionSeconds === before) return false;
    if (seconds !== undefined) seekPosition.current = command.positionSeconds;
    current.onAction(command);
  };
  const adjustRef = useRef(adjust); adjustRef.current = adjust;
  const repeat = useMemo(() => createTVSeekRepeat((direction, seconds) => adjustRef.current(direction, seconds)), []);
  const fenceKey = JSON.stringify(occupant.presentation.fence);
  useLayoutEffect(() => {repeat.stop(); seekPosition.current = undefined; return () => repeat.stop();}, [enabled, fenceKey, repeat]);
  useEffect(() => {
    const back = BackHandler.addEventListener('hardwareBackPress', () => {
      repeat.stop(); seekPosition.current = undefined; return false;
    });
    return () => back.remove();
  }, [repeat]);
  useTVKeyEvent(event => {
    if (!enabled || !focused.current) {repeat.stop(); return;}
    if (event.key === 'menu') {repeat.stop(); seekPosition.current = undefined; return;}
    if (event.phase === 'press' && !event.repeat) seekPosition.current = undefined;
    repeat.handle(event);
    if (event.phase === 'long-end') seekPosition.current = undefined;
  });
  const bounds = seekBounds(occupant), position = playbackPosition(occupant), span = bounds ? bounds.end - bounds.start : 0;
  const fraction = bounds && span > 0 ? Math.max(0, Math.min(1, (position - bounds.start) / span)) : 0;
  return <Focusable ref={node} focusId="seek" testID={testID} focusRadius={8} accessibilityRole="adjustable" accessibilityLabel={defaultI18n.t('player.position')}
    disabled={!enabled || !occupant.presentation.capabilities.seek || !(span > 0)}
    accessibilityValue={{min: bounds?.start ?? 0, max: bounds?.end ?? 0, now: bounds ? Math.max(bounds.start, Math.min(bounds.end, position)) : 0}}
    accessibilityActions={[{name: 'increment'}, {name: 'decrement'}]} onAccessibilityAction={event => {
      const direction = event.nativeEvent.actionName === 'increment' ? 1 : event.nativeEvent.actionName === 'decrement' ? -1 : undefined;
      if (direction) {repeat.stop(); seekPosition.current = undefined; adjust(direction);}
    }}
    {...(host ? {nextFocusLeft: host, nextFocusRight: host} : {})} onFocus={() => {focused.current = true;}} onBlur={() => {focused.current = false; repeat.stop(); seekPosition.current = undefined;}}
    style={{height: 28, justifyContent: 'center'}}>
    <View style={{height: 8, borderRadius: 8, backgroundColor: 'rgba(237,245,250,.2)'}}><View style={{width: `${fraction * 100}%`, height: 8, borderRadius: 8, backgroundColor: theme.colors.text}}/></View>
  </Focusable>;
}
