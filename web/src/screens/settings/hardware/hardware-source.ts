import type {SettingsPageSnapshot} from '../Settings';
import type {ServerRequester} from '@core/query/server-api.ts';
import {readOwnerStreamingHardware, testOwnerStreamingHardware, type OwnerStreamingHardware} from '@core/server-admin/streaming-hardware.ts';

export type HardwareSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error';
  /** What the server found and measured (`GET /v1/admin/hardware`). */
  hardware?: OwnerStreamingHardware;
  /** A test is running: the button shows its spinner and the rows wait for it. */
  testing: boolean;
  /** What the last test in this visit found, for the button's one result line. */
  tested?: 'ok' | 'problem';
}>;

const idle: HardwareSnapshot = Object.freeze({phase: 'idle', testing: false});
/** How often a running test is looked at again, and for how long at most. */
const TEST_POLL_MS = 2000, TEST_POLL_TIMES = 45;

/** One selected server's measured graphics devices, read once per visit (definition 12 rules 31 to 34). */
export class SettingsHardwareSource {
  private state: HardwareSnapshot = idle;
  private readonly listeners = new Set<() => void>();
  private readonly lifetime = new AbortController();
  private loading = false;
  private page?: {base: SettingsPageSnapshot; hardware: HardwareSnapshot; value: SettingsPageSnapshot};
  constructor(private readonly api: ServerRequester, private readonly isCurrent: () => boolean, private readonly reportError: (error: unknown) => void,
    private readonly wait: (ms: number) => Promise<void> = ms => new Promise(resolve => setTimeout(resolve, ms))) {}

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
  /** A signed-out answer or a lost role is the page's; any other failure leaves the rows out. */
  private terminal(error: unknown) {
    const {status, code} = (error && typeof error === 'object' ? error : {}) as {status?: number; code?: string};
    if (status === 401 || status === 403 || ['unauthorized', 'not_permitted', 'forbidden'].includes(code ?? '')) {
      this.lifetime.abort();
      this.state = idle;
      this.reportError(error);
    }
  }

  load = async (): Promise<void> => {
    if (!this.current() || this.loading || this.state.phase === 'ready') return;
    this.loading = true;
    this.publish({phase: 'loading'});
    try {
      const hardware = await readOwnerStreamingHardware(this.api, this.lifetime.signal);
      this.publish({phase: 'ready', hardware});
    } catch (error) {
      if (this.current()) {this.publish({phase: 'error'});this.terminal(error);}
    } finally {
      this.loading = false;
    }
  };

  /** Read again after the page changed a setting the report describes (the Device choice). */
  reload = async (): Promise<void> => {
    if (!this.current()) return;
    try {this.publish({hardware: await readOwnerStreamingHardware(this.api, this.lifetime.signal), phase: 'ready'});}
    catch (error) {if (this.current()) this.terminal(error);}
  };

  /** Test hardware: the server starts one bounded test and says `checking` until it has finished, so the
   * report is read again until then. It never needs a restart (rule 34). */
  test = async (): Promise<void> => {
    if (!this.current() || this.state.testing || this.state.phase !== 'ready') return;
    this.publish({testing: true, tested: undefined});
    try {
      let hardware = await testOwnerStreamingHardware(this.api, this.lifetime.signal);
      for (let n = 0; hardware.checking && n < TEST_POLL_TIMES && this.current(); n++) {
        await this.wait(TEST_POLL_MS);
        if (!this.current()) return;
        hardware = await readOwnerStreamingHardware(this.api, this.lifetime.signal);
      }
      this.publish({hardware, testing: false, tested: hardware.checking ? undefined : hardware.lastError ? 'problem' : 'ok'});
    } catch (error) {
      if (!this.current()) return;
      this.publish({testing: false, tested: undefined});
      this.terminal(error);
    }
  };

  dispose = () => {
    this.lifetime.abort();
    this.state = idle;
    this.listeners.clear();
  };
}
