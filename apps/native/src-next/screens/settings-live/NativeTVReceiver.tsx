import {readCatalogueItemResponse} from '../../../../../packages/client-core/src/query/catalogue-reads';
import React, {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {AppState, Platform, Text, View} from 'react-native';
import {ReceiverHost, type ReceiverSnapshot} from '../../../../../packages/client-core/src/remote-playback';
import {installationIdentity} from '../../../../../packages/client-core/src/installation';
import {parseMediaItem} from '../../../../../packages/client-core/src/media-item';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {receiverAllowedDevices} from '../../platform/play-on/receiver-allowed-storage';
import {nativeDeviceSettings} from '../../platform/device-settings';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRuntime} from '../../shell/runtime';
import {Button} from '../../kit/controls';
import {Dialog} from '../../kit/overlays';
import {NativeScreenLifetime} from './lifetime';

const off: ReceiverSnapshot = Object.freeze({phase: 'off'});
const noSubscribe = () => () => {};
const getOff = () => off;

/** Accepting playback from the account's other devices, only in the foreground: the person is
 * asked once per sending device, the TV remembers the answer, and commands from a controlling
 * device are carried out here on the session they name. */
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
    const identity = installationIdentity(), session = context.session();
    if (!identity || !session) return;
    const lifetime = new NativeScreenLifetime(context), abort = new AbortController();
    let receiver: ReceiverHost | undefined;
    const current = () => ticketCurrent(ticket) && !abort.signal.aborted && lifetime.current() && installationIdentity() === identity
      && context.session() === session && nativeDeviceSettings(context).read('receiveFromDevices') === true;
    const cancelTicket = () => {abort.abort(); receiver?.stop(); lifetime.dispose();};
    ticket.abort.signal.addEventListener('abort', cancelTicket, {once: true});
    // Only in the foreground: a TV in the background is not listed and plays nothing sent.
    const follow = () => {
      if (!current()) return;
      if (AppState.currentState === 'active') receiver?.start(); else receiver?.stop();
    };
    const activity = AppState.addEventListener('change', follow);
    const playingHere = () => current() && AppState.currentState === 'active';
    // The command is for this exact session; anything else plays here now and it is dropped.
    const fenceFor = (sessionId: string) => {const fence = context.playback.getSnapshot().fence; return fence?.sessionId === sessionId ? fence : undefined;};
    const scope = [session.viewer.serverId, session.viewer.accountId, session.sessionFamilyId].join(':');
    void receiverAllowedDevices(scope).then(allowed => {
      if (!current()) return;
      receiver = new ReceiverHost({api: context.api, events: context.events, allowed, isCurrent: playingHere,
        player: {
          load: async play => {
            if (!playingHere()) throw new Error('native_receiver_retired');
            const item = await lifetime.request(signal => readCatalogueItemResponse(context.api, play.itemId, signal).then(parseMediaItem), abort.signal);
            if (!playingHere() || item.id !== play.itemId) throw new Error('native_receiver_retired');
            const kind = item.kind === 'track' ? 'song' : item.kind === 'audiobook' ? 'audiobook_file' : item.kind;
            if (!['movie', 'episode', 'extra', 'song', 'audiobook_file'].includes(kind)) throw new Error('native_receiver_item_unsupported');
            const target = {itemId: play.itemId, startSeconds: play.positionSeconds};
            const entry = {...item, kind: kind as ContentEntry['kind'], playback: target} as ContentEntry;
            await context.playback.start({entry, target, startIntent: 'explicit', lifetime: {signal: abort.signal, isCurrent: playingHere},
              move: {requestId: play.requestId, ...(play.audioTrackId ? {audioTrackId: play.audioTrackId} : {}), ...(play.queueId ? {queueId: play.queueId} : {})}});
            // The subtitles picked on the other device ('' is off) follow once this one plays.
            const fence = context.playback.getSnapshot().fence;
            if (play.subtitleTrackId !== undefined && fence && playingHere()) await context.playback.change(fence, {subtitleTrackId: play.subtitleTrackId || null}).catch(() => {});
          },
          state: () => {
            const snapshot = context.playback.getSnapshot(), playback = snapshot.playback;
            return {itemId: current() ? playback.itemId ?? '' : '', playbackId: current() ? snapshot.fence?.sessionId ?? '' : '',
              playing: current() && playback.phase === 'ready' && playback.intent === 'playing' && playback.positionSeconds > 0, positionSeconds: playback.positionSeconds};
          },
          command: (sessionId, command) => {
            const fence = fenceFor(sessionId), adapter = context.playback;
            if (!fence || !playingHere()) return;
            const run = async () => {
              if (command.state && (adapter.getSnapshot().playback.intent === 'playing') !== (command.state === 'playing')) await adapter.toggle(fence);
              if (command.seekMs !== undefined) await adapter.seek(fence, command.seekMs / 1000);
              if (command.skip === 'next') await adapter.next(fence);
              if (command.skip === 'previous') await adapter.previous(fence);
              if (command.audioTrackId !== undefined) await adapter.change(fence, {audioTrackId: command.audioTrackId});
              if (command.subtitleTrackId !== undefined) await adapter.change(fence, {subtitleTrackId: command.subtitleTrackId || null});
            };
            void run().catch(error => {if (current()) runtime.reportError(error);});
          },
        },
      });
      setOwned({host: receiver, ticket}); follow();
    }).catch(error => {if (current()) runtime.reportError(error);});
    return () => {ticket.abort.signal.removeEventListener('abort', cancelTicket); cancelTicket(); activity.remove(); receiver?.dispose(); setOwned(value => value?.ticket === ticket ? undefined : value);};
  }, [context, enabled, runtime, ticketCurrent]);
  const state = useSyncExternalStore<ReceiverSnapshot>(host?.subscribe ?? noSubscribe, host?.getSnapshot ?? getOff);
  const asking = state.asking;
  const decide = (decision: 'allow' | 'decline') => {
    if (!host || !owned || !ticketCurrent(owned.ticket) || !asking || !context.current() || !nativeDeviceSettings(context).read('receiveFromDevices') || AppState.currentState !== 'active' || host.getSnapshot().asking !== asking) return;
    void host.decide(asking, decision).catch(error => {if (ticketCurrent(owned.ticket)) runtime.reportError(error);});
  };
  return <Dialog testID="tv-receiver-approval" open={!!asking && !!owned && ticketCurrent(owned.ticket)} title={runtime.text.t('playOn.receiver.askTitle', {device: asking?.from.deviceName ?? ''})} onOpenChange={open => {if (!open) decide('decline');}}>
    <View style={{gap: 16}}>
      <Text>{runtime.text.t('playOn.receiver.askBody', {device: asking?.from.deviceName ?? ''})}</Text>
      <Button testID="tv-receiver-decline" variant="glass" onPress={() => decide('decline')}>{runtime.text.t('playOn.receiver.decline')}</Button>
      <Button testID="tv-receiver-allow" onPress={() => decide('allow')}>{runtime.text.t('playOn.receiver.allow')}</Button>
    </View>
  </Dialog>;
}
