/** Owns one initialization and its mounted application. Closing is terminal:
 * a late native installation response cannot mount a new app after teardown. */
export class AppLifetime {
  private readonly controller = new AbortController();
  private starting?: Promise<void>;
  private unmount?: () => void;
  readonly signal = this.controller.signal;

  start(initialize: (signal: AbortSignal) => Promise<unknown>, mount: () => () => void): Promise<void> {
    if (this.signal.aborted) return Promise.resolve();
    return this.starting ??= this.open(initialize, mount);
  }
  private async open(initialize: (signal: AbortSignal) => Promise<unknown>, mount: () => () => void) {
    try {
      await initialize(this.signal);
      if (this.signal.aborted) return;
      const unmount = mount();
      if (this.signal.aborted) unmount();
      else this.unmount = unmount;
    } catch (error) {
      if (!this.signal.aborted) throw error;
    }
  }
  dispose = () => {
    if (this.signal.aborted) return;
    this.controller.abort();
    const unmount = this.unmount; this.unmount = undefined;
    unmount?.();
  };
}
