import React from 'react';
import {WatchChromeView} from '../../../kit/player/WatchChromeView';
// Named through pages/shared so the screen harness loads the hook for real when a test loads this page.
import {useWatchChrome, type NativeWatchChromeProps} from '../../../pages/shared/player/useWatchChrome';

export type {NativeWatchChromeProps} from '../../../pages/shared/player/useWatchChrome';

/** The player's controls over the shell's one retained surface. This page owns no video view. */
export function WatchChromePage(props: NativeWatchChromeProps) {
  return <WatchChromeView {...useWatchChrome(props)}/>;
}
