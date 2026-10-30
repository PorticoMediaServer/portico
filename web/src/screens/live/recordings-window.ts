/** Three genuine producer chunks, plus their opaque continuation receipts. Row
 * bodies are bounded; receipts are retained to reject cycles and revisit pages.
 * No aggregate or revision is ever reconstructed from the resident rows. */
export function recordingsWindow<T extends {nextCursor: string}>() {
  let entries: {cursor: string; page: T}[] = [], index = -1;
  const cursors: string[] = [];
  const seen = new Set<string>();
  return {
    reset(cursor = '', page?: T) {
      entries = page ? [{cursor, page}] : [];index = page ? 0 : -1;
      cursors.length = 0;seen.clear();
      if (page) {cursors.push(cursor);seen.add(cursor);}
    },
    offer(direction: 'next' | 'previous'): string | undefined {
      if (index < 0) return;
      if (direction === 'previous') return index > 0 ? cursors[index - 1] : undefined;
      const token = entries.find(entry => entry.cursor === cursors[index])?.page.nextCursor;
      if (!token || seen.has(token) && cursors[index + 1] !== token) return;
      return token;
    },
    cached(cursor: string): T | undefined {return entries.find(entry => entry.cursor === cursor)?.page;},
    accept(cursor: string, page: T) {
      const known = cursors.indexOf(cursor);
      if (known < 0) {cursors.splice(index + 1);cursors.push(cursor);seen.add(cursor);index = cursors.length - 1;}
      else index = known;
      entries = [...entries.filter(entry => entry.cursor !== cursor), {cursor, page}].slice(-3);
    },
  };
}
