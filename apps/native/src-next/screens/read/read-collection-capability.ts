import {DetailService} from '../../../../../packages/client-core/src/detail';
import type {HttpLocalApi} from '../../../../../packages/client-core/src';
import {linkNativeAbortSignals} from '../../platform/abort-signals';

const cancelled = () => Object.assign(new Error('cancelled'), {name: 'AbortError', code: 'cancelled'});
/** The exact Reading content kinds the producer admits: an ebook item or a
 * comic item. Never the `book` container, a `books` spelling, a format label or
 * an audiobook item. */
export type NativeReadingCollectionKind = 'ebook' | 'comic';

export type NativeReadingCollectionCapability = Readonly<{api: HttpLocalApi; scope: Readonly<{serverId: string; viewerId: string}>;
  requestId: () => Promise<string>; signal: AbortSignal; isCurrent: () => boolean; itemId: string; libraryId: string}>;

/** The item's own bounded Detail offer. It returns the producer's actual admitted
 * `data.item.kind` only when that kind is exactly ebook or comic and the target,
 * library, server and enabled add_to_collection action all match this item.
 * Format and library spelling are never used to infer an item kind. */
export async function nativeReadingCollectionCapability(options: NativeReadingCollectionCapability): Promise<NativeReadingCollectionKind | undefined> {
  const current = () => !options.signal.aborted && options.isCurrent();
  if (!current()) throw cancelled();
  const api = {request: async <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
    const scoped = linkNativeAbortSignals([options.signal, signal]);
    try {
      if (!current()) throw cancelled();
      const result = await options.api.request<T>(path, method, body, scoped.signal);
      if (!current()) throw cancelled();
      return result;
    } finally {scoped.dispose();}
  }};
  const requestId = async () => {if (!current()) throw cancelled(); const id = await options.requestId(); if (!current()) throw cancelled(); return id;};
  const detail = new DetailService({api, scope: options.scope, requestId});
  try {
    await detail.select({libraryId: options.libraryId, itemId: options.itemId});
    if (!current()) return undefined;
    const state = detail.getSnapshot(), data = state.data;
    if (state.phase !== 'ready' || !data || data.item.id !== options.itemId || data.item.libraryId !== options.libraryId
      || data.scope.itemId !== options.itemId || data.scope.libraryId !== options.libraryId
      || data.scope.serverId !== options.scope.serverId
      || !data.actions.some(action => action.id === 'add_to_collection' && action.enabled)) return undefined;
    return data.item.kind === 'ebook' || data.item.kind === 'comic' ? data.item.kind : undefined;
  } finally {detail.dispose();}
}
