import React from 'react';
import {WatchEpisodesView} from '../../../kit/player/WatchEpisodesView';
// Named through pages/shared so the screen harness loads the hook for real when a test loads this page.
import {useWatchEpisodes, type NativeWatchEpisodesProps} from '../../../pages/shared/player/useWatchEpisodes';

export type {NativeWatchEpisodesProps};

/** The episodes inside the player: the TV band, the sideways panel and the upright sheet, or the foldable's following pane. */
export function WatchEpisodesPage(props: NativeWatchEpisodesProps) {
  return <WatchEpisodesView {...useWatchEpisodes(props)}/>;
}
