import type {HomeLayoutRowView, HomeLayoutView} from '../../../../../packages/client-core/src/home';
import type {PersonalResource} from '../../../../../packages/client-core/src/personal-saved';
import type {HomeService} from '../../../../../packages/client-core/src/home-service';
import {HomeLayoutEditor, type HomeLayoutDraft} from '../../../../../packages/client-core/src/home-layout-editor';

export type NativeHomeCustomizeRow = Readonly<Pick<HomeLayoutRowView, 'id' | 'kind' | 'libraryId' | 'title' | 'titleText' | 'availabilityText' | 'required' | 'hideable' | 'reorderable' | 'hidden'> & {savedView?: PersonalResource}>;

export type NativeHomeCustomization = Readonly<{
  phase: 'loading' | 'ready' | 'error';
  rows: readonly NativeHomeCustomizeRow[];
  revision?: number;
  layout?: HomeLayoutView;
  pending: boolean;
  dirty: boolean;
  pickedId?: string;
  pickupOrder?: readonly string[];
  conflict: boolean;
  showConflict: boolean;
  error?: unknown;
  retry?: 'save' | 'reset';
}>;
export type NativeHomeResetOpening = Readonly<{controller: NativeHomeCustomizeController; layout: HomeLayoutView; revision: number}>;
type Ports = Readonly<{
  home: Pick<HomeService, 'loadLayout' | 'saveLayout' | 'resetLayout'>;
  current: () => boolean;
  requestId: () => Promise<string>;
  savedViews?: Readonly<{serverId: string; isCurrent: (view: PersonalResource) => boolean}>;
}>;
const isConflict = (error: unknown) => {
  const failure = error as {status?: number; code?: string} | undefined;
  return ['home_layout_conflict', 'revision_conflict', 'revision_mismatch'].includes(failure?.code ?? '');
};

/** Transient draft only. The existing Home service owns persistence, revision
 * checks and fresh Home publication; a conflict is never automatically retried. */
export class NativeHomeCustomizeController {
  private readonly ports: Ports;
  private editor: HomeLayoutEditor;
  private state: NativeHomeCustomization = Object.freeze({phase: 'loading', rows: [], pending: false, dirty: false, conflict: false, showConflict: false});
  private base?: HomeLayoutView;
  private baseRows: readonly NativeHomeCustomizeRow[] = [];
  private pickedOrder?: readonly string[];
  private pickedDraft?: HomeLayoutDraft;
  private savedViews = new Map<string, PersonalResource>();
  private listeners = new Set<() => void>();
  private generation = 0;
  private command?: {kind: 'save' | 'reset'; rows: readonly NativeHomeCustomizeRow[]; revision: number; generation: number; abort: AbortController; key?: string; accepted: boolean};
  private alive = true;
  constructor(ports: Ports) {
    this.ports = ports;
    this.editor = this.createEditor();
  }
  private createEditor(): HomeLayoutEditor {
    // The canonical editor owns draft edits and command identity. Native keeps its
    // async identity acquisition, captured write lifetime and post-write reload retry.
    const lifetime = () => {
      const command = this.command!;
      return {signal: command.abort.signal, isCurrent: () => this.current() && this.command === command && command.generation === this.generation && !command.abort.signal.aborted};
    };
    return new HomeLayoutEditor({
      loadLayout: signal => this.ports.home.loadLayout(signal),
      saveLayout: (order, hidden, requestId, revision) => this.ports.home.saveLayout(order, hidden, requestId, revision, lifetime()),
      resetLayout: (requestId, revision) => this.ports.home.resetLayout(requestId, revision, lifetime()),
    }, {isCurrent: this.current, requestId: () => this.command!.key!});
  }
  getSnapshot = (): NativeHomeCustomization => this.state;
  subscribe = (listener: () => void): (() => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  isCurrent = (): boolean => this.current();
  private current = () => this.alive && this.ports.current();
  private editable = () => this.current() && this.state.phase === 'ready' && !this.state.pending && !this.state.retry;
  private publish(patch: Partial<NativeHomeCustomization>): void {
    if (!this.current()) return;
    const next = {...this.state, ...patch};
    const base = this.baseRows;
    next.dirty = !!this.base && (next.rows.some((row, index) => base[index]?.id !== row.id || base[index]?.hidden !== row.hidden) || next.rows.length !== base.length);
    this.state = Object.freeze(next);
    for (const listener of [...this.listeners]) listener();
  }
  load = async (): Promise<void> => {
    if (!this.current() || this.state.pending) return;
    const generation = ++this.generation;
    this.command?.abort.abort();this.command = undefined;
    // An explicit reload abandons the old command identity as well as its draft.
    this.editor.dispose(); this.editor = this.createEditor();
    const editor = this.editor;
    this.publish({phase: 'loading', error: undefined, retry: undefined, conflict: false, showConflict: false, pickedId: undefined, pickupOrder: undefined});
    await editor.load();
    if (!this.current() || generation !== this.generation) return;
    const state = editor.getSnapshot();
    if (state.phase === 'ready' && state.draft) this.accept(state.draft.view);
    else this.publish({phase: 'error', error: state.error});
  };
  private draftRows(): readonly NativeHomeCustomizeRow[] {
    const draft = this.editor.getSnapshot().draft;
    if (!draft) return this.state.rows;
    return Object.freeze(draft.order.map(id => {
      const view = draft.added.get(id);
      const row: NativeHomeCustomizeRow = view
        ? {id, kind: 'saved_view', libraryId: view.definition!.libraryId, title: view.name, required: false, hideable: true, reorderable: true, hidden: false}
        : draft.view.rows.find(value => value.id === id)!;
      const savedView = this.savedViews.get(id) ?? view;
      return Object.freeze({...row, hidden: draft.hidden.has(id), ...(savedView ? {savedView} : {})});
    }));
  }
  private accept(layout: HomeLayoutView): void {
    this.base = layout; this.pickedOrder = undefined; this.pickedDraft = undefined; this.savedViews.clear();
    this.baseRows = this.draftRows();
    this.publish({phase: 'ready', rows: this.baseRows, revision: layout.revision, layout,
      pickedId: undefined, pickupOrder: undefined, pending: false, conflict: false, showConflict: false, error: undefined, retry: undefined});
  }
  /** Adds the current server resource to the transient layout only. Save is
   * the sole mutation; this never issues a separate pin or changes Saved.
   * Frame 29: on TV the list returns with the new row held so it moves at once. */
  addSavedView = (view: PersonalResource, hold = false): boolean => {
    const access = this.ports.savedViews;
    if (!this.editable() || this.state.pickedId || this.state.rows.length >= 64 || !access || view.serverId !== access.serverId || !access.isCurrent(view)
      || view.kind !== 'view' || view.status !== 'ready' || !view.definition || view.onHome || !view.actions.includes('pin')) return false;
    const id = `view:${view.id}`;
    if (this.state.rows.some(row => row.id === id)) return false;
    this.editor.add(view);
    const rows = this.draftRows();
    if (!hold) {this.publish({rows}); return true;}
    this.pickedOrder = Object.freeze(rows.map(value => value.id));
    this.pickedDraft = this.editor.getSnapshot().draft;
    this.publish({rows, pickedId: id, pickupOrder: this.pickedOrder}); return true;
  };
  private currentSavedViews(rows: readonly NativeHomeCustomizeRow[]): boolean {
    return rows.every(row => !row.savedView || this.ports.savedViews?.isCurrent(row.savedView));
  }
  /** Takes a saved view off Home in the draft. The layout write is still the only mutation:
   * the server builds a saved view's row from the order it is sent, so a row left out of the
   * order is off Home on the next read, and the view's own On Home mark follows. */
  removeSavedView = (id: string): boolean => {
    const row = this.state.rows.find(value => value.id === id);
    if (!this.editable() || this.state.pickedId || !row?.savedView) return false;
    this.pickedOrder = undefined;
    this.editor.remove(id);
    this.savedViews.delete(id);
    this.publish({rows: this.draftRows(), pickedId: undefined, pickupOrder: undefined});
    return true;
  };
  toggle = (id: string): boolean => {
    const row = this.state.rows.find(value => value.id === id);
    if (!this.editable() || this.state.pickedId || !row || row.required || !row.hideable) return false;
    this.editor.show(id, row.hidden);
    this.publish({rows: this.draftRows()});
    return true;
  };
  pick = (id: string): boolean => {
    if (!this.editable() || !this.state.rows.some(row => row.id === id && row.reorderable)) return false;
    if (this.state.pickedId === id) {this.drop(); return true;}
    if (this.state.pickedId) return false;
    this.pickedDraft = this.editor.getSnapshot().draft;
    this.pickedOrder = Object.freeze(this.state.rows.map(row => row.id)); this.publish({pickedId: id, pickupOrder: this.pickedOrder}); return true;
  };
  move = (direction: -1 | 1): boolean => {
    if (!this.editable() || !this.state.pickedId) return false;
    const rows = this.state.rows, from = rows.findIndex(row => row.id === this.state.pickedId), to = from + direction;
    if (from < 0 || !rows[to]?.reorderable) return false;
    this.editor.move(this.state.pickedId, to);
    this.publish({rows: this.draftRows()}); return true;
  };
  drop = (): void => {if (this.editable()) {this.pickedOrder = undefined; this.pickedDraft = undefined; this.publish({pickedId: undefined, pickupOrder: undefined});}};
  cancelPickup = (): boolean => {
    if (!this.editable() || !this.state.pickedId || !this.pickedOrder) return false;
    if (!this.pickedDraft) return false;
    this.editor.restoreDraft(this.pickedDraft);
    this.pickedOrder = undefined; this.pickedDraft = undefined; this.publish({rows: this.draftRows(), pickedId: undefined, pickupOrder: undefined}); return true;
  };
  /** Dismiss the conflict notice and return to the draft. `conflict` stays set,
   * so Save remains blocked and only Reload can accept the server revision. */
  keepEditing = (): void => {if (this.editable()) this.publish({showConflict: false});};
  bindSavedViews = (views: readonly PersonalResource[]): void => {
    if (!this.current() || this.state.phase !== 'ready') return;
    const byId = new Map(views.map(view => [`view:${view.id}`, view]));
    let changed = false;
    const rows = this.state.rows.map(row => {
      const view = byId.get(row.id);
      if (row.kind !== 'saved_view' || !view || !this.ports.savedViews?.isCurrent(view) || row.savedView === view) return row;
      changed = true; this.savedViews.set(row.id, view);
      return Object.freeze({...row, savedView: view});
    });
    if (changed) this.publish({rows: Object.freeze(rows)});
  };
  save = async (): Promise<boolean> => {
    if (this.command) return this.command.kind === 'save' ? this.retry() : false;
    if (!this.editable() || this.state.conflict || !this.state.dirty || this.state.pickedId || !this.base) return false;
    this.command = {kind: 'save', rows: this.state.rows, revision: this.base.revision, generation: this.generation, abort: new AbortController(), accepted: false};
    return this.retry();
  };
  captureReset = (layout: HomeLayoutView | undefined): NativeHomeResetOpening | undefined => {
    if (!this.editable() || this.state.conflict || this.state.pickedId || !layout || layout !== this.base) return;
    return Object.freeze({controller: this, layout, revision: layout.revision});
  };
  /** Confirm only the exact controller/document/revision this opening drew. */
  resetConfirmed = async (opening: NativeHomeResetOpening): Promise<boolean> => {
    if (!this.editable() || this.state.conflict || this.state.pickedId || !this.base || opening.controller !== this || opening.layout !== this.base || opening.revision !== this.base.revision) return false;
    this.command = {kind: 'reset', rows: this.state.rows, revision: opening.revision, generation: this.generation, abort: new AbortController(), accepted: false};
    return this.retry();
  };
  retry = async (): Promise<boolean> => {
    const command = this.command;
    if (!command || !this.current() || this.state.pending) return false;
    const current = () => this.current() && this.command === command && command.generation === this.generation && !command.abort.signal.aborted;
    this.publish({pending: true, error: undefined, retry: undefined});
    try {
      command.key ??= await this.ports.requestId();
      if (!current()) return false;
      if (!command.accepted) {
        if (command.kind === 'save' && !this.currentSavedViews(command.rows)) throw Object.assign(new Error('Saved view is no longer current'), {code: 'unavailable'});
        const accepted = await (command.kind === 'save' ? this.editor.save() : this.editor.reset());
        if (!current()) return false;
        if (!accepted) throw this.editor.getSnapshot().error;
        command.accepted = true;
      }
      if (!current()) return false;
      await this.editor.load();
      if (!current()) return false;
      const loaded = this.editor.getSnapshot();
      if (loaded.phase !== 'ready' || !loaded.draft) throw loaded.error;
      this.command = undefined;this.accept(loaded.draft.view);return true;
    } catch (error) {
      if (current()) {
        const conflict = isConflict(error);
        if (conflict) this.command = undefined;
        this.publish({pending: false, error, conflict, showConflict: conflict, retry: conflict ? undefined : command.kind});
      }
      return false;
    }
  };
  dispose = (): void => {this.alive = false; this.generation++; this.editor.dispose(); this.command?.abort.abort();this.command = undefined;this.listeners.clear(); this.base = undefined; this.pickedOrder = undefined; this.pickedDraft = undefined; this.savedViews.clear();};
}
