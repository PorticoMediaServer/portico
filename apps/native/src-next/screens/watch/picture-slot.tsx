import React, {useCallback, useEffect, useRef} from 'react';
import {View, useWindowDimensions, type StyleProp, type ViewStyle} from 'react-native';
import {sameFence, type PlaybackFence} from '../../../../../packages/player-core/src';
import {sameAuthority, type ShellAuthority} from '../../shell/model';
import type {NativeExpandedVideoSlot, NativeWatchCurrent, NativeWatchLease, NativeWatchOutletPort} from '../../shell/watch-outlet';

/** Consumer port for Accounts' one watch outlet. No second outlet/store or native bridge. */
export type NativeWatchPictureSlot = NativeExpandedVideoSlot;
export type NativeWatchPictureOutlet = Pick<NativeWatchOutletPort, 'register'>;
export type NativeWatchPictureProps = Readonly<{
  authority: ShellAuthority;
  fence: PlaybackFence;
  outlet: NativeWatchPictureOutlet;
  current: () => NativeWatchCurrent;
  style?: StyleProp<ViewStyle>;
}>;

/** Measure only. The existing NativePlaybackLayer owns Surface/ref/tag/runtime/lease. */
export function NativeWatchPicture({authority, fence, outlet, current, style}: NativeWatchPictureProps) {
  const node = useRef<View>(null), revision = useRef(0), registration = useRef<NativeWatchLease | undefined>(undefined);
  const window = useWindowDimensions();
  const measure = useCallback(() => {
    const view = node.current, owner = registration.current, epoch = ++revision.current;
    if (!view || !owner) return;
    view.measureInWindow((x, y, width, height) => {
      const latest = current();
      if (node.current !== view || registration.current !== owner || epoch !== revision.current || !sameAuthority(latest.authority, authority)
        || !sameFence(latest.fence, fence) || latest.window.width !== window.width || latest.window.height !== window.height
        || ![x, y, width, height, window.width, window.height].every(Number.isFinite) || width <= 0 || height <= 0 || window.width <= 0 || window.height <= 0) return;
      owner.publish({authority, fence, layoutRevision: epoch, window: {width: window.width, height: window.height}, frameInWindow: {x, y, width, height}});
    });
  }, [authority, fence, current, window.width, window.height]);
  useEffect(() => {
    const owner = outlet.register(authority); registration.current = owner;
    return () => {if (registration.current === owner) registration.current = undefined; owner.dispose();};
  }, [outlet, authority]);
  useEffect(() => {registration.current?.clear(); measure();}, [fence, window.width, window.height, measure]);
  return <View ref={node} collapsable={false} testID="watch-picture-slot" pointerEvents="none" accessible={false} onLayout={measure} style={style}/>;
}
