/** UI operations belong to one captured parent lifetime. The parent also fences
 * the real API/native adapter; this prevents a late completion reopening a surface
 * or reporting an error in the next viewer's screen. No data is persisted here. */
export class SavedOperations {
  private closed = false;
  private pending = new Set<AbortController>();
  constructor(private readonly isCurrent: () => boolean) {}

  get current(): boolean { return !this.closed && this.isCurrent(); }

  async run<T>(operation: (signal: AbortSignal) => Promise<T>, settled?: (result: T) => void): Promise<void> {
    if (!this.current) return;
    const controller = new AbortController();
    this.pending.add(controller);
    try {
      const result = await operation(controller.signal);
      if (this.current && !controller.signal.aborted) settled?.(result);
    } catch (error) {
      if (this.current && !controller.signal.aborted) throw error;
    } finally { this.pending.delete(controller); }
  }

  abortPending(): void {
    for (const controller of this.pending) controller.abort();
    this.pending.clear();
  }

  dispose(): void { this.closed = true; this.abortPending(); }
}
