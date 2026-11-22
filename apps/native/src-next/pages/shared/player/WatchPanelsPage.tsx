import React from 'react';
import {WatchPanelsView} from '../../../kit/player/WatchPanelsView';
// Named through pages/shared so the screen harness loads the hook for real when a test loads this page.
import {useWatchPanels, type NativeWatchPanelsProps} from '../../../pages/shared/player/useWatchPanels';

export type {NativeWatchPanelsProps, NativeWatchStyleField, NativeWatchStylePort} from '../../../pages/shared/player/useWatchPanels';

/** The player's panels (Options, Audio, Subtitles, Quality, Speed, Chapters, Episodes, Up next and the report). */
export function WatchPanelsPage(props: NativeWatchPanelsProps) {
  return <WatchPanelsView {...props} {...useWatchPanels(props)}/>;
}
