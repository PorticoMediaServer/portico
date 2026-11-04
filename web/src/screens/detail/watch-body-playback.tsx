import {createContext, useContext} from 'react';

/** phone-player 4 / desktop-player 7: on the watch page the title's white button follows the video
 * playing above it, Pause while it plays and Play when it's paused. The watch host supplies this
 * for the title it plays; an ordinary title page has none and keeps its own Play or Resume. */
export type WatchBodyPlayback = Readonly<{playing: boolean; enabled: boolean; toggle(): void}>;
export const WatchBodyPlaybackContext = createContext<WatchBodyPlayback | undefined>(undefined);
export const useWatchBodyPlayback = () => useContext(WatchBodyPlaybackContext);
