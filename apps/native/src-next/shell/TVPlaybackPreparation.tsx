import React from 'react';
import {StyleSheet, Text, View} from 'react-native';
import {useTheme} from '../kit/theme';
import {StandardBackdrop} from '../kit/artwork';

/**
 * The one line a TV shows while a playback start is still being prepared. The host gives it the
 * already-translated catalogue message (`player.preparing.slowStart`); this view decides nothing
 * about when preparation begins, whether it is still current or when it ends, and adds no control,
 * spinner or countdown of its own.
 *
 * It sits on the standard backdrop (never flat ink, Justin 29 Oct), in the existing body type at `textSecondary`,
 * centred, and wraps to the picture it is given rather than to a fixed canvas width. Pointer
 * passive, and announced as a polite status: `role` carries `status`, where the
 * `AccessibilityRole` union has no such member.
 */
export function TVPlaybackPreparation({message}: {message: string}): React.ReactElement | null {
  const theme = useTheme();
  if (!message) return null;
  return (
    <View pointerEvents="none" accessible role="status" accessibilityLiveRegion="polite"
      style={[StyleSheet.absoluteFill, styles.backdrop, {backgroundColor: theme.colors.ink}]}>
      <StandardBackdrop />
      <Text style={[theme.type.body, styles.line, {color: theme.colors.textSecondary}]}>{message}</Text>
    </View>
  );
}

const styles = StyleSheet.create({
  backdrop: {alignItems: 'center', justifyContent: 'center', paddingHorizontal: '12%'},
  line: {textAlign: 'center', maxWidth: '100%'},
});
