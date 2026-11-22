import React from 'react';
import {WatchHostView} from '../../../kit/player/WatchHostView';
// Named through pages/shared so the screen harness loads the hook for real when a test loads this page.
import {useWatchHost, type WatchHostProps} from '../../../pages/shared/player/useWatchHost';

export type {NativeWatchHostBindings, NativeWatchHostContext, WatchHostProps} from '../../../pages/shared/player/useWatchHost';

/** The player's watch page over the shell: the measured picture slot and every pane, drawn while the player is expanded
 * and its playback has a fence and a surface. */
export function WatchHostPage(props: WatchHostProps) {
  const host = useWatchHost(props);
  return host ? <WatchHostView {...props} {...host}/> : null;
}
