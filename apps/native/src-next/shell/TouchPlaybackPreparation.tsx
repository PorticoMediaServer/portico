import React from 'react';
import {StyleSheet, Text, View} from 'react-native';
import {useTheme} from '../kit/theme';
import {Layer} from '../kit/shell/Layer';

/** Only the host's actual server-directed preparation message. Back/cancellation
 * stays with the original start; this passive view owns no timer or command. */
export function TouchPlaybackPreparation({message}: {message: string}) {
  const theme = useTheme();
  if (!message || theme.isTV) return null;
  return <Layer testID="touch-playback-preparation" pointerEvents="none" accessible role="status" accessibilityLiveRegion="polite"
    style={[StyleSheet.absoluteFill, styles.backdrop]}>
    <Text style={[theme.type.body, styles.message, {color: theme.colors.textSecondary}]}>{message}</Text>
  </Layer>;
}
const styles = StyleSheet.create({
  backdrop: {alignItems: 'center', justifyContent: 'center', paddingHorizontal: 24},
  message: {textAlign: 'center', maxWidth: '100%'},
});
