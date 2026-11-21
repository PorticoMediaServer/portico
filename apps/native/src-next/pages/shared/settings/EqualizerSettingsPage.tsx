import React, {useCallback} from 'react';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {EqualizerFrame, type NativeEqualizerCopy} from '../../../kit/frames/EqualizerFrame';
import {useEqualizer} from '../player/useEqualizer';
import {useTheme} from '../../../kit/theme';
import {useScreenBack} from '../../../screens/settings-live/Screen';

/** Settings and playback edit the same persistent native device record. */
export function EqualizerSettingsPage({runtime, onBack}: {runtime: NativeShellRuntime; onBack: () => void}) {
  const theme = useTheme(), t = runtime.text.t;
  const back = useCallback(() => {onBack(); return true;}, [onBack]);
  useScreenBack(runtime, back, theme.isTV);
  const model = useEqualizer({reportError: runtime.reportError});
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
  return <EqualizerFrame {...model} copy={copy} onBack={back}/>;
}
