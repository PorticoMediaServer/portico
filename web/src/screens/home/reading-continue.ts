import {createReadBindings, type ReadBindingContext} from '../read/bindings';
import {createReadingContinueCollection, type ReadingContinueItem} from '../read/library-collection';
import {readChosenDestination, type ReadDestination} from '../read/navigation';

/** A captured Home/Library data door. The canonical collection alone owns
 * cursor rows and reading progress; construction never fetches or Prepares. */
export function createReadingContinueBindings(options: Readonly<{
  context: ReadBindingContext;
  libraryId?: string;
  open: (destination: ReadDestination) => void | Promise<void>;
}>) {
  const reading = createReadBindings(options.context), rows = createReadingContinueCollection(reading.client, options.libraryId);
  const open = options.open;
  let closed = false, disposal: Promise<void> | undefined, off = () => {};
  const dispose = () => {
    if (disposal) return disposal;
    closed = true;off();rows.dispose();disposal = reading.dispose();return disposal;
  };
  const current = () => {
    if (closed) return false;
    if (!reading.authority.isCurrent()) {void dispose().catch(() => {});return false;}
    return true;
  };
  const contains = (row: ReadingContinueItem) => current() && rows.collection.residentItems().includes(row);
  off = reading.authority.subscribe(() => {void dispose().catch(() => {});});
  return Object.freeze({
    collection: rows.collection,
    isCurrent: current,
    contains,
    async open(row: ReadingContinueItem) {
      if (!contains(row) || !row.item.inventoryAvailable) throw new DOMException('Reading Continue changed', 'AbortError');
      await open(readChosenDestination(row.item.id));
    },
    dispose,
  });
}
export type ReadingContinueBindings = ReturnType<typeof createReadingContinueBindings>;
