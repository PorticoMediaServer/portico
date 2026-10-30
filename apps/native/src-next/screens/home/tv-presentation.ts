import type {HomeRow} from '../../../../../packages/client-core/src/home';

function keptOrder<T>(before: readonly T[], next: readonly T[], key: (value: T) => string): readonly T[] {
  const remaining = new Map(next.map(value => [key(value), value]));
  const result: T[] = [];
  for (const value of before) {
    const id = key(value), latest = remaining.get(id);
    if (latest) {result.push(latest); remaining.delete(id);}
  }
  return [...result, ...remaining.values()];
}

/** Keep occupied slots stable, but apply removals and latest capability-bearing objects.
 * Ownership includes retained menu/page/player openers from the shared focus registry. */
export function tvHomeRows(before: readonly HomeRow[], incoming: readonly HomeRow[], owns: (rowId: string) => boolean, pageOccupied: boolean): readonly HomeRow[] {
  const rows = pageOccupied ? keptOrder(before, incoming, row => row.id) : incoming;
  return rows.map(row => {
    const previous = before.find(value => value.id === row.id);
    if (!previous || !owns(row.id)) return row;
    if (row.live && previous.live) return {...row, artworkShape: previous.artworkShape, live: {...row.live, channels: keptOrder(previous.live.channels, row.live.channels,
      channel => JSON.stringify([channel.channel.sourceId, channel.channel.id]))}};
    return {...row, artworkShape: previous.artworkShape, entries: keptOrder(previous.entries, row.entries, entry => JSON.stringify([entry.kind, entry.libraryId, entry.id]))};
  });
}
