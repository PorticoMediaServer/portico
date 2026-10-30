import type {ContentScope, LibraryContentApi} from '@core/library-content.ts';
import {readLibraryInventoryConfig, type InventoryConfig, type SourceSettings} from '@core/library-inventory.ts';
import {removeLibraryFolder, saveLibraryFolder} from '@core/server-admin/libraries.ts';

/** The canonical folder fields, with the inventory-config revision captured by this source. */
export type SettingsLibraryFolderEdit = Omit<SourceSettings, 'expectedRevision'>;
export type SettingsLibraryFolderMutation = 'idle' | 'pending' | 'rejected' | 'reconciliation' | 'ambiguous';
export type SettingsLibraryFoldersSnapshot = Readonly<{libraryId?: string; config?: InventoryConfig; loading: boolean; error?: unknown; mutation: SettingsLibraryFolderMutation}>;
const empty: SettingsLibraryFoldersSnapshot = Object.freeze({loading: false, mutation: 'idle'});
/** Refusals that definitely did not apply; anything else may have been accepted. */
const definite = ['invalid_request', 'source_busy', 'source_changed', 'source_already_configured', 'library_configuration_conflict', 'stale_continuation', 'not_found', 'forbidden', 'unauthorized'];

/** One explicitly selected library's read-only configuration and its canonical folder commands. It is the producer's inventory revision, never a Settings revision. */
export class SettingsLibraryFoldersSource {
  private readonly bound: ContentScope;
  private state = empty;
  private request?: AbortController;
  private commandRequest?: AbortController;
  private pending?: Promise<void>;
  private commanding?: Promise<boolean>;
  private closed = false;
  constructor(private readonly api: LibraryContentApi, scope: ContentScope, private readonly isCurrent: () => boolean, private readonly notify: () => void, private readonly reportError: (error: unknown) => void) {
    // The caller's server/viewer scope is the fence every answer is checked against.
    this.bound = Object.freeze({serverId: scope.serverId, viewerId: scope.viewerId});
  }
  private current = () => !this.closed && this.isCurrent();
  getSnapshot = (): SettingsLibraryFoldersSnapshot => this.current() ? this.state : empty;
  /** Folders for exactly this selection; another library never borrows its answer. */
  config = (libraryId?: string): InventoryConfig | undefined => libraryId && this.current() && this.state.libraryId === libraryId ? this.state.config : undefined;
  private publish(state: SettingsLibraryFoldersSnapshot) {if (this.current()) {this.state = Object.freeze(state);this.notify();}}
  private owns(request: AbortController, libraryId: string) {return this.current() && !request.signal.aborted && this.request === request && this.state.libraryId === libraryId;}
  private ownsCommand(request: AbortController, libraryId: string) {return this.current() && !request.signal.aborted && this.commandRequest === request && this.state.libraryId === libraryId;}
  /** A command may start only from a genuine captured configuration; an uncertain outcome must be reconciled by a reread first. */
  private captured(libraryId: string): InventoryConfig | undefined {
    if (!this.current() || this.state.libraryId !== libraryId || !this.state.config || this.state.loading) return undefined;
    return this.state.mutation === 'idle' || this.state.mutation === 'rejected' ? this.state.config : undefined;
  }
  select = (libraryId?: string): Promise<void> => {
    if (!this.current()) return Promise.resolve();
    if (!libraryId) {this.clear();return Promise.resolve();}
    if (this.state.libraryId === libraryId) {
      if (this.pending) return this.pending;
      if (this.commanding) return this.commanding.then(() => undefined);
      if (this.state.config) return Promise.resolve();
    }
    // A replaced selection retires its command for good: returning later must never reconcile an old write.
    this.retireCommand();
    return this.read(libraryId);
  };
  /** Explicit recovery: rereads the selected library, keeping its genuine folders visible while the read runs. */
  refresh = (): Promise<void> => {
    const libraryId = this.state.libraryId;
    if (!this.current() || !libraryId) return Promise.resolve();
    if (this.pending) return this.pending;
    if (this.commanding) return this.commanding.then(() => undefined);
    return this.read(libraryId);
  };
  /** Add (`sourceId` omitted) or update exactly the captured folder membership, at the captured inventory revision. */
  save = (libraryId: string, sourceId: string | undefined, edit: SettingsLibraryFolderEdit): Promise<boolean> => {
    const captured = this.captured(libraryId);
    if (!captured || (sourceId !== undefined && !captured.sources.some(item => item.id === sourceId))) return Promise.resolve(false);
    return this.command(libraryId, captured, signal => saveLibraryFolder(this.api, libraryId, sourceId, {...edit, expectedRevision: captured.revision}, signal));
  };
  /** Remove exactly a captured folder at the captured inventory revision. */
  remove = (libraryId: string, sourceId: string): Promise<boolean> => {
    const captured = this.captured(libraryId);
    if (!captured || !captured.sources.some(item => item.id === sourceId)) return Promise.resolve(false);
    return this.command(libraryId, captured, signal => removeLibraryFolder(this.api, libraryId, sourceId, captured.revision, signal));
  };
  private read(libraryId: string): Promise<void> {
    this.request?.abort();
    const request = new AbortController();this.request = request;
    const retained = this.state.libraryId === libraryId ? this.state.config : undefined;
    const mutation = this.state.libraryId === libraryId ? this.state.mutation : 'idle';
    const pending = readLibraryInventoryConfig(this.api, this.bound, libraryId, request.signal).then(config => {
      if (this.owns(request, libraryId)) this.publish({libraryId, config, loading: false, mutation: 'idle'});
    }).catch(error => {
      // An error means this selection has no current configuration; a genuine reread is the only recovery.
      if (this.owns(request, libraryId)) {this.publish({libraryId, loading: false, mutation, error});this.reportError(error);}
    }).finally(() => {if (this.request === request) {this.request = undefined;this.pending = undefined;}});
    this.pending = pending;
    this.publish({libraryId, ...(retained ? {config: retained} : {}), loading: true, mutation});
    return pending;
  }
  /** One write at a time, followed by a genuine reread: the new membership is published only when the producer confirms it. */
  private command(libraryId: string, captured: InventoryConfig, write: (signal: AbortSignal) => Promise<unknown>): Promise<boolean> {
    const request = new AbortController();this.commandRequest = request;
    const running = (async (): Promise<boolean> => {
      try {
        await write(request.signal);
      } catch (error) {
        if (!this.ownsCommand(request, libraryId)) return false;
        const code = this.code(error);
        if (code === 'unauthorized' || code === 'forbidden') this.publish({libraryId, loading: false, mutation: 'rejected', error});
        else if (definite.includes(code ?? '')) this.publish({libraryId, config: captured, loading: false, mutation: 'rejected', error});
        else this.publish({libraryId, loading: false, mutation: 'ambiguous', error});
        return false;
      }
      if (!this.ownsCommand(request, libraryId)) return false;
      try {
        const config = await readLibraryInventoryConfig(this.api, this.bound, libraryId, request.signal);
        if (!this.ownsCommand(request, libraryId)) return false;
        this.publish({libraryId, config, loading: false, mutation: 'idle'});
        return true;
      } catch (error) {
        // The write was accepted; only the confirming reread is missing.
        if (this.ownsCommand(request, libraryId)) this.publish({libraryId, loading: false, mutation: 'reconciliation', error});
        return false;
      }
    })();
    const commanding = running.finally(() => {if (this.commandRequest === request) {this.commandRequest = undefined;this.commanding = undefined;}});
    this.commanding = commanding;
    this.publish({libraryId, config: captured, loading: false, mutation: 'pending'});
    return commanding;
  }
  private retireCommand() {
    this.commandRequest?.abort();this.commandRequest = undefined;this.commanding = undefined;
  }
  private code(error: unknown): string | undefined {
    const value = error as {code?: unknown} | null;
    return value && typeof value === 'object' && typeof value.code === 'string' ? value.code : undefined;
  }
  clear = () => {
    this.request?.abort();this.request = undefined;this.pending = undefined;
    this.commandRequest?.abort();this.commandRequest = undefined;this.commanding = undefined;
    if (this.state !== empty) {this.state = empty;if (this.current()) this.notify();}
  };
  dispose() {
    this.closed = true;this.request?.abort();this.request = undefined;this.pending = undefined;
    this.commandRequest?.abort();this.commandRequest = undefined;this.commanding = undefined;this.state = empty;
  }
}
