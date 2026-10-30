import type {QuietRefreshOutcome} from '@core/freshness-actions.ts';
import type {DraftForm, FormState} from '@core/server-admin/draft-form.ts';

/** Keep the form locked through its receipt reread, not just the PUT. The
 * existing form still owns revisions, rebasing and operation identities. */
export class AutoSaveForm<T extends object, S> {
  private timer?: ReturnType<typeof setTimeout>;
  private running = false;
  private disposed = false;
  private listeners = new Set<() => void>();
  private state: FormState<T, S>;
  private off: () => void;
  constructor(private form: DraftForm<T, S>, private current: () => boolean, private failure: (error: unknown) => void, private delay = 500) {
    this.state = form.getSnapshot();
    this.off = form.subscribe(() => this.publish());
  }
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private active() {return !this.disposed && this.current();}
  private publish() {
    if (!this.active()) return;
    this.state = Object.freeze({...this.form.getSnapshot(), saving: this.running || this.form.getSnapshot().saving});
    for (const listener of this.listeners) listener();
  }
  invalidateQuiet = () => this.form.invalidateQuiet();
  quietBlocked = () => !this.active() || this.running || this.timer !== undefined || this.form.getSnapshot().saving || this.form.getSnapshot().saveError !== undefined;
  refresh = async (): Promise<QuietRefreshOutcome> => this.quietBlocked() ? {kind: 'deferred'} : this.form.refresh();
  load = async () => {if (this.active() && !this.running) await this.form.load();};
  set(change: Partial<T> | ((draft: T) => T)) {
    if (!this.active() || this.running) return;
    this.form.set(change);
    clearTimeout(this.timer);
    const state = this.form.getSnapshot();
    if (state.dirty && !state.problem) this.timer = setTimeout(() => {this.timer = undefined;void this.save();}, this.delay);
  }
  discard = () => {if (this.active() && !this.running) {clearTimeout(this.timer);this.timer = undefined;this.form.discard();}};
  save = async () => {
    if (!this.active() || this.running) return;
    clearTimeout(this.timer);this.timer = undefined;
    const state = this.form.getSnapshot();
    if (!state.dirty || state.problem) return;
    this.running = true;this.publish();
    try {await this.form.save();}
    catch (error) {if (this.active()) this.failure(error);}
    finally {this.running = false;this.publish();}
  };
  dispose = () => {if (this.disposed) return;this.disposed = true;clearTimeout(this.timer);this.off();this.form.dispose();this.listeners.clear();};
}
