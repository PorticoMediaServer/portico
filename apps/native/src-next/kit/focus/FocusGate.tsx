import React, {useCallback, useLayoutEffect, useRef, useSyncExternalStore} from 'react';
import {findNodeHandle, TVFocusGuideView, type StyleProp, type ViewStyle} from 'react-native';
import {useFocusRegistry} from './FocusRegion';
import {useTheme} from '../theme';

type Guide = React.ElementRef<typeof TVFocusGuideView> & {setDestinations?: (destinations: (number | object)[]) => void};

/**
 * A strip between two parts of a TV screen that hands focus to a region (W-14). Moving across it
 * from anywhere on one side, whichever control or panel focus leaves from, D-pad search meets the
 * strip first and lands in the region's own entry (its remembered child), never on whatever
 * happens to lie further along that side of the screen.
 */
export function FocusGate({regionId, style, testID}: {regionId: string; style?: StyleProp<ViewStyle>; testID?: string}) {
  const {isTV} = useTheme(), registry = useFocusRegistry(), guide = useRef<Guide>(null);
  const subscribe = useCallback((listener: () => void) => registry.subscribeTarget(undefined, [regionId], listener), [registry, regionId]);
  const getSnapshot = useCallback(() => registry.targetSnapshot(undefined, [regionId]), [registry, regionId]);
  const revision = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  useLayoutEffect(() => {
    const host = registry.host(regionId) as object | undefined;
    const handle = host ? findNodeHandle(host as never) : null;
    guide.current?.setDestinations?.(handle == null ? [] : [handle]);
  }, [registry, regionId, revision]);
  return isTV ? <TVFocusGuideView ref={guide as never} style={style} testID={testID ?? `portico.gate.${encodeURIComponent(regionId)}`}/> : null;
}
