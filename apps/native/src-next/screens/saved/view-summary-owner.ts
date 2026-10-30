import type {PersonalResource} from '../../../../../packages/client-core/src/personal-saved';
import type {SavedViewSummaryText} from '../../../../../packages/client-core/src/presentation/saved-view-summary';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import {NativeHomeSavedViewSummaries} from '../home/saved-view-summaries';
import {createNativeSavedViewSummaryBinding, type NativeSavedViewSummarySource} from './view-summaries';

/** One Saved-owned reader and its binding for one real retained views-directory
 * source. The reader stays private: callers only start, read the existing
 * binding ports, or dispose the whole owner. */
export type NativeSavedViewSummaryOwner = Readonly<{
  start: () => Promise<void>;
  getSnapshot: () => number;
  subscribe: (listener: () => void) => () => void;
  line: (resource: PersonalResource, text: SavedViewSummaryText) => string | undefined;
  dispose: () => void;
}>;

/** Construction is inert: no request, library enumeration or subscription
 * happens until start(). An observed lost owner retires the binding before the
 * owned reader, aborting its work without notifying React; the borrowed Saved
 * source is never disposed here. */
export function createNativeSavedViewSummaryOwner(
  context: NativeShellBootstrapContext,
  source: NativeSavedViewSummarySource,
  ownerCurrent: () => boolean,
): NativeSavedViewSummaryOwner {
  const lifetime = nativeDestinationLifetime(context);
  let disposed = false, retired = false, startPromise: Promise<void> | undefined;
  let stopAuthority: (() => void) | undefined;
  const current = () => !disposed && !retired && lifetime.current() && ownerCurrent();
  const reader = new NativeHomeSavedViewSummaries({api: context.api, serverId: lifetime.scope.serverId, current,
    libraryIds: () => context.libraries().map(library => library.id)});
  const binding = createNativeSavedViewSummaryBinding(context, source, ownerCurrent, reader);
  const retire = () => {
    if (disposed || retired) return;
    retired = true;
    stopAuthority?.(); stopAuthority = undefined;
    binding.dispose();
    reader.dispose();
  };
  const check = () => {if (!current()) retire();};
  const start = (): Promise<void> => {
    if (startPromise) return startPromise;
    if (disposed || retired) {startPromise = Promise.resolve(); return startPromise;}
    stopAuthority = context.subscribeAuthority(check);
    startPromise = binding.start().then(() => {if (!current()) retire();});
    return startPromise;
  };
  const line = (resource: PersonalResource, text: SavedViewSummaryText): string | undefined => {
    if (!current()) {retire(); return undefined;}
    const value = binding.line(resource, text);
    if (!current()) retire();
    return value;
  };
  const dispose = (): void => {
    if (disposed) return;
    disposed = true;
    stopAuthority?.(); stopAuthority = undefined;
    binding.dispose();
    reader.dispose();
  };
  return Object.freeze({start, getSnapshot: binding.getSnapshot, subscribe: binding.subscribe, line, dispose});
}
