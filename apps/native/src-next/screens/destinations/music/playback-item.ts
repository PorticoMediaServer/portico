/** A cached primitive projection: progress, buffering and queue publications
 * cannot make an album/artist page reconcile its track list. */
export function playbackItemStore(source?: Readonly<{
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => Readonly<{playback: Readonly<{itemId?: string | null}>}>;
}>) {
  const read = () => source?.getSnapshot().playback.itemId ?? undefined;
  return {
    getSnapshot: read,
    subscribe: (listener: () => void) => {
      if (!source) return () => {};
      let current = read();
      return source.subscribe(() => {const next = read(); if (next !== current) {current = next; listener();}});
    },
  };
}
