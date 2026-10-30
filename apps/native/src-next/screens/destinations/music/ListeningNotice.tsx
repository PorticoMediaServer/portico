import React, {useEffect, useState} from 'react';
import {Text, View, useWindowDimensions} from 'react-native';
import {defaultI18n, type MessageId} from '../../../../../../packages/i18n/src';
import {Icon} from '../../../kit/Icon';
import {useTheme, nativeType} from '../../../kit/theme';
import {shellLayout} from '../../../shell/model';

const t = defaultI18n.t;
const VISIBLE_MS = 8000;

/** listening-notices 4/5b/6: the pause you did not make, while Now playing is closed.
 * It is a buttonless status line above the dock on a phone and the TV toast at the top
 * right; it never takes focus and clears itself, a resume or a stop. */
export function NativeListeningNotice({notice, bottomOffset = 0, topOffset = 64, rightOffset = 96, persistent = false}: {
  notice: MessageId; bottomOffset?: number; topOffset?: number; rightOffset?: number;
  /** Expanded Now playing holds the notice until its current playback owner clears it. */
  persistent?: boolean;
}) {
  const theme = useTheme(), tv = theme.isTV, [visible, setVisible] = useState(true);
  const window = useWindowDimensions(), canvas = shellLayout(window.width, window.height, tv);
  useEffect(() => {
    setVisible(true);
    if (persistent) return;
    const timer = setTimeout(() => setVisible(false), VISIBLE_MS);
    return () => clearTimeout(timer);
  }, [notice, persistent]);
  if (!visible) return null;
  const toast = <View pointerEvents="none" accessibilityLiveRegion="polite" accessibilityRole={tv ? 'text' : 'alert'} accessible={tv ? true : undefined} accessibilityLabel={tv ? t(notice) : undefined} testID="music-listening-toast"
    style={tv ? {position: 'absolute', top: topOffset, right: rightOffset, maxWidth: 560, paddingHorizontal: 20, paddingVertical: 12, borderRadius: 14, backgroundColor: 'rgba(7,16,24,.62)'}
      : {position: 'absolute', left: 16, right: 16, bottom: bottomOffset, flexDirection: 'row', justifyContent: 'center'}}>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: tv ? 12 : 8, paddingHorizontal: tv ? 0 : 12, paddingVertical: tv ? 0 : 8,
      borderRadius: 10, backgroundColor: tv ? 'transparent' : 'rgba(7,16,24,.5)'}}>
      <Icon name="i-pause" size={tv ? 28 : 14} color={theme.colors.accentStrong}/>
      <Text numberOfLines={2} style={[nativeType({size: tv ? 24 : 13, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: tv ? theme.colors.textSecondary : theme.colors.textSecondary, flexShrink: 1}]}>{t(notice)}</Text>
    </View>
  </View>;
  return tv ? <View pointerEvents="none" style={{position: 'absolute', left: canvas.left, top: canvas.top, width: canvas.width, height: canvas.height,
    transformOrigin: 'top left', transform: [{scale: canvas.scale}]}}>{toast}</View> : toast;
}
