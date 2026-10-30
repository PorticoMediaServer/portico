import type {ReadingItem} from '../../../../../packages/client-core/src';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {nativeReadEligibility, openNativeReadBook, type NativeReadOrigin} from './eligibility';

/**
 * tablet-read#1: a card in the tablet Books grid opens the item's own Reading
 * book page. The clicked leaf is a real browse entry of this library; its exact
 * version comes from the same signed versions producer the reader admits, so an
 * entry's kind is never read as an audiobook and no source is guessed. The
 * admission runs against the route and revision the grid was laid out on, and
 * is refused once the pressed leaf, its collection or the library has moved on.
 * No preview session is opened: the page loads its own manifest.
 */
export async function openNativeReadingBookEntry(context: NativeShellBootstrapContext, target: Readonly<{
  libraryId: string; entry: ContentEntry; origin: NativeReadOrigin; resident: () => boolean; signal?: AbortSignal;
}>): Promise<boolean> {
  const {entry, libraryId, signal} = target, selected = context.session();
  if (signal?.aborted || !selected || !context.current() || !target.resident()) return false;
  if ((entry.kind !== 'ebook' && entry.kind !== 'comic') || (entry.libraryId !== undefined && entry.libraryId !== libraryId)) return false;
  const resident = () => !signal?.aborted && context.current() && context.session()?.sessionFamilyId === selected.sessionFamilyId && target.resident();
  // The Read door checks only this context's `current()` on both sides of its
  // own versions await, so the same residence guard fences its admission too.
  const scoped: NativeShellBootstrapContext = {...context, current: resident};
  const client = context.api.reading({viewer: selected.viewer, isCurrent: resident, signal});
  let item: ReadingItem | undefined;
  try {
    const versions = await nativeReadEligibility(client, entry.id, signal);
    const version = versions?.versions.find(value => value.inventoryAvailable);
    if (version && resident()) item = Object.freeze({id: entry.id, libraryId, title: entry.title, format: version.format,
      sourceId: version.sourceId, inventoryAvailable: true as const});
  } finally {client.dispose();}
  return item ? openNativeReadBook(scoped, {libraryId, item}, target.origin, signal) : false;
}
