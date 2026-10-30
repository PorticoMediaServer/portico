import {useEffect, useState, useSyncExternalStore} from 'react';
import type {PlaybackFence} from '../../../../../../packages/player-core/src';
import type {NativeListeningReadPort, NativeListeningReadSnapshot} from '../../../platform/playback/types';

const EMPTY: NativeListeningReadSnapshot = Object.freeze({phase: 'retired'});
const empty = () => EMPTY;
const noSubscription = () => () => {};
export type AudiobookRead = (fence: PlaybackFence) => NativeListeningReadPort | undefined;

/** The read lives with the actual expanded page. Outgoing paint only keeps
 * display values; it never owns or revives a chapter reader. */
export function useAudiobookRead(read: AudiobookRead | undefined, fence: PlaybackFence, reportError: (error: unknown) => void) {
  const key = JSON.stringify(fence);
  const [opened, setOpened] = useState<Readonly<{key: string; read: AudiobookRead; reader: NativeListeningReadPort}>>();
  useEffect(() => {
    if (!read) return;
    let reader: NativeListeningReadPort | undefined;
    try {reader = read(fence);} catch (error) {reportError(error); return;}
    if (!reader) return;
    setOpened({key, read, reader});
    void reader.readChapters().catch(error => {if (reader.current()) reportError(error);});
    return () => reader.dispose();
    // Equal fences identify the same admitted output; progress publishes must
    // not restart its original chapter read.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [read, key]);
  const reader = opened?.key === key && opened.read === read ? opened.reader : undefined;
  const snapshot = useSyncExternalStore(reader?.subscribe ?? noSubscription, reader?.getSnapshot ?? empty);
  return {reader, snapshot};
}
