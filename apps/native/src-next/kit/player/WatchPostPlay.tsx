import React from 'react';
import {PlayerEndCard} from './PlayerEndCard';
import {usePostPlay, type NativeWatchPostPlayProps} from '../../pages/shared/player/usePostPlay';

/** tv-player 7 / phone-player 6: the end of a title with nothing queued after it, drawn by the kit's end card. */
export function NativeWatchPostPlay(props: NativeWatchPostPlayProps) {
  return <PlayerEndCard {...usePostPlay(props)}/>;
}
