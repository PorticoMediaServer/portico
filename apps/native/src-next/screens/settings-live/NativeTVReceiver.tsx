import React, {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {AppState, Platform, Text, View} from 'react-native';
import {ReceiverHost, type ReceiverSnapshot} from '../../../../../packages/client-core/src/remote-playback';
import {installationIdentity} from '../../../../../packages/client-core/src/installation';
import {parseMediaItem} from '../../../../../packages/client-core/src/media-item';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {encodedRoutePath} from '../../../../../packages/contracts/src/routes';
import {routeBase64} from '../../../../../packages/client-core/src/route-identity';
import {nativeRouteCrypto} from '../../platform/server-connections';
import {nativeDeviceSettings} from '../../platform/device-settings';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRuntime} from '../../shell/runtime';
import {Button} from '../../kit/controls';
import {Dialog} from '../../kit/overlays';
import {NativeScreenLifetime} from './lifetime';

const off: ReceiverSnapshot = Object.freeze({phase: 'off'});
const noSubscribe = () => () => {};
const getOff = () => off;

/** The existing grant/handoff protocol, owned by this installed viewer, only in the foreground. */
export function NativeTVReceiver({context, runtime}: {context: NativeShellBootstrapContext; runtime: NativeShellRuntime}) {
  const enabled = useSyncExternalStore(context.subscribePreferences, () => context.current() && nativeDeviceSettings(context).read('receiveFromDevices') === true);
  type Ticket = {context: typeof context; runtime: typeof runtime; enabled: boolean; live: boolean; abort: AbortController};
  const active = useRef<Ticket | undefined>(undefined);
  const [owned, setOwned] = useState<Readonly<{host: ReceiverHost; ticket: Ticket}>>();
  const retire = React.useCallback((ticket: Ticket | undefined) => {if (!ticket) return; ticket.live = false; if (active.current === ticket) active.current = undefined; ticket.abort.abort();}, []);
  // Retained handlers from the previous render cannot borrow the replacement's ticket.
  if (active.current && (active.current.context !== context || active.current.runtime !== runtime || active.current.enabled !== enabled)) retire(active.current);
  useLayoutEffect(() => {
    const ticket: Ticket = {context, runtime, enabled, live: true, abort: new AbortController()};
    active.current = ticket;
    return () => retire(ticket);
  }, [context, runtime, enabled, retire]);
  const ticketCurrent = React.useCallback((ticket: Ticket) => active.current === ticket && ticket.live && !ticket.abort.signal.aborted
    && ticket.context === context && ticket.runtime === runtime && ticket.enabled && context.current(), [context, runtime]);
  const host = owned?.host;
  useEffect(() => {
    const ticket = active.current;
    if (!Platform.isTV || !enabled || !ticket || !ticketCurrent(ticket)) return;
    const identity = installationIdentity();
    if (!identity) return;
    const lifetime = new NativeScreenLifetime(context), abort = new AbortController();
    let receiver: ReceiverHost | undefined, foreground = new AbortController();
    const current = () => ticketCurrent(ticket) && !abort.signal.aborted && lifetime.current() && installationIdentity() === identity
      && nativeDeviceSettings(context).read('receiveFromDevices') === true;
    const cancelTicket = () => {abort.abort(); foreground.abort(); lifetime.dispose();};
    ticket.abort.signal.addEventListener('abort', cancelTicket, {once: true});
    const follow = () => {
      if (!current()) return;
      if (AppState.currentState === 'active') {if (foreground.signal.aborted) foreground = new AbortController(); receiver?.start();}
      else {foreground.abort(); receiver?.stop();}
    };
    const activity = AppState.addEventListener('change', follow);
    const playbackOff = context.playback.subscribe(() => {if (current()) receiver?.setPlayerActive(!!context.playback.getSnapshot().dock.local);});
    void nativeRouteCrypto.sha256(Uint8Array.from('portico-receiver:' + identity.installationId, c => c.charCodeAt(0) & 255)).then(digest => {
      if (!current()) return;
      receiver = new ReceiverHost({
        api: {request: async <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
          const opening = foreground, caller = new AbortController(), cancel = () => caller.abort();
          const signals = [opening.signal, signal].filter((value): value is AbortSignal => !!value);
          for (const value of signals) {value.addEventListener('abort', cancel); if (value.aborted) cancel();}
          try {
            if (!current() || AppState.currentState !== 'active') throw new Error('native_receiver_retired');
            const value = await lifetime.request(next => context.api.request<T>(path, method, body, next), caller.signal);
            if (!current() || opening !== foreground || opening.signal.aborted) throw new Error('native_receiver_retired');
            return value;
          } finally {for (const value of signals) value.removeEventListener('abort', cancel);}
        }},
        device: {deviceId: identity.installationId, displayName: identity.name, platform: identity.platform, keyFingerprint: routeBase64(digest)},
        events: context.events,
        player: {
          load: (itemId, positionSeconds) => {
            if (!current() || AppState.currentState !== 'active') return;
            const opening = foreground;
            const playingHere = () => current() && opening === foreground && !opening.signal.aborted && AppState.currentState === 'active';
            void lifetime.request(signal => context.api.request(encodedRoutePath('get_items_id', {id: encodeURIComponent(itemId)}), 'GET', undefined, signal).then(parseMediaItem), opening.signal).then(item => {
              if (!playingHere() || item.id !== itemId) return;
              const kind = item.kind === 'track' ? 'song' : item.kind === 'audiobook' ? 'audiobook_file' : item.kind;
              if (!['movie', 'episode', 'extra', 'song', 'audiobook_file'].includes(kind)) throw new Error('native_receiver_item_unsupported');
              const target = {itemId, startSeconds: positionSeconds};
              const entry = {...item, kind: kind as ContentEntry['kind'], playback: target} as ContentEntry;
              return context.playback.start({entry, target, startIntent: 'explicit', lifetime: {signal: opening.signal, isCurrent: playingHere}});
            }).catch(error => {if (current()) runtime.reportError(error);});
          },
          state: () => {
            const snapshot = context.playback.getSnapshot(), playback = snapshot.playback;
            return {itemId: current() ? playback.itemId ?? '' : '', playbackId: current() ? snapshot.fence?.sessionId ?? '' : '',
              playing: current() && playback.phase === 'ready' && playback.intent === 'playing' && playback.positionSeconds > 0, positionSeconds: playback.positionSeconds};
          },
        },
      });
      receiver.setPlayerActive(!!context.playback.getSnapshot().dock.local);
      setOwned({host: receiver, ticket}); follow();
    }).catch(error => {if (current()) runtime.reportError(error);});
    return () => {ticket.abort.signal.removeEventListener('abort', cancelTicket); cancelTicket(); activity.remove(); playbackOff(); receiver?.dispose(); setOwned(value => value?.ticket === ticket ? undefined : value);};
  }, [context, enabled, runtime, ticketCurrent]);
  const state = useSyncExternalStore<ReceiverSnapshot>(host?.subscribe ?? noSubscribe, host?.getSnapshot ?? getOff);
  const asking = state.asking;
  const decide = (decision: 'accept' | 'decline') => {
    if (!host || !owned || !ticketCurrent(owned.ticket) || !asking || !context.current() || !nativeDeviceSettings(context).read('receiveFromDevices') || AppState.currentState !== 'active' || host.getSnapshot().asking !== asking) return;
    void host.decide(asking, decision).catch(error => {if (ticketCurrent(owned.ticket)) runtime.reportError(error);});
  };
  return <Dialog testID="tv-receiver-approval" open={!!asking && !!owned && ticketCurrent(owned.ticket)} title={runtime.text.t('playOn.receiver.askTitle', {device: asking?.controllerDisplayName ?? ''})} onOpenChange={open => {if (!open) decide('decline');}}>
    <View style={{gap: 16}}>
      <Text>{runtime.text.t('playOn.receiver.askBody', {device: asking?.controllerDisplayName ?? ''})}</Text>
      <Button testID="tv-receiver-decline" variant="glass" onPress={() => decide('decline')}>{runtime.text.t('playOn.receiver.decline')}</Button>
      <Button testID="tv-receiver-allow" onPress={() => decide('accept')}>{runtime.text.t('playOn.receiver.allow')}</Button>
    </View>
  </Dialog>;
}
