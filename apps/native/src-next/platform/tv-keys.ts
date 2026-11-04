import {useEffect, useMemo, useRef} from 'react';
import {useTVEventHandler} from 'react-native';
import {registerBack} from '../kit/navigation/back';
import {createTVKeyAdapter, type TVKeyEvent} from './tv-key-adapter';
import {tvKeySource} from './tv-key-source';

export function useTVKeyEvent(handler: (event: TVKeyEvent) => void): void {
  const adapter = useMemo(() => createTVKeyAdapter({source: tvKeySource}), []);
  useEffect(() => () => adapter.reset(), [adapter]);
  useTVEventHandler(raw => {const event = adapter.read(raw); if (event) handler(event);});
}

/** Whether anything at all came from the remote or Back, for a caller that only needs to know the person acted (it
 *  reads nothing and never consumes). The raw TV events carry what the key adapter drops (tvOS swipes and pans,
 *  channel and page keys); Android's Back never reaches them, so it is observed in back.ts's `observer` scope, which
 *  sees every Back before any other scope and never consumes it, so the page's own Back handlers still run. */
export function useTVRemoteActivity(enabled: boolean, handler: (raw: {eventType?: string}) => void): void {
  const latest = useRef(handler); latest.current = handler;
  useTVEventHandler(raw => {if (enabled && raw) latest.current(raw);});
  useEffect(() => {
    if (!enabled) return;
    return registerBack(() => {latest.current({eventType: 'back'}); return false;}, {scope: 'observer'});
  }, [enabled]);
}
