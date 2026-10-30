import type {RefObject} from 'react';
import type {View} from 'react-native';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {SearchEntry, SearchGroup} from '../../../../../packages/client-core/src/search';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeDestinationActivationPort} from '../destinations/focus';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import {createNativeEntryActions} from '../destinations/entry-actions';
import {nativeScreenServices} from '../native-screen-services';
import type {NativeSearchDestination} from './model';
import type {NativeSearchActions, NativeSearchSelectionPort} from './types';

const media = (entry: SearchEntry): entry is ContentEntry => entry.kind !== 'person' && entry.kind !== 'channel';

/** The current Search page supplies membership and its real generation. A
 * borrowed media action is retired whenever that generation is replaced, even
 * for the same query/fence and even if the producer retained an entry object. */
export function createNativeSearchActions(context: NativeShellBootstrapContext, source: NativeSearchDestination,
  activation: NativeDestinationActivationPort, selection?: NativeSearchSelectionPort): NativeSearchActions & Readonly<{canMenu: (entry: SearchEntry, group: SearchGroup) => boolean; dispose: () => void}> {
  const lifetime = nativeDestinationLifetime(context), common = nativeScreenServices(context);
  let disposed = false, generation = -1, revision = 0;
  let borrowed: ReturnType<typeof createNativeEntryActions> | undefined, offMenu: (() => void) | undefined;
  const listeners = new Set<() => void>();
  const current = () => !disposed && lifetime.current() && activation.getSnapshot().visible && source.getSnapshot().search.phase === 'ready';
  const contains = (entry: SearchEntry, group?: SearchGroup) => current() && source.getSnapshot().groups.some(result =>
    (group === undefined || result.id === group) && result.status === 'success' && result.items.includes(entry));
  const emit = () => {revision++; for (const listener of [...listeners]) listener();};
  const reconcile = () => {
    if (disposed) return;
    const next = source.getSnapshot().search.generation;
    if (next === generation) return;
    offMenu?.(); offMenu = undefined; borrowed?.dispose();
    generation = next;
    const captured = generation;
    borrowed = createNativeEntryActions(context, {
      current: () => current() && source.getSnapshot().search.generation === captured,
      contains,
      subscribe: source.subscribe,
    });
    offMenu = borrowed.menuState.subscribe(emit); emit();
  };
  const offSource = source.subscribe(reconcile);
  reconcile();
  return Object.freeze({
    menuState: Object.freeze({getSnapshot: () => revision, subscribe: (listener: () => void) => {
      if (disposed) return () => {};
      listeners.add(listener); return () => {listeners.delete(listener);};
    }}),
    canMenu: (entry, group) => media(entry) && contains(entry, group),
    open: (entry, group) => {
      if (!contains(entry, group)) return;
      if (media(entry)) {borrowed?.open(entry); return;}
      if (entry.kind === 'person' && group === 'people' && entry.navigation.view === 'person') {
        context.navigation.open(Object.freeze({kind: 'person', personId: entry.navigation.entityId}), context.authority()); return;
      }
      if (entry.kind === 'channel' && group === 'live-tv' && entry.navigation.view === 'channel') {
        const {sourceId, channelId, provenance} = entry.navigation;
        if (sourceId && channelId && provenance === 'live-source') {
          context.navigation.open(Object.freeze({kind: 'channel', sourceId, channelId}), context.authority()); return;
        }
        common.reportError(Object.assign(new Error('native_search_channel_source_unavailable'), {code: 'unavailable'}), 'search');
      }
    },
    play: (entry, group) => {if (media(entry) && contains(entry, group)) borrowed?.play(entry);},
    menu: (entry: SearchEntry, group: SearchGroup, anchor?: RefObject<View | null>) => {
      if (!media(entry) || !contains(entry, group)) return;
      const captured = source.getSnapshot().search.generation;
      const select = selection?.item(entry, group);
      borrowed?.menu(entry, anchor, select ? () => {
        if (current() && source.getSnapshot().search.generation === captured && contains(entry, group)) selection?.item(entry, group)?.onEnter();
      } : undefined);
    },
    isMenuOpen: (entry, group) => media(entry) && contains(entry, group) && borrowed?.isMenuOpen(entry) === true,
    dispose: () => {if (disposed) return; disposed = true; offSource(); offMenu?.(); borrowed?.dispose(); borrowed = undefined; listeners.clear();},
  });
}
