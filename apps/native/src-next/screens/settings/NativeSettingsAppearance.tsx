import React, {useCallback, useEffect, useState, useSyncExternalStore, type ReactNode} from 'react';
import {AccessibilityInfo} from 'react-native';
import type {NativeShellRuntime} from '../../shell/runtime';
import {ThemeProvider, textScaleFor, useTheme} from '../../kit/theme';

type Source = Pick<NativeShellRuntime, 'subscribe' | 'reportError'> & Readonly<{appearanceMotion?: 'follow' | 'reduce'; appearanceTextSize?: 'standard' | 'large'}>;

/** Existing theme inputs only: no new native View, layout mode or text scale.
 * An explicit specimen scale wins; otherwise the confirmed TV text size maps
 * once through the kit's published 1.0/1.2 table (ruling R46-TV-TEXT-SIZE). */
export function NativeSettingsAppearance({source, children}: {source: Source; children: ReactNode}) {
  const inherited = useTheme();
  const motion = useSyncExternalStore(source.subscribe, useCallback(() => source.appearanceMotion, [source]));
  const textSize = useSyncExternalStore(source.subscribe, useCallback(() => source.appearanceTextSize, [source]));
  const [systemReduced, setSystemReduced] = useState<boolean>();
  useEffect(() => {
    if (typeof AccessibilityInfo?.isReduceMotionEnabled !== 'function' || typeof AccessibilityInfo.addEventListener !== 'function') return;
    let live = true, revision = 0;
    const subscription = AccessibilityInfo.addEventListener('reduceMotionChanged', value => {
      revision++; if (live) setSystemReduced(value);
    });
    const failed = (error: unknown) => {if (live) source.reportError(error);};
    try {
      void AccessibilityInfo.isReduceMotionEnabled().then(value => {if (live && revision === 0) setSystemReduced(value);}, failed);
    } catch (error) {failed(error);}
    return () => {live = false; subscription.remove();};
  }, [source]);
  const scale = inherited.textScale !== 1 ? inherited.textScale : textScaleFor(textSize, inherited.isTV);
  return <ThemeProvider width={inherited.width} textScale={scale}
    reduceMotion={inherited.reduceMotion || systemReduced === true || motion === 'reduce'}>{children}</ThemeProvider>;
}
