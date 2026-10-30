import {useEffect, useMemo} from 'react';
import {useTVEventHandler} from 'react-native';
import {createTVKeyAdapter, type TVKeyEvent} from './tv-key-adapter';
import {tvKeySource} from './tv-key-source';

export function useTVKeyEvent(handler: (event: TVKeyEvent) => void): void {
  const adapter = useMemo(() => createTVKeyAdapter({source: tvKeySource}), []);
  useEffect(() => () => adapter.reset(), [adapter]);
  useTVEventHandler(raw => {const event = adapter.read(raw); if (event) handler(event);});
}
