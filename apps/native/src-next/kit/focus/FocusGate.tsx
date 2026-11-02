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
 *
 * W-14 reopened: the gate only exists for focus coming FROM the other side. While focus is in the
 * region itself, the gate takes no focus (a blocked guide is no D-pad candidate); otherwise Right
 * from the region met the gate first and was handed straight back, never reaching the rows.
 */
export function FocusGate({regionId, style, testID}: {regionId: string; style?: StyleProp<ViewStyle>; testID?: string}) {
  const {isTV} = useTheme(), registry = useFocusRegistry(), guide = useRef<Guide>(null);
  const subscribe = useCallback((listener: () => void) => registry.subscribeTarget(undefined, [regionId], listener), [registry, regionId]);
  const getSnapshot = useCallback(() => registry.targetSnapshot(undefined, [regionId]), [registry, regionId]);
  const revision = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  // Whether focus is in the region the gate leads to: then it must not stand in the way out.
  const inside = useSyncExternalStore(registry.subscribe, () => registry.capture()?.regionId === regionId, () => false);
  useLayoutEffect(() => {
    if (inside) return;
    // A guide drops its destinations whenever it stops taking focus, so they are sent again each time it is re-enabled.
    const host = registry.host(regionId) as object | undefined;
    const handle = host ? findNodeHandle(host as never) : null;
    guide.current?.setDestinations?.(handle == null ? [] : [handle]);
  }, [inside, registry, regionId, revision]);
  return isTV ? <TVFocusGuideView ref={guide as never} focusable={!inside} style={style} testID={testID ?? `portico.gate.${encodeURIComponent(regionId)}`}/> : null;
}
