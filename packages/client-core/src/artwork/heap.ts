/** A small binary min-heap. `before(a, b)` is true when `a` should come out first. */
export class MinHeap<T> {
  private items: T[] = [];
  private before: (a: T, b: T) => boolean;

  constructor(before: (a: T, b: T) => boolean) { this.before = before; }

  get size(): number { return this.items.length; }

  peek(): T | undefined { return this.items[0]; }

  push(item: T): void {
    const items = this.items;
    items.push(item);
    let i = items.length - 1;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (!this.before(items[i]!, items[parent]!)) break;
      [items[i], items[parent]] = [items[parent]!, items[i]!];
      i = parent;
    }
  }

  pop(): T | undefined {
    const items = this.items;
    if (!items.length) return undefined;
    const top = items[0];
    const last = items.pop()!;
    if (items.length) {
      items[0] = last;
      let i = 0;
      for (;;) {
        const l = 2 * i + 1, r = l + 1;
        let best = i;
        if (l < items.length && this.before(items[l]!, items[best]!)) best = l;
        if (r < items.length && this.before(items[r]!, items[best]!)) best = r;
        if (best === i) break;
        [items[i], items[best]] = [items[best]!, items[i]!];
        i = best;
      }
    }
    return top;
  }

  /** Keep only the items `keep` accepts, then restore heap order (O(n)). */
  retain(keep: (item: T) => boolean): void {
    const kept = this.items.filter(keep);
    this.items = [];
    for (const item of kept) this.push(item);
  }

  clear(): void { this.items = []; }
}
