import React from 'react';
import {Text, View} from 'react-native';
import {clockText} from '../../../../../packages/client-core/src/presentation/kit-model/text';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Button} from '../../kit/controls';
import {useTheme} from '../../kit/theme';
import {shellType} from '../../kit/shell/visual';
import type {NativePlaybackFailure} from './PlaybackFailed';

/** A compact state inside the picture, leaving the player's own exit controls usable. */
export function NativeHandheldPlaybackFailed({failure, retry, differentVersion}: {failure: NativePlaybackFailure; retry?: () => void; differentVersion?: () => void}) {
  const t = defaultI18n.t, theme = useTheme();
  return <View testID="watch-handheld-failed" pointerEvents="box-none" style={{position: 'absolute', inset: 0, justifyContent: 'center', alignItems: 'center'}}>
    <View pointerEvents="none" style={{position: 'absolute', inset: 0, backgroundColor: '#03080c8c'}}/>
    <View testID="watch-handheld-failure" style={{maxWidth: '80%', alignItems: 'center', gap: 4}}>
      <Text accessibilityRole="header" numberOfLines={1} style={[shellType(16, 700), {color: theme.colors.text, textAlign: 'center'}]}>{t('next.states.playbackStopped')}</Text>
      <Text numberOfLines={1} style={[shellType(13, 400), {color: theme.colors.textSecondary, textAlign: 'center'}]}>{t('next.dock.native.stoppedAt', {position: clockText(failure.positionSeconds) ?? ''})}</Text>
      {retry && <Button testID="watch-handheld-retry" size="compact" onPress={retry}>{t('next.states.tryAgain')}</Button>}
      {differentVersion && <Button testID="watch-handheld-version" size="compact" variant="glass" onPress={differentVersion}>{t('next.overlays.differentVersion')}</Button>}
    </View>
  </View>;
}
