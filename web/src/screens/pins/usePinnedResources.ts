import {useEffect, useState, useSyncExternalStore} from 'react';
import type {PinnedResource, PinsApi} from '@core/library-pins.ts';
import {randomId} from '@core/random-id.ts';
import {PinsController, type PinsSnapshot} from './pins-controller';

type Library = Readonly<{id: string}>;
const idle: PinsSnapshot = Object.freeze({phase: 'idle', busy: false});
const none: readonly PinnedResource[] = Object.freeze([]);

/** The viewer's pinned collections, saved views and playlists in the server's order (phone-app Saved: "Pinned"),
 * read only while `enabled`. `reload` re-reads after the pin editor closes; nothing is kept that the server did
 * not answer. */
export function usePinnedResources(context: Readonly<{api: PinsApi; isCurrent: () => boolean; scope: {serverId: string}; identity?: {libraries: readonly Library[]}}>,
  enabled: boolean): Readonly<{pins: readonly PinnedResource[]; reload: () => void}> {
  const [controller, setController] = useState<PinsController>();
  const {api, isCurrent} = context, serverId = context.scope.serverId, libraries = context.identity?.libraries;
  useEffect(() => {
    if (!enabled) return;
    const created = new PinsController({api, serverId, current: isCurrent, libraryIds: () => (libraries ?? []).map(library => library.id), requestId: async () => randomId()});
    setController(created); void created.load();
    return () => {created.dispose(); setController(current => current === created ? undefined : current);};
  }, [enabled, api, serverId, isCurrent, libraries]);
  const snapshot = useSyncExternalStore(controller?.subscribe ?? noSubscribe, controller?.getSnapshot ?? idleSnapshot, controller?.getSnapshot ?? idleSnapshot);
  return {pins: snapshot.resources?.order ?? none, reload: () => {void controller?.load();}};
}
const noSubscribe = () => () => {};
const idleSnapshot = () => idle;
