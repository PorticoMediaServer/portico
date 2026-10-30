/** The Home layout editor both clients draw: one dialog-owned draft and one command at a time.
 * The service stays the only layout producer and writer; this owns the revision the editor loaded
 * and never re-reads it before a write, so a save or reset made elsewhere surfaces as a conflict. */
import type {HomeService} from './home-service.ts';
import {homeViewResourceId, homeViewRowId, isPersonalRowId, type HomeLayoutView} from './home.ts';
import type {PersonalResource} from './personal-saved.ts';

/** A dialog draft keeps the exact published revision until an explicit reload. */
export type HomeLayoutDraft = Readonly<{
  view: HomeLayoutView;
  order: readonly string[];
  hidden: ReadonlySet<string>;
  added: ReadonlyMap<string, PersonalResource>;
}>;
export function homeLayoutDraft(view: HomeLayoutView): HomeLayoutDraft {
  return {view, order: view.rows.filter(row => !isPersonalRowId(row.id)).map(row => row.id), hidden: new Set(view.hiddenRowIds), added: new Map()};
}
export function homeDraftRow(draft: HomeLayoutDraft, id: string) {
  return draft.view.rows.find(row => row.id === id);
}
export function homeDraftCanMove(draft: HomeLayoutDraft, id: string): boolean {
  return draft.added.has(id) || homeDraftRow(draft, id)?.reorderable === true;
}
export function homeDraftCanHide(draft: HomeLayoutDraft, id: string): boolean {
  const row = homeDraftRow(draft, id);
  return draft.added.has(id) || !!row && row.hideable && !row.required;
}
/** Fixed rows keep their slots; only server-reorderable rows exchange places. */
export function moveHomeDraftRow(draft: HomeLayoutDraft, id: string, target: number): HomeLayoutDraft {
  if (!homeDraftCanMove(draft, id) || target < 0 || target >= draft.order.length) return draft;
  const targetId = draft.order[target];
  if (!homeDraftCanMove(draft, targetId)) return draft;
  const movable = draft.order.filter(value => homeDraftCanMove(draft, value)), from = movable.indexOf(id), to = movable.indexOf(targetId);
  if (from < 0 || from === to) return draft;
  movable.splice(to, 0, ...movable.splice(from, 1));
  let at = 0;
  return {...draft, order: draft.order.map(value => homeDraftCanMove(draft, value) ? movable[at++] : value)};
}
export function showHomeDraftRow(draft: HomeLayoutDraft, id: string, shown: boolean): HomeLayoutDraft {
  if (!homeDraftCanHide(draft, id)) return draft;
  const hidden = new Set(draft.hidden);
  if (shown) hidden.delete(id); else hidden.add(id);
  return {...draft, hidden};
}
export function addHomeDraftView(draft: HomeLayoutDraft, resource: PersonalResource): HomeLayoutDraft {
  if (resource.kind !== 'view' || resource.status !== 'ready' || !resource.definition) return draft;
  const id = homeViewRowId(resource.id);
  if (draft.order.includes(id)) return draft;
  return {...draft, order: [...draft.order, id], added: new Map(draft.added).set(id, resource)};
}
export function removeHomeDraftView(draft: HomeLayoutDraft, id: string): HomeLayoutDraft {
  if (!homeViewResourceId(id)) return draft;
  const hidden = new Set(draft.hidden), added = new Map(draft.added);
  hidden.delete(id);added.delete(id);
  return {...draft, order: draft.order.filter(value => value !== id), hidden, added};
}
export function homeDraftIntent(draft: HomeLayoutDraft) {
  return {expectedRevision: draft.view.revision,
    rowOrder: draft.order.filter(id => homeDraftCanMove(draft, id)),
    hiddenRowIds: [...draft.hidden].filter(id => {
      const row = homeDraftRow(draft, id);
      if (row && !homeDraftCanHide(draft, id)) return false;
      return !homeViewResourceId(id) || draft.order.includes(id);
    })};
}

export type HomeLayoutEditorBindings = Readonly<{
  /** A fresh value for each distinct command; reused only when that same command is retried. */
  requestId: () => string;
  /** The captured authority this dialog was opened under. */
  isCurrent: () => boolean;
}>;
export type HomeLayoutEditorState = Readonly<{
  phase: 'loading' | 'ready' | 'load-error';
  draft?: HomeLayoutDraft;
  pending?: 'save' | 'reset';
  error?: unknown;
  /** Only an explicit successful layout reload clears a save or reset conflict. */
  conflict: boolean;
}>;
export type HomeLayoutEditorService = Pick<HomeService, 'loadLayout' | 'saveLayout' | 'resetLayout'>;
type Command = Readonly<{kind: 'save' | 'reset'; key: string; requestId: string}>;

const conflicted = (error: unknown) => (error as {code?: unknown} | null)?.code === 'home_layout_conflict';

/** One open dialog. The actual HomeService remains the sole layout producer/writer. */
export class HomeLayoutEditor {
  private state: HomeLayoutEditorState = Object.freeze({phase: 'loading', conflict: false});
  private listeners = new Set<() => void>();
  private life = new AbortController();
  private read?: AbortController;
  private epoch = 0;
  /** The last command's identity, kept until that command succeeds, so a retry reuses its request id. */
  private command?: Command;
  private readonly service: HomeLayoutEditorService;
  private readonly bindings: HomeLayoutEditorBindings;
  constructor(service: HomeLayoutEditorService, bindings: HomeLayoutEditorBindings) {this.service = service;this.bindings = bindings;}
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private current = () => !this.life.signal.aborted && this.bindings.isCurrent();
  private publish(patch: Partial<HomeLayoutEditorState>) {
    if (!this.current()) return;
    this.state = Object.freeze({...this.state, ...patch});
    for (const listener of this.listeners) listener();
  }
  private requestId(kind: Command['kind'], key: string): string {
    const retried = this.command;
    if (retried && retried.kind === kind && retried.key === key) return retried.requestId;
    return (this.command = Object.freeze({kind, key, requestId: this.bindings.requestId()})).requestId;
  }
  private settled(requestId: string) {if (this.command?.requestId === requestId) this.command = undefined;}
  load = async () => {
    if (!this.current() || this.state.pending) return;
    this.read?.abort();const read = new AbortController(), epoch = ++this.epoch;this.read = read;
    this.publish({phase: 'loading', draft: undefined, error: undefined});
    try {
      const view = await this.service.loadLayout(read.signal);
      if (this.current() && epoch === this.epoch && !read.signal.aborted) this.publish({phase: 'ready', draft: homeLayoutDraft(view), conflict: false});
    } catch (error) {
      if (this.current() && epoch === this.epoch && !read.signal.aborted) this.publish({phase: 'load-error', error});
    } finally {if (this.read === read) this.read = undefined;}
  };
  private edit(change: (draft: HomeLayoutDraft) => HomeLayoutDraft) {
    if (this.current() && this.state.phase === 'ready' && this.state.draft && !this.state.pending) this.publish({draft: change(this.state.draft)});
  }
  move = (id: string, target: number) => this.edit(draft => moveHomeDraftRow(draft, id, target));
  show = (id: string, shown: boolean) => this.edit(draft => showHomeDraftRow(draft, id, shown));
  add = (resource: PersonalResource) => this.edit(draft => addHomeDraftView(draft, resource));
  remove = (id: string) => this.edit(draft => removeHomeDraftView(draft, id));
  restoreDraft = (draft: HomeLayoutDraft) => this.edit(current => current.view === draft.view ? draft : current);
  save = async (): Promise<boolean> => {
    if (!this.current() || this.state.phase !== 'ready' || !this.state.draft || this.state.pending || this.state.conflict) return false;
    const intent = homeDraftIntent(this.state.draft), epoch = this.epoch;
    const requestId = this.requestId('save', JSON.stringify([intent.expectedRevision, intent.rowOrder, intent.hiddenRowIds]));
    this.publish({pending: 'save', error: undefined});
    try {
      await this.service.saveLayout(intent.rowOrder, intent.hiddenRowIds, () => requestId, intent.expectedRevision);
      this.settled(requestId);
      return this.current() && epoch === this.epoch;
    } catch (error) {
      if (this.current() && epoch === this.epoch) this.publish({error, conflict: conflicted(error)});
      return false;
    } finally {if (epoch === this.epoch) this.publish({pending: undefined});}
  };
  /** Called only after the drawn Reset confirmation. */
  reset = async (): Promise<boolean> => {
    if (!this.current() || this.state.phase !== 'ready' || !this.state.draft || this.state.pending || this.state.conflict) return false;
    const revision = this.state.draft.view.revision, epoch = this.epoch, requestId = this.requestId('reset', String(revision));
    this.publish({pending: 'reset', error: undefined});
    try {await this.service.resetLayout(() => requestId, revision);this.settled(requestId);return this.current() && epoch === this.epoch;}
    catch (error) {if (this.current() && epoch === this.epoch) this.publish({error, conflict: conflicted(error)});return false;}
    finally {if (epoch === this.epoch) this.publish({pending: undefined});}
  };
  dispose() {this.life.abort();this.read?.abort();this.epoch++;this.listeners.clear();}
}
