import type {ContentEntry} from '@core/library-content.ts';
import type {SelectionContext} from '@core/selection-model/index.ts';
import {PlaybackOptionsClient} from '@core/playback-v1/options.ts';
import {localV1Http} from '@core/playback-v1/local-http.ts';
import {createDetailBindings} from './bindings';
import {screenLifetime, type ScreenContext} from './context';

/** Private adapter awaiting the steward's checked MAIN EntryMenuDetails public surface. */
export type EntryDetailsAuthority = Readonly<{isCurrent: (context: SelectionContext) => boolean}>;
export function createEntryDetailsBindings(context: ScreenContext, authority: EntryDetailsAuthority) {
  const life = screenLifetime(context), detail = createDetailBindings(context), versions = new PlaybackOptionsClient(localV1Http(life.api));
  const guard = (captured: SelectionContext, signal: AbortSignal) => {
    life.assert(); signal.throwIfAborted();
    if (captured.scope.serverId !== context.scope.serverId || !authority.isCurrent(captured)) throw new DOMException('Entry menu authority changed', 'AbortError');
  };
  const target = (entry: ContentEntry) => {
    if (!entry.libraryId || entry.navigation?.view !== 'item' || entry.navigation.entityId !== entry.id) throw new Error('An actual item target is required');
    return {libraryId: entry.libraryId, itemId: entry.id};
  };
  return {
    details(entry: ContentEntry, captured: SelectionContext, signal: AbortSignal) { guard(captured, signal); context.effects.openEntry(entry); },
    async versions(entry: ContentEntry, captured: SelectionContext, signal: AbortSignal) {
      guard(captured, signal); const value = await versions.options(target(entry).itemId, {}, life.signal(signal)); guard(captured, signal);
      if (value.itemId !== entry.id) throw new Error('Playback options target mismatch');
      return value;
    },
    async playVersion(entry: ContentEntry, versionId: string, captured: SelectionContext, signal: AbortSignal) {
      guard(captured, signal); const item = target(entry);
      // Read the actual current detail actions and grouped version offer at command time.
      await detail.select(item, signal); guard(captured, signal);
      const data = detail.service.getSnapshot().data, offered = await versions.options(item.itemId, {}, life.signal(signal));
      guard(captured, signal);
      const action = data?.actions.find(value => value.enabled && value.playback && value.id !== 'start_over');
      if (!data || !action?.playback || offered.itemId !== item.itemId || !offered.versions.some(value => value.id === versionId)) throw new Error('Version playback unavailable');
      // Do not compare a Search aggregate revision/fence to a library's detail revision.
      // Both owners must still be current, and the item endpoint reauthorizes its read.
      detail.current(data);
      // A manual choice resumes the title now, not the position captured by the menu.
      // This remains an explicit one-shot version start; ordinary Play keeps its automatic choice.
      const positionMs = offered.resume?.positionMs ?? 0;
      if (!Number.isFinite(positionMs) || positionMs < 0) throw new Error('Unreadable title resume position');
      await context.effects.play({...item, startSeconds: positionMs / 1000, versionId}, life.signal(signal));
    },
    dispose() { life.dispose(); detail.dispose(); },
  };
}
export type EntryDetailsBindings = ReturnType<typeof createEntryDetailsBindings>;
