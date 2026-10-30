/**
 * The save model of the Server pages (Justin, 2 Oct 2026): server settings are consequential, so
 * they are edited as a draft and saved explicitly, with **one** Save / Discard for the whole
 * page. A `DraftForm` is one settings document (load, edit, dirty, save, discard); a `FormSet`
 * is a page's forms behind one `dirty / save() / discard()`. No rendering: the web's sticky bar
 * and the iPhone's toolbar both sit on a `FormSet`.
 *
 * A document that moved on the server while it was being edited is not the owner's problem: what
 * they did not touch takes the server's value, what they changed stays theirs, and a save the
 * server calls out of date is read again and sent again by itself (`freshness-actions.ts`). Only
 * a setting they changed that was also changed elsewhere, to something else, is ever reported.
 */
import {ALREADY_DONE, rebaseDraft, retryWhenStale, type QuietRefreshOutcome} from '../freshness-actions.ts';

export type FormState<T, S = undefined> = Readonly<{
  /** The first read has not answered yet. */
  loading: boolean;
  /** The server has no such document (a feature that is not set up): the form's rows are absent. */
  unavailable: boolean;
  /** Why the read failed; `saved` keeps the last good value beside it. */
  loadError?: unknown;
  /** What the server has. */
  saved?: T;
  /** What the owner has typed; equals `saved` until something is edited. */
  draft?: T;
  /** Read-only facts that arrive with the document (status, effective values, choices). */
  status?: S;
  dirty: boolean;
  saving: boolean;
  /** Why the last save failed; cleared by the next edit or save. */
  saveError?: unknown;
  /** A plain-words reason the draft cannot be saved as it stands (authored by the form). */
  problem?: string;
}>;

export type Loaded<T, S> = Readonly<{value: T; status?: S}>;

export type DraftFormOptions<T, S> = Readonly<{
  /** `null`: the server has no such document. */
  load: (signal: AbortSignal) => Promise<Loaded<T, S> | null>;
  /** Sends the draft; may answer with the stored document (otherwise the form reads it again). */
  save: (draft: T, saved: T, status: S | undefined) => Promise<Loaded<T, S> | void>;
  /** Why this draft cannot be saved, in words for the owner, or nothing. */
  validate?: (draft: T, status: S | undefined) => string | undefined;
}>;

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);

export class DraftForm<T extends object, S = undefined> {
  private state: FormState<T, S> = Object.freeze({loading: true, unavailable: false, dirty: false, saving: false});
  private listeners = new Set<() => void>();
  private generation = 0;
  private refreshSerial = 0;
  private refreshRead?: AbortController;
  private controller?: AbortController;
  private disposed = false;
  private readonly o: DraftFormOptions<T, S>;

  constructor(options: DraftFormOptions<T, S>) {
    this.o = options;
  }

  getSnapshot = (): FormState<T, S> => this.state;
  subscribe = (fn: () => void) => { this.listeners.add(fn); return () => { this.listeners.delete(fn); }; };

  private publish(patch: Partial<FormState<T, S>>) {
    if (this.disposed) return;
    const next = {...this.state, ...patch};
    const dirty = next.saved !== undefined && next.draft !== undefined && !same(next.saved, next.draft);
    const problem = dirty && next.draft !== undefined ? this.o.validate?.(next.draft, next.status) : undefined;
    this.state = Object.freeze({...next, dirty, problem});
    for (const fn of this.listeners) fn();
  }

  private adopt(loaded: Loaded<T, S> | null, keepDraft: boolean) {
    if (loaded === null) { this.publish({loading: false, unavailable: true, loadError: undefined, saved: undefined, draft: undefined, status: undefined}); return; }
    // While editing, the draft is carried onto the newer document: the owner's own changes stay,
    // and nobody else's change turns into a local edit when this form next saves.
    const editing = keepDraft && this.state.dirty && this.state.saved !== undefined && this.state.draft !== undefined;
    const draft = editing ? rebaseDraft(this.state.saved as T, this.state.draft as T, loaded.value).merged : loaded.value;
    this.publish({loading: false, unavailable: false, loadError: undefined, saved: loaded.value, status: loaded.status, draft});
  }

  private invalidateRefresh() {
    this.refreshSerial++;
    this.refreshRead?.abort();
    this.refreshRead = undefined;
  }

  /** A mounted buffered input cancels adoption before its local edit reaches set(). */
  invalidateQuiet = (): void => {this.invalidateRefresh();};

  /** Reads without replacing the edit's base on conflict or acknowledging a write. */
  async refresh(): Promise<QuietRefreshOutcome> {
    if (this.disposed || this.state.saving || this.controller !== undefined) return {kind: 'deferred'};
    this.invalidateRefresh();
    const serial = this.refreshSerial, saved = this.state.saved, status = this.state.status;
    const read = this.refreshRead = new AbortController();
    const owns = () => !this.disposed && !read.signal.aborted && serial === this.refreshSerial &&
      !this.state.saving && this.state.saved === saved && this.state.status === status;
    try {
      const loaded = await this.o.load(read.signal);
      if (!owns()) return {kind: 'deferred'};
      // Rebase against the draft at completion, including edits made during this read.
      const draft = this.state.draft;
      if (loaded === null && this.state.dirty) return {kind: 'conflict'};
      if (loaded !== null && saved !== undefined && draft !== undefined) {
        const rebased = rebaseDraft(saved, draft, loaded.value);
        if (rebased.conflict) return {kind: 'conflict'};
        this.publish({loading: false, unavailable: false, loadError: undefined,
          saved: loaded.value, status: loaded.status, draft: rebased.merged});
      } else this.adopt(loaded, false);
      return {kind: 'adopted'};
    } catch (error) {
      return owns() ? {kind: 'unreadable', error} : {kind: 'deferred'};
    } finally {
      if (this.refreshRead === read) this.refreshRead = undefined;
    }
  }

  /** Reads the document. A failure keeps what was loaded before. */
  async load(): Promise<void> {
    this.invalidateRefresh();
    const generation = ++this.generation;
    this.controller?.abort();
    const controller = this.controller = new AbortController();
    if (this.state.saved === undefined) this.publish({loading: true, loadError: undefined});
    try {
      const loaded = await this.o.load(controller.signal);
      if (generation === this.generation) this.adopt(loaded, true);
    } catch (error) {
      if (generation === this.generation && !controller.signal.aborted) this.publish({loading: false, loadError: error});
    } finally {
      if (this.controller === controller) this.controller = undefined;
    }
  }

  /** Edits the draft: a partial, or a function of the current draft. */
  set(change: Partial<T> | ((draft: T) => T)) {
    const draft = this.state.draft;
    if (draft === undefined || this.state.saving) return;
    this.publish({draft: typeof change === 'function' ? change(draft) : {...draft, ...change}, saveError: undefined});
  }

  discard() {
    this.invalidateRefresh();
    if (this.state.saved !== undefined && !this.state.saving) this.publish({draft: this.state.saved, saveError: undefined});
  }

  /**
   * Saves a dirty, valid draft. An out-of-date answer is met by reading the document again,
   * carrying the draft onto it and sending it again, silently. Rejects with the server's failure,
   * which is also kept in `saveError`; after a real conflict the form holds the newer document
   * with the owner's own changes still in the draft, so saving again is a deliberate choice.
   */
  async save(): Promise<void> {
    const {saved, draft, status, dirty, saving, problem} = this.state;
    if (!dirty || saving || saved === undefined || draft === undefined) return;
    if (problem) throw new Error(problem);
    this.invalidateRefresh();
    this.publish({saving: true, saveError: undefined});
    let base = saved, sending = draft, sendingStatus = status, conflict = false, gone = false, moved = false;
    try {
      const stored = await retryWhenStale(() => this.o.save(sending, base, sendingStatus), {
        reread: async () => {
          const fresh = await this.o.load(new AbortController().signal);
          if (fresh === null) { gone = true; return; }
          const rebased = rebaseDraft(base, sending, fresh.value);
          base = fresh.value; sending = rebased.merged; sendingStatus = fresh.status; conflict = rebased.conflict; moved = true;
        },
        stillApplies: () => (gone || conflict ? false : same(sending, base) ? 'done' : true),
      });
      if (stored === ALREADY_DONE) this.publish({saving: false, saved: base, status: sendingStatus, draft: base});
      else if (stored) this.publish({saving: false, saved: stored.value, status: stored.status ?? sendingStatus, draft: stored.value});
      else {
        // The write did not answer with the document: what was sent is what is stored, until the reread says otherwise.
        this.publish({saving: false, saved: sending, status: sendingStatus, draft: sending});
        await this.load();
      }
    } catch (error) {
      // What the re-read brought stays: the next Save is made against the document as it is now.
      this.publish(moved && !gone ? {saving: false, saveError: error, saved: base, status: sendingStatus, draft: sending} : {saving: false, saveError: error});
      throw error;
    }
  }

  dispose() {
    this.invalidateRefresh();
    this.disposed = true;
    this.controller?.abort();
    this.listeners.clear();
  }
}

export type AnyDraftForm = Pick<DraftForm<object, unknown>, 'getSnapshot' | 'subscribe' | 'load' | 'discard' | 'save' | 'dispose'>;

export type FormSetState = Readonly<{dirty: boolean; saving: boolean; /** The first form that cannot be saved as it stands, in words. */ problem?: string; /** The failure of the last Save, if any. */ saveError?: unknown}>;

/** A page's forms behind one Save and one Discard. */
export class FormSet {
  private forms = new Map<string, AnyDraftForm>();
  private offs = new Map<string, () => void>();
  private listeners = new Set<() => void>();
  private state: FormSetState = Object.freeze({dirty: false, saving: false});
  private saveError: unknown;

  getSnapshot = (): FormSetState => this.state;
  subscribe = (fn: () => void) => { this.listeners.add(fn); return () => { this.listeners.delete(fn); }; };

  private recompute = () => {
    const states = [...this.forms.values()].map(f => f.getSnapshot());
    const next: FormSetState = {
      dirty: states.some(s => s.dirty),
      saving: states.some(s => s.saving),
      problem: states.find(s => s.dirty && s.problem)?.problem,
      saveError: states.find(s => s.saveError !== undefined)?.saveError ?? this.saveError,
    };
    if (next.dirty === this.state.dirty && next.saving === this.state.saving && next.problem === this.state.problem && next.saveError === this.state.saveError) return;
    this.state = Object.freeze(next);
    for (const fn of this.listeners) fn();
  };

  /** Adds a form under an id; adding the same id again replaces it. Returns the remover. */
  add(id: string, form: AnyDraftForm): () => void {
    this.offs.get(id)?.();
    this.forms.set(id, form);
    this.offs.set(id, form.subscribe(this.recompute));
    this.recompute();
    return () => {
      if (this.forms.get(id) !== form) return;
      this.offs.get(id)?.();
      this.offs.delete(id);
      this.forms.delete(id);
      this.recompute();
    };
  }

  get<F extends AnyDraftForm>(id: string): F | undefined {
    return this.forms.get(id) as F | undefined;
  }

  discard() {
    this.saveError = undefined;
    for (const form of this.forms.values()) form.discard();
    this.recompute();
  }

  /** Saves every dirty form, one after another. A failure stops there: what was saved stays saved, the rest stays a draft. */
  async save(): Promise<boolean> {
    this.saveError = undefined;
    if (this.state.problem) return false;
    for (const form of this.forms.values()) {
      if (!form.getSnapshot().dirty) continue;
      try { await form.save(); } catch (error) { this.saveError = error; this.recompute(); return false; }
    }
    this.recompute();
    return true;
  }
}
