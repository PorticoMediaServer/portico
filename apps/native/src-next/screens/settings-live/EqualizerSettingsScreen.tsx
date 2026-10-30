import React, {useCallback} from 'react';
import {View} from 'react-native';
import type {NativeShellRuntime} from '../../shell/runtime';
import {nativeEqualizer} from '../../platform/playback/equalizer';
import {NativeWatchEqualizer, type NativeEqualizerCopy} from '../watch/Equalizer';
import {useTheme} from '../../kit/theme';
import {useScreenBack} from './Screen';
import {StandardBackdrop} from '../../kit/artwork';

/** Settings and playback edit the same persistent native device record. */
export function NativeEqualizerSettingsScreen({runtime, onBack}: {runtime: NativeShellRuntime; onBack: () => void}) {
  const theme = useTheme(), t = runtime.text.t;
  const back = useCallback(() => {onBack(); return true;}, [onBack]);
  useScreenBack(runtime, back, theme.isTV);
  const copy: NativeEqualizerCopy = {
    title: t('next.media.eq.title'), back: t('action.back'), thisDevice: t('settings.thisDevice'),
    bands: t('next.media.eq.bands'), preamp: t('next.media.eq.preamp'), reset: t('next.media.eq.reset'),
    musicAndAudiobooks: t('next.saved.musicAndAudiobooks'), notBitPerfect: t('next.media.eq.help'),
    gain: (value, withUnit) => withUnit ? t('next.media.eq.db', {value}) : String(value),
    frequency: (index, accessible) => {
      const frequency = [31, 62, 125, 250, 500, 1000, 2000, 4000, 8000, 16000][index];
      return t(accessible || frequency < 1000 ? 'next.media.eq.hz' : 'next.media.eq.khz', {frequency: accessible || frequency < 1000 ? frequency : frequency / 1000});
    },
  };
  return <View style={{flex: 1}}><StandardBackdrop/>
    <NativeWatchEqualizer store={nativeEqualizer} copy={copy} onBack={back} reportError={runtime.reportError}/>
  </View>;
}
