/** Entitled item reads are retained within one room/viewer, independently of queue order.
 * Catalogue revision hints explicitly invalidate affected values. In-flight reads retain
 * their own controller so a removed entry or an old room cannot publish into its successor. */
export class TogetherItemSummaries<T> {
  private scope = '';
  private wanted = new Set<string>();
  private values = new Map<string, T>();
  private pending = new Map<string, AbortController>();
  private failed = new Set<string>();
  private disposed = false;
  constructor(private readonly ports: {current(): boolean; read(id: string, signal: AbortSignal): Promise<T>; publish(values: ReadonlyMap<string, T>): void; reportError(error: unknown): void}) {}

  update(scope: string, ids: readonly string[]): void {
    if (this.disposed) return;
    if (scope !== this.scope) {
      this.cancel();this.values.clear();this.scope = scope;
    }
    this.wanted = new Set(scope && this.ports.current() ? ids : []);
    this.failed.clear();
    for (const id of this.values.keys()) if (!this.wanted.has(id)) this.values.delete(id);
    for (const [id, controller] of this.pending) if (!this.wanted.has(id)) {controller.abort();this.pending.delete(id);}
    this.publish();this.drain();
  }
  invalidate(changed: (value: T) => boolean): void {
    if (this.disposed) return;
    for (const [id, value] of this.values) if (changed(value)) this.values.delete(id);
    // Until an answer arrives its library is unknown; it cannot cross a freshness boundary.
    this.cancel();this.failed.clear();this.publish();this.drain();
  }
  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;this.cancel();this.wanted.clear();this.values.clear();this.failed.clear();this.publish();
  }
  private cancel(): void {for (const controller of this.pending.values()) controller.abort();this.pending.clear();}
  private publish(): void {this.ports.publish(new Map(this.values));}
  private drain(): void {
    if (this.disposed || !this.scope || !this.ports.current()) return;
    for (const id of this.wanted) {
      if (this.pending.size >= 4) break;
      if (this.values.has(id) || this.pending.has(id) || this.failed.has(id)) continue;
      const scope = this.scope, controller = new AbortController();this.pending.set(id, controller);
      const current = () => !this.disposed && this.ports.current() && !controller.signal.aborted
        && this.scope === scope && this.wanted.has(id) && this.pending.get(id) === controller;
      void this.ports.read(id, controller.signal).then(value => {
        if (!current()) return;
        this.values.set(id, value);this.publish();
      }).catch(error => {
        if (!current()) return;
        this.failed.add(id);this.ports.reportError(error);
      }).finally(() => {
        if (this.pending.get(id) !== controller) return;
        this.pending.delete(id);this.drain();
      });
    }
  }
}
