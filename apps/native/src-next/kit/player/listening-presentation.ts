import React from 'react';
import {ScrollView, Text, View} from 'react-native';
import {GestureDetector} from 'react-native-gesture-handler';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {NativeEqualizerCopy} from '../frames/EqualizerFrame';
import {SignalLine, SignalSheet, TVSignalSheet} from './SignalPath';
import {NowPlayingQueue} from './NowPlayingQueue';
import {NowPlayingLyrics} from './NowPlayingLyrics';
import {ListeningSongActions, ListeningFavoritePaint} from '../../screens/destinations/music/ListeningSongActions';
import {ListeningQueuePaint, ListeningLyricsPaint} from './ListeningPanePaint';
import {AmbientArt, Artwork} from '../artwork';
import {Button, Focusable} from '../controls';
import {Icon} from '../Icon';
import {AudioPlayerFrame} from '../frames/AudioPlayerFrame';
import {ListeningGroup, ListeningSeekPaint} from '../dock/ListeningCoverBridge';
import {ListeningCover, ListeningNoticeLine, ListeningPageBackground, ListeningPanel, ListeningSurface} from './ListeningSurface';
import {AudioPanel, ChapterList, ChapterTicks} from './queue';

const t = defaultI18n.t;

export const equalizerCopy = (): NativeEqualizerCopy => ({title: t('next.media.eq.title'), back: t('action.back'), thisDevice: t('device.thisDevice'),
  bands: t('next.media.eq.bands'), preamp: t('next.media.eq.preamp'), reset: t('next.media.eq.reset'),
  musicAndAudiobooks: t('next.settingsSpec.account.music.title'), notBitPerfect: t('next.media.eq.help'),
  gain: (value, unit) => unit ? t('next.media.eq.db', {value}) : String(value),
  frequency: (index) => {const hz = [31, 62, 125, 250, 500, 1000, 2000, 4000, 8000, 16000][index] ?? 0;
    return hz >= 1000 ? t('next.media.eq.khz', {frequency: hz / 1000}) : t('next.media.eq.hz', {frequency: hz});}});

export const retainedListeningParts = [ListeningSurface, ListeningPanel, ListeningNoticeLine, ListeningPageBackground, ListeningGroup, ListeningSeekPaint,
  ListeningQueuePaint, ListeningLyricsPaint, SignalLine, SignalSheet, TVSignalSheet, ListeningFavoritePaint, AudioPanel, ChapterList,
  ChapterTicks, AudioPlayerFrame, ListeningCover, View, Text, ScrollView, Artwork, AmbientArt, Button, Focusable, Icon, React.Fragment];
export const omittedListeningParts = [GestureDetector, NowPlayingQueue, NowPlayingLyrics, ListeningSongActions];
