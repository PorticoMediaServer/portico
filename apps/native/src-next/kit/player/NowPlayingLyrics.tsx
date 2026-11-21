import React from 'react';
import {ListeningLyricsPaint} from './ListeningPanePaint';
import {useNowPlayingLyrics, type NowPlayingLyricsProps} from '../../pages/shared/player/useNowPlayingLyrics';
export type {NowPlayingLyricsProps} from '../../pages/shared/player/useNowPlayingLyrics';

export function NowPlayingLyrics(props: NowPlayingLyricsProps) {
  const lyrics = useNowPlayingLyrics(props);
  return lyrics ? <ListeningLyricsPaint data={lyrics.data} onRetry={lyrics.actions.retry} onChoose={lyrics.actions.choose}
    onSeek={lyrics.actions.seek} testID={props.testID}/> : null;
}
