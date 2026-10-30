import type {PersonalResource, PersonalSavedError, PersonalSavedSnapshot} from '../../../../../packages/client-core/src/personal-saved';

/** The narrow view of the owner's canonical per-resource Saved service. The owner
 * keeps the service, its one command and its receipt; this controller only reads
 * the published snapshot and asks for the offered update. */
export type NativeSavedViewRenamePort = Readonly<{
  getSnapshot: () => PersonalSavedSnapshot;
  subscribe: (listener: () => void) => () => void;
  mutate: (intent: Readonly<{action: 'update'; name: string}>) => Promise<boolean>;
  retry: () => Promise<boolean>;
}>;

export type NativeSavedViewRenameSnapshot = Readonly<{
  /** The field's text, exactly as typed or as the resource named it. */
  name: string;
  /** The canonical service holds a command this controller started. */
  busy: boolean;
  /** The canonical service's error, unchanged. */
  error: PersonalSavedError | null;
  /** The canonical service holds an unconfirmed command; retry is byte-identical. */
  retryPending: boolean;
  /** A confirmed receipt closed this rename. */
  closed: boolean;
  /** The owning view retired; a retirement never revives. */
  retired: boolean;
  /** A trimmed name can be submitted against the currently offered update. */
  canSave: boolean;
}>;

export type NativeSavedViewRenameController = Readonly<{
  getSnapshot: () => NativeSavedViewRenameSnapshot;
  subscribe: (listener: () => void) => () => void;
  setName: (name: string) => boolean;
  save: () => Promise<boolean>;
  retry: () => Promise<boolean>;
  cancel: () => boolean;
  dispose: () => void;
}>;

export type NativeSavedViewRenameOptions = Readonly<{
  service: NativeSavedViewRenamePort;
  resource: PersonalResource;
  current: () => boolean;
}>;

/** One rename of the actual loaded Saved view, over the service that view already
 * owns. It never creates, disposes or dismisses that service: an unconfirmed
 * command stays the owner's canonical command, and disposal removes only these
 * subscriptions and local state. */
export function createNativeSavedViewRenameController(options: NativeSavedViewRenameOptions): NativeSavedViewRenameController {
  const {service, resource, current} = options;
  const listeners = new Set<() => void>();
  let stop: () => void = () => {};
  let disposed = false;
  let retired = !current();
  let closed = false;
  let busy = false;
  let error: PersonalSavedError | null = null;
  let retryPending = false;
  let name = resource.name;

  /** The offered update is on the same loaded view, not a replacement or another
   * kind of resource that happens to offer update. */
  const offered = (): boolean => {
    if (resource.kind !== 'view') return false;
    const loaded = service.getSnapshot().resource;
    return loaded !== null && loaded.kind === 'view' && loaded.id === resource.id && loaded.serverId === resource.serverId && loaded.actions.includes('update');
  };
  const idle = (): boolean => {
    const state = service.getSnapshot();
    return !state.pending && !state.retryPending;
  };
  const build = (): NativeSavedViewRenameSnapshot => Object.freeze({
    name, busy, error, retryPending, closed, retired,
    canSave: !disposed && !retired && !closed && !busy && !retryPending && name.trim() !== '' && offered() && idle(),
  });
  let value = build();
  const publish = (): void => {
    value = build();
    for (const listener of [...listeners]) listener();
  };
  const markRetired = (): boolean => {
    if (disposed || retired) return false;
    retired = true; busy = false; stop();
    return true;
  };
  /** Retirement found by a snapshot read is latched without notifying that read's
   * caller; the cached snapshot stays stable and later reads cannot revive it. */
  const latch = (): void => { if (markRetired()) value = build(); };
  const retire = (): void => { if (markRetired()) publish(); };
  /** The owner's lifetime gates every command and every callback. */
  const live = (): boolean => {
    if (!disposed && !retired && !closed && !current()) retire();
    return !disposed && !retired;
  };
  const sync = (): void => {
    if (disposed || retired || closed) return;
    if (!current()) { retire(); return; }
    // A command the owner dismissed is no longer outstanding. Keeping the name
    // lets a later Save be a new canonical command instead of a dead Retry.
    const state = service.getSnapshot();
    if (!busy && !state.pending && !state.retryPending) { error = null; retryPending = false; }
    publish();
  };
  if (!retired) stop = service.subscribe(sync);

  const getSnapshot = (): NativeSavedViewRenameSnapshot => {
    if (!disposed && !retired && !closed && !current()) latch();
    return value;
  };
  const subscribe = (listener: () => void): (() => void) => {
    if (disposed) return () => {};
    listeners.add(listener);
    return () => { listeners.delete(listener); };
  };
  const setName = (input: string): boolean => {
    if (!live() || busy || retryPending || closed) return false;
    name = input;
    publish();
    return true;
  };
  /** One command, then the canonical service's answer decides the outcome. */
  const settle = async (command: () => Promise<boolean>): Promise<boolean> => {
    busy = true; error = null; publish();
    if (!live()) { busy = false; value = build(); return false; }
    let accepted = false;
    try { accepted = await command(); } catch { accepted = false; }
    if (disposed) return false;
    if (!live()) { value = build(); return false; }
    const state = service.getSnapshot();
    // A successful service call already matched this exact operation ID and
    // receipt. A result that names another resource is not this rename's answer.
    if (accepted && !state.pending && !state.retryPending && state.mutationError === null) {
      const result = state.result;
      if (result !== null && result.deleted === false && result.resourceId === resource.id) {
        busy = false; error = null; retryPending = false; closed = true; name = '';
        stop();
        publish();
        return true;
      }
    }
    busy = false;
    error = state.mutationError;
    retryPending = error !== null && state.retryPending;
    publish();
    return false;
  };
  const save = async (): Promise<boolean> => {
    if (!live() || busy || closed || retryPending) return false;
    const typed = name.trim();
    if (!typed || !offered() || !idle()) return false;
    return await settle(() => service.mutate({action: 'update', name: typed}));
  };
  /** The canonical service keeps the original command and operation ID. */
  const retry = async (): Promise<boolean> => {
    if (!live() || busy || closed || !retryPending) return false;
    const state = service.getSnapshot();
    if (!state.retryPending) { error = null; retryPending = false; publish(); return false; }
    if (state.pending) return false;
    return await settle(() => service.retry());
  };
  /** Closing the presentation never dismisses the owner's command or receipt. */
  const cancel = (): boolean => {
    if (!live() || busy || closed) return false;
    closed = true; busy = false; error = null; retryPending = false;
    stop();
    publish();
    return true;
  };
  const dispose = (): void => {
    if (disposed) return;
    disposed = true; busy = false;
    stop();
    value = build();
    listeners.clear();
  };

  return Object.freeze({getSnapshot, subscribe, setName, save, retry, cancel, dispose});
}
