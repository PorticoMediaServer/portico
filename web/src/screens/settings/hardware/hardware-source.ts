import type {TranscodeCapacity} from '@core/telemetry.ts';
import type {SettingsPageSnapshot} from '../Settings';
import {readHardwareInventory, readTranscodeCapacity, testHardware, type HardwareApi, type HardwareInventory} from '@core/hardware.ts';

export type HardwareSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error';
  capacity?: TranscodeCapacity;
  inventory?: HardwareInventory;
  /** A test is running: the button shows its spinner and the rows wait for it. */
  testing: boolean;
  /** What the last test in this visit found, for the button's one result line. */
  tested?: 'ok' | 'problem';
}>;

const idle: HardwareSnapshot = Object.freeze({phase: 'idle', testing: false});

/** One selected server's measured hardware, read once per visit. The install's
 * platform and remedy cannot change while the server runs, so they are read
 * beside the capacity document and kept for this visit. */
export class SettingsHardwareSource {
  private state: HardwareSnapshot = idle;
  private readonly listeners = new Set<() => void>();
  private readonly lifetime = new AbortController();
  private read?: AbortController;
  private loading = false;
  private page?: {base: SettingsPageSnapshot; hardware: HardwareSnapshot; value: SettingsPageSnapshot};
  constructor(private readonly api: HardwareApi, private readonly isCurrent: () => boolean, private readonly reportError: (error: unknown) => void) {}

  private current() {return !this.lifetime.signal.aborted && this.isCurrent();}
  getSnapshot = (): HardwareSnapshot => this.current() ? this.state : idle;
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private publish(patch: Partial<HardwareSnapshot>) {
    if (!this.current()) return;
    this.state = Object.freeze({...this.state, ...patch});
    for (const listener of [...this.listeners]) listener();
  }
  /** The page's snapshot for this section. It changes only when the controller's
   * state or these measured facts do, so the screen repaints once per read and
   * React never sees a new object on every render. */
  pageSnapshot = (base: SettingsPageSnapshot): SettingsPageSnapshot => {
    const hardware = this.getSnapshot();
    if (!this.page || this.page.base !== base || this.page.hardware !== hardware)
      this.page = {base, hardware, value: Object.freeze({...base})};
    return this.page.value;
  };
  private terminal(error: unknown) {
    const {status, code} = (error && typeof error === 'object' ? error : {}) as {status?: number; code?: string};
    if (status === 401 || status === 403 || ['unauthorized', 'not_permitted', 'forbidden'].includes(code ?? '')) {
      this.read?.abort();
      this.lifetime.abort();
      this.state = idle;
      this.reportError(error);
    }
  }

  /** The drawn rows need both answers; a failure keeps the page readable and
   * lets the owner retry rather than drawing half a hardware section. */
  load = async (): Promise<void> => {
    if (!this.current() || this.loading || this.state.phase === 'ready') return;
    this.loading = true;
    this.read?.abort();
    const read = new AbortController();
    this.read = read;
    this.publish({phase: 'loading'});
    try {
      const [capacity, inventory] = await Promise.all([readTranscodeCapacity(this.api, read.signal), readHardwareInventory(this.api, read.signal)]);
      if (!this.current() || read.signal.aborted) return;
      this.publish({phase: 'ready', capacity, inventory});
    } catch (error) {
      if (this.current() && !read.signal.aborted) {
        this.publish({phase: 'error'});
        this.terminal(error);
      }
    } finally {
      this.loading = false;
    }
  };

  /** Read the capacity document again after the page itself changed a setting it
   * describes, so the drawn choice matches what the server now stores. */
  reload = async (): Promise<void> => {
    if (!this.current()) return;
    const read = new AbortController();
    this.read?.abort();
    this.read = read;
    try {
      const capacity = await readTranscodeCapacity(this.api, read.signal);
      if (this.current() && !read.signal.aborted) this.publish({capacity, phase: 'ready'});
    } catch (error) {
      if (this.current() && !read.signal.aborted) this.terminal(error);
    }
  };

  /** Probe every adapter and backend again now. The server bounds the work, so
   * this waits for one answer instead of polling. */
  test = async (): Promise<void> => {
    if (!this.current() || this.state.testing || this.state.phase !== 'ready') return;
    this.publish({testing: true, tested: undefined});
    try {
      const capacity = await testHardware(this.api, this.lifetime.signal);
      if (!this.current()) return;
      this.publish({capacity, testing: false, tested: capacity.hardware.lastError ? 'problem' : 'ok'});
    } catch (error) {
      if (!this.current()) return;
      this.publish({testing: false, tested: undefined});
      this.terminal(error);
    }
  };

  dispose = () => {
    this.read?.abort();
    this.lifetime.abort();
    this.state = idle;
    this.listeners.clear();
  };
}
