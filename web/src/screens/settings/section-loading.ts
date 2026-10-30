import type {QuietRefreshOutcome} from '@core/freshness-actions.ts';
import type {QuietInput} from './quiet-refresh';
import type {SettingsPageSnapshot} from './Settings';

type Read = Readonly<{id: string; load: () => Promise<void>}>;
const initial: SettingsPageSnapshot = Object.freeze({loading: true});

/** A failed Settings section must not replace another section or repeat its successful reads. */
export class SettingsSectionReads {
  private readonly states = new Map<string, SettingsPageSnapshot>();
  private readonly sections = new Map<string, Promise<void>>();
  private readonly reads = new Map<string, Promise<void>>();
  private readonly attempted = new Set<string>();
  private readonly loaded = new Set<string>();
  private saveState?: SettingsPageSnapshot['saveState'];
  private closed = false;
  constructor(private readonly isCurrent: () => boolean, private readonly notify: () => void) {}
  private current = () => !this.closed && this.isCurrent();
  snapshot = (section: string): SettingsPageSnapshot => this.current() ? this.states.get(section) ?? initial : initial;
  update(patch: Partial<SettingsPageSnapshot>) {
    if (!this.current()) return;
    if (Object.hasOwn(patch, 'saveState')) this.saveState = patch.saveState;
    for (const [id, state] of this.states) this.states.set(id, Object.freeze({...state, ...patch}));
    this.notify();
  }
  private read(input: Read): Promise<void> {
    if (this.loaded.has(input.id)) return Promise.resolve();
    const running = this.reads.get(input.id);
    if (running) return running;
    this.attempted.add(input.id);
    const task = Promise.resolve().then(() => {if (this.current()) return input.load();}).then(() => {
      if (this.current()) this.loaded.add(input.id);
    }).finally(() => {if (this.reads.get(input.id) === task) this.reads.delete(input.id);});
    this.reads.set(input.id, task);
    return task;
  }
  load(section: string, inputs: readonly Read[]): Promise<void> {
    if (!this.current()) return Promise.resolve();
    const running = this.sections.get(section);
    if (running) return running;
    if (this.states.get(section)?.loading === false && !this.states.get(section)?.error) return Promise.resolve();
    this.states.set(section, Object.freeze({loading: true, saveState: this.saveState}));
    this.notify();
    const task = Promise.all(inputs.map(input => this.read(input))).then(() => {
      if (this.current()) this.states.set(section, Object.freeze({loading: false, saveState: this.saveState}));
    }, error => {
      if (this.current()) this.states.set(section, Object.freeze({loading: false, error, saveState: this.saveState}));
    }).finally(() => {
      if (this.sections.get(section) === task) this.sections.delete(section);
      if (this.current()) this.notify();
    });
    this.sections.set(section, task);
    return task;
  }
  hasRead = (id: string) => this.attempted.has(id);
  async refresh(_section: string, inputs: readonly Pick<QuietInput, 'id' | 'refresh'>[]): Promise<QuietRefreshOutcome> {
    if (!this.current()) return {kind: 'deferred'};
    const outcomes = await Promise.all(inputs.map(async input => {
      try {return await input.refresh();} catch (error) {return {kind: 'unreadable', error} as const;}
    }));
    if (!this.current()) return {kind: 'deferred'};
    return outcomes.find(result => result.kind === 'unreadable') ?? outcomes.find(result => result.kind === 'deferred') ?? outcomes.find(result => result.kind === 'conflict') ?? {kind: 'adopted'};
  }
  dispose() {this.closed = true; this.states.clear(); this.attempted.clear(); this.loaded.clear(); this.reads.clear(); this.sections.clear();}
}
