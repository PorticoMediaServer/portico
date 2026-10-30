import {PersonalSavedService, type SavedDefinition} from '../../../../../packages/client-core/src/personal-saved';
import type {SavedApi, SavedScope} from '../../../../../packages/client-core/src/saved';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';

export type NativeSaveViewSnapshot = Readonly<{open: boolean; name: string; busy: boolean; nameLocked: boolean; error?: unknown}>;
type Options = Readonly<{api: SavedApi; scope: SavedScope; definition: SavedDefinition; requestId: () => Promise<string>; current: () => boolean; saved: (id: string) => void}>;

/** One captured definition and one current mutation. An uncertain receipt is
 * retried through the same canonical service, with its original operation ID. */
export class NativeLibrarySaveView {
  private value: NativeSaveViewSnapshot = Object.freeze({open: true, name: '', busy: false, nameLocked: false});
  private readonly service: PersonalSavedService;
  private readonly options: Options;
  private readonly listeners = new Set<() => void>();
  private disposed = false;
  constructor(options: Options) {
    this.options = options;
    this.service = new PersonalSavedService({request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
      if (!this.current() || signal?.aborted) throw Object.assign(new Error('cancelled'), {name: 'AbortError'});
      const result = await options.api.request<T>(path, method, body, signal);
      if (!this.current() || signal?.aborted) throw Object.assign(new Error('cancelled'), {name: 'AbortError'});
      return result;
    }}, options.scope, options.requestId);
  }
  private current() {if (!this.disposed && !this.options.current()) this.dispose(); return !this.disposed;}
  getSnapshot = () => {this.current(); return this.value;};
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private patch(change: Partial<NativeSaveViewSnapshot>) {
    if (!this.current()) return;
    this.value = Object.freeze({...this.value, ...change}); for (const listener of [...this.listeners]) listener();
  }
  setName = (name: string) => {
    if (!this.current() || this.value.busy || this.value.nameLocked) return;
    if (this.service.getSnapshot().retryPending) this.service.dismiss();
    this.patch({name});
  };
  save = async (): Promise<void> => {
    const name = this.value.name.trim();
    if (!this.current() || this.value.busy || !name) return;
    this.patch({busy: true, error: undefined});
    try {
      if (!this.service.getSnapshot().route || this.service.getSnapshot().error) await this.service.select({view: 'views'});
      if (!this.current()) return;
      const before = this.service.getSnapshot();
      if (before.error) throw before.error;
      if (before.retryPending) await this.service.retry();
      else await this.service.mutate({action: 'create', kind: 'view', name, definition: this.options.definition});
      if (!this.current()) return;
      const after = this.service.getSnapshot();
      // The validated mutation receipt is authoritative even when refreshing
      // the directory after that receipt fails. Never create that view twice.
      if (!after.result?.resourceId) throw after.mutationError ?? after.error ?? Object.assign(new Error('invalid_response'), {code: 'invalid_response'});
      const id = after.result.resourceId;
      this.patch({open: false, name: '', busy: false});
      this.options.saved(id);
    } catch (error) {
      if (!this.current()) return;
      const state = this.service.getSnapshot();
      const presented = presentError(state.mutationError ?? error, 'library', {operation: 'save'});
      this.patch({busy: false, error, nameLocked: state.retryPending && (presented.action === 'try-again' || state.mutationError?.conflict === true)});
    } finally {if (this.current() && this.value.busy) this.patch({busy: false});}
  };
  close = () => {if (!this.value.busy) this.dispose();};
  dispose = () => {
    if (this.disposed) return;
    this.disposed = true; this.service.dispose();
    this.value = Object.freeze({open: false, name: '', busy: false, nameLocked: false});
    for (const listener of [...this.listeners]) listener(); this.listeners.clear();
  };
}
