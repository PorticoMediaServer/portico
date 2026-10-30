import type {RefObject} from 'react';
import type {PlaybackOptions} from '@core/playback-v1/types.ts';
import type {ShellAssemblyContext} from '../../shell/bootstrap';
import type {WatchVersionSelection} from './versions';

/** The title owns the original options read and real guarded command. No playing fence is
 * manufactured for a title menu, and no version preference survives this presentation. */
export type WatchVersionRequest = Readonly<{
  subject: Readonly<{libraryId: string; itemId: string; title: string}>;
  options: PlaybackOptions;
  signal: AbortSignal;
  isCurrent(): boolean;
  playVersion(versionId: string, signal: AbortSignal): Promise<void>;
  returnFocusRef?: RefObject<HTMLElement | null>;
}>;
export type WatchVersionPresentation = Readonly<{
  request: WatchVersionRequest;
  choices: WatchVersionSelection;
  playVersion(versionId: string, signal: AbortSignal): Promise<void>;
}>;

const owners = new WeakMap<ShellAssemblyContext, ReturnType<typeof createWatchVersionPresentation>>();

/** Inert rendezvous with the single persistent Watch host for this exact captured context. */
export function openWatchVersionChooser(context: ShellAssemblyContext, request: WatchVersionRequest): Promise<void> {
  const owner = owners.get(context);
  if (!context.isCurrent() || !owner) return Promise.reject(new DOMException('Watch authority retired', 'AbortError'));
  return owner.open(request);
}

/** No I/O, player, queue or formatter is created by this presentation controller. */
export function createWatchVersionPresentation(isCurrent: () => boolean) {
  const listeners = new Set<() => void>();
  let current: WatchVersionPresentation | undefined, life: AbortController | undefined, finish: (() => void) | undefined;
  const publish = () => {for (const listener of [...listeners]) listener();};
  const close = () => {
    const release = finish, retiredLife = life;finish = undefined;life = undefined;current = undefined;
    retiredLife?.abort();publish();release?.();
  };
  return {
    getSnapshot: () => current,
    subscribe: (listener: () => void) => {listeners.add(listener);return () => {listeners.delete(listener);};},
    close,
    open(request: WatchVersionRequest): Promise<void> {
      if (!isCurrent() || request.signal.aborted || !request.isCurrent()) return Promise.reject(new DOMException('Title authority retired', 'AbortError'));
      if (!request.subject.libraryId || !request.subject.itemId || !request.subject.title || request.options.itemId !== request.subject.itemId
        || request.options.versions.length < 2 || new Set(request.options.versions.map(version => version.id)).size !== request.options.versions.length) return Promise.reject(new Error('playback_version_unavailable'));
      close();
      const active = new AbortController();life = active;
      const valid = () => !active.signal.aborted && !request.signal.aborted && isCurrent() && request.isCurrent();
      const choices: WatchVersionSelection = Object.freeze({options: request.options, versions: request.options.versions, isCurrent: valid});
      const done = new Promise<void>(resolve => {finish = () => {request.signal.removeEventListener('abort', retired);resolve();};});
      const retired = () => {if (life === active) close();};
      request.signal.addEventListener('abort', retired, {once: true});
      current = Object.freeze({request, choices, async playVersion(versionId: string, signal: AbortSignal) {
        if (!valid() || signal.aborted || !choices.versions.some(version => version.id === versionId)) throw new DOMException('Title authority retired', 'AbortError');
        // The title adapter re-reads permission, membership and current resume at click. Its
        // original read lifetime and this modal's lifetime remain live until that start settles.
        await request.playVersion(versionId, AbortSignal.any([signal, request.signal, active.signal]));
      }});
      if (!valid()) retired();else publish();return done;
    },
    dispose() {close();listeners.clear();},
  };
}

export function registerWatchVersionPresentation(context: ShellAssemblyContext, owner: ReturnType<typeof createWatchVersionPresentation>): () => void {
  if (owners.has(context)) throw new Error('watch_version_owner_already_registered');
  owners.set(context, owner);
  return () => {if (owners.get(context) === owner) owners.delete(context);owner.dispose();};
}
