import type {RefObject} from 'react';
import type {View} from 'react-native';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {nativeScreenServices} from '../native-screen-services';
import {nativeWatchEntryMenu, type NativeWatchEntryMenu} from '../watch';
import {openNativeReadingBookEntry} from '../read/reading-books-open';
import {nativeDestinationLifetime} from './lifetime';
import type {NativeWatchMenuExtra} from '../watch/factory';

export type NativeEntryMembership = Readonly<{
  /** Actual visible destination and its currently mounted/loaded entry owner. */
  current: () => boolean;
  contains: (entry: ContentEntry) => boolean;
  subscribe: (listener: () => void) => () => void;
}>;
export type NativeEntryMenuState = Readonly<{
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => number;
}>;

/** Library, Saved and Search supply their own real membership. This borrows
 * Watch's existing menu and the bootstrap player, never Home selection state. */
export function createNativeEntryActions(context: NativeShellBootstrapContext, membership: NativeEntryMembership) {
  const lifetime = nativeDestinationLifetime(context), common = nativeScreenServices(context);
  const pending = new Set<Readonly<{entry: ContentEntry; revision: number; abort: AbortController}>>();
  const listeners = new Set<() => void>();
  let disposed = false, revision = 0, menu: NativeWatchEntryMenu | undefined, offMenu: (() => void) | undefined;
  const current = (entry: ContentEntry) => !disposed && lifetime.current() && membership.current() && membership.contains(entry);
  const emit = () => {revision++; for (const listener of [...listeners]) listener();};
  const borrowMenu = () => {
    const next = nativeWatchEntryMenu(context);
    if (next !== menu) {offMenu?.(); offMenu = undefined; menu = next;}
    if (menu && !offMenu && listeners.size) offMenu = menu.subscribe(emit);
    return menu;
  };
  const check = () => {
    for (const operation of pending) if (!current(operation.entry) || context.navigation.getSnapshot().revision !== operation.revision) operation.abort.abort();
  };
  const off = [membership.subscribe(check), context.navigation.subscribe(check), context.subscribeAuthority(check)];
  const play = (entry: ContentEntry) => {
    if (!current(entry) || !entry.playback && !entry.radioSeed) return;
    const operation = {entry, revision: context.navigation.getSnapshot().revision, abort: new AbortController()};
    const isCurrent = () => !operation.abort.signal.aborted && current(entry) && context.navigation.getSnapshot().revision === operation.revision;
    pending.add(operation);
    const execute = async () => {
      if (!isCurrent()) return;
      if (entry.kind === 'radio' && entry.radioSeed) {
        const catalogue = context.playback.cataloguePlayback?.();
        if (!catalogue) throw Object.assign(new Error('native_radio_playback_unavailable'), {code: 'unavailable'});
        await catalogue.playSegments([{source: {container: {kind: 'radio', id: entry.radioSeed.id}}}], {signal: operation.abort.signal, isCurrent});
      } else if (entry.playback) {
        await context.playback.start({entry, target: entry.playback, ...(entry.kind === 'chapter' && entry.playback.startSeconds !== undefined ? {startIntent: 'explicit' as const} : {}), lifetime: {signal: operation.abort.signal, isCurrent}});
      }
    };
    void execute().catch(error => {if (isCurrent()) common.reportError(error, 'detail');}).finally(() => {pending.delete(operation);});
  };
  const menuState: NativeEntryMenuState = Object.freeze({getSnapshot: () => revision, subscribe: listener => {
    if (disposed) return () => {};
    listeners.add(listener); borrowMenu();
    return () => {listeners.delete(listener); if (!listeners.size) {offMenu?.(); offMenu = undefined;}};
  }});
  return Object.freeze({
    menuState,
    open: (entry: ContentEntry) => {
      if (!current(entry)) return;
      if (entry.kind === 'radio' || entry.kind === 'song' || entry.kind === 'audiobook_file') {
        if (entry.kind === 'radio' ? !entry.radioSeed : !entry.playback) common.reportError(Object.assign(new Error('unavailable'), {code: 'unavailable'}), 'detail');
        else play(entry);
        return;
      }
      if (entry.kind === 'ebook' || entry.kind === 'comic') {
        const snapshot = context.navigation.getSnapshot(), libraryId = entry.libraryId;
        const kind = entry.kind === 'ebook' ? 'book' : 'comic';
        if (!libraryId || !context.libraries().some(library => library.id === libraryId && library.kind === kind)) {
          common.reportError(Object.assign(new Error('unavailable'), {code: 'unavailable'}), 'detail'); return;
        }
        const operation = {entry, revision: snapshot.revision, abort: new AbortController()};
        const resident = () => !operation.abort.signal.aborted && current(entry) && context.navigation.getSnapshot() === snapshot
          && context.libraries().some(library => library.id === libraryId && library.kind === kind);
        pending.add(operation);
        void openNativeReadingBookEntry(context, {libraryId, entry, origin: {route: snapshot.route, revision: snapshot.revision}, resident, signal: operation.abort.signal})
          .then(opened => {if (!opened && resident()) common.reportError(Object.assign(new Error('unavailable'), {code: 'unavailable'}), 'detail');})
          .catch(error => {if (resident()) common.reportError(error, 'detail');}).finally(() => {pending.delete(operation);});
        return;
      }
      if (entry.kind === 'collection' && !entry.libraryId) {common.reportError(Object.assign(new Error('unavailable'), {code: 'unavailable'}), 'detail'); return;}
      common.navigate(entry);
    },
    play,
    menu: (entry: ContentEntry, anchorRef?: RefObject<View | null>, select?: () => void, extraActions?: readonly NativeWatchMenuExtra[]) => {
      if (!current(entry)) return;
      const captured = context.navigation.getSnapshot().revision, port = borrowMenu();
      if (!port) {common.reportError(Object.assign(new Error('native_watch_menu_unavailable'), {code: 'unavailable'}), 'detail'); return;}
      const entryCurrent = () => current(entry) && context.navigation.getSnapshot().revision === captured;
      void port.open(entry, entryCurrent, anchorRef, select ? () => {if (entryCurrent()) select();} : undefined,
        extraActions?.map(action => ({...action, onSelect: () => {if (entryCurrent() && !action.disabled) action.onSelect();}}))).catch(error => {if (entryCurrent()) common.reportError(error, 'detail');});
    },
    isMenuOpen: (entry: ContentEntry) => current(entry) && nativeWatchEntryMenu(context)?.isOpen(entry) === true,
    dispose: () => {
      if (disposed) return;
      disposed = true; for (const operation of pending) operation.abort.abort(); pending.clear();
      for (const unsubscribe of off) unsubscribe(); offMenu?.(); offMenu = undefined; menu = undefined;
      listeners.clear();
    },
  });
}

/** The borrowed entry menu and page actions, as the calling screen sees them. */
export type NativeEntryActions = ReturnType<typeof createNativeEntryActions>;
