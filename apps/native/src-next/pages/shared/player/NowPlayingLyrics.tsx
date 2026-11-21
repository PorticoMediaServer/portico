import React from 'react';
import {ListeningLyricsPaint} from '../../../kit/player/ListeningPanePaint';
import {useNowPlayingLyrics, type NowPlayingLyricsProps} from './useNowPlayingLyrics';
export type {NowPlayingLyricsProps} from './useNowPlayingLyrics';

export function NowPlayingLyrics(props: NowPlayingLyricsProps) {
  const lyrics = useNowPlayingLyrics(props);
  return lyrics ? <ListeningLyricsPaint data={lyrics.data} onRetry={lyrics.actions.retry} onChoose={lyrics.actions.choose}
    onSeek={lyrics.actions.seek} testID={props.testID}/> : null;
}
