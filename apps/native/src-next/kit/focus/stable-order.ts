/** UI order only. Actual source membership can remove a row; it cannot invent one.
 * Reads are pure so an abandoned render cannot replace the last committed order. */
export function createStableFocusOrder<T>(getKey: (item: T) => string) {
  let committed: Readonly<{scope: unknown; items: readonly T[]}> | undefined;
  return {
    project({scope, held, items, allItems, memberIds}: Readonly<{scope: unknown; held: boolean; items: readonly T[]; allItems: readonly T[]; memberIds: ReadonlySet<string>}>): readonly T[] {
      if (!held || !committed || committed.scope !== scope) return items;
      const live = new Map(allItems.map(item => [getKey(item), item]));
      return committed.items.filter(item => memberIds.has(getKey(item))).map(item => live.get(getKey(item)) ?? item);
    },
    commit(scope: unknown, items: readonly T[]) {committed = {scope, items};},
  };
}
