import {ReadingError, type LocalSession, type ReadingClient, type ReadingItem} from '../../../../../packages/client-core/src';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {sameAuthority, type ShellAuthority} from '../../shell/model';
import {nativeReadOrigin, openNativeReadBook, type NativeReadOrigin} from './eligibility';

export type NativeReadingLibraryState = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error';
  items: readonly ReadingItem[];
  nextCursor?: string;
  error?: unknown;
}>;

const refused = () => Object.assign(new Error('native_reading_library_refused'), {name: 'AbortError', code: 'cancelled'});

/** The bounded Reading list for a book or comic library (`GET /v1/reading/items`),
 * and the one existing Read door for opening a resident item. Never the generic
 * browse content endpoint, which has no reading hierarchy. */
export class NativeReadingLibrary {
  private readonly context: NativeShellBootstrapContext;
  private readonly libraryId: string;
  private readonly authority: ShellAuthority;
  private readonly captured?: Readonly<{sessionFamilyId: string; viewer: LocalSession['viewer']}>;
  private readonly life = new AbortController();
  private client?: ReadingClient;
  private origin?: NativeReadOrigin;
  private state: NativeReadingLibraryState = Object.freeze({phase: 'idle', items: []});
  private listeners = new Set<() => void>();
  private disposed = false;
  private opened = false;

  constructor(context: NativeShellBootstrapContext, libraryId: string) {
    this.context = context; this.libraryId = libraryId; this.authority = context.authority();
    const selected = context.session();
    this.captured = selected && Object.freeze({sessionFamilyId: selected.sessionFamilyId, viewer: Object.freeze({...selected.viewer})});
  }
  private current = (): boolean => {
    const captured = this.captured, selected = this.context.session();
    return !this.disposed && !this.life.signal.aborted && this.context.current() && !!captured && !!selected
      && selected.sessionFamilyId === captured.sessionFamilyId && sameAuthority(this.authority, this.context.authority());
  };
  private reading(): ReadingClient {
    if (!this.current()) throw refused();
    return this.client ??= this.context.api.reading({viewer: this.captured!.viewer, isCurrent: this.current});
  }
  private publish(next: NativeReadingLibraryState): void {
    if (this.disposed || !this.current()) return;
    this.state = Object.freeze(next);
    for (const listener of [...this.listeners]) listener();
  }
  getSnapshot = () => this.state;
  subscribe = (listener: () => void): (() => void) => {
    if (this.disposed) return () => {};
    this.listeners.add(listener); return () => {this.listeners.delete(listener);};
  };
  load = async (): Promise<void> => {
    if (!this.current() || this.opened) return;
    this.origin ??= nativeReadOrigin(this.context, this.context.navigation.getSnapshot().route);
    this.opened = true;
    this.publish({phase: 'loading', items: []});
    try {
      const page = await this.reading().list({libraryId: this.libraryId, limit: 50}, this.life.signal);
      this.publish({phase: 'ready', items: page.items, ...(page.nextCursor ? {nextCursor: page.nextCursor} : {})});
    } catch (error) {
      if (this.current() && !(error instanceof ReadingError && error.code === 'cancelled')) this.publish({phase: 'error', items: [], error});
    }
  };
  more = async (): Promise<void> => {
    const cursor = this.state.nextCursor;
    if (!this.current() || !cursor || this.state.phase === 'loading') return;
    this.publish({...this.state, phase: 'loading'});
    try {
      const page = await this.reading().list({libraryId: this.libraryId, limit: 50, cursor}, this.life.signal);
      this.publish({phase: 'ready', items: [...this.state.items, ...page.items], ...(page.nextCursor ? {nextCursor: page.nextCursor} : {})});
    } catch (error) {
      if (this.current()) this.publish({...this.state, phase: 'error', error});
    }
  };
  retry = async (): Promise<void> => {
    if (!this.current()) return;
    this.opened = false;
    this.state = Object.freeze({phase: 'idle', items: []});
    await this.load();
  };
  /** Only a resident, inventory-available item opens its own book page. */
  open = async (item: ReadingItem): Promise<boolean> => {
    const origin = this.origin;
    if (!origin || !this.current() || item.inventoryAvailable !== true || !this.state.items.some(candidate => candidate.id === item.id && candidate.sourceId === item.sourceId)) return false;
    try {return await openNativeReadBook(this.context, {libraryId: this.libraryId, item}, origin, this.life.signal);}
    catch (error) {
      const cancelled = error instanceof ReadingError && error.code === 'cancelled' && error.status === undefined;
      if (this.life.signal.aborted || !this.current() || cancelled) return false;
      throw error;
    }
  };
  dispose = (): void => {
    if (this.disposed) return;
    this.disposed = true; this.life.abort(); this.client?.dispose(); this.client = undefined;
  };
}
