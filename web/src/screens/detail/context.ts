import {useCallback, useSyncExternalStore} from 'react';
import type {EntryMetadataEffect} from '../../shell/entry-metadata';
import type {BrowserDownloadPort} from '../../platform/browser-download';
import type {WebPlaybackScreenTarget} from '../../platform/playback/types';
import type {EntryActionTarget} from '../../shell/entry-actions';
import type {HttpLocalApi} from '@core/index.ts';
import type {ContentScope} from '@core/library-content.ts';
import type {SearchEntry} from '@core/search.ts';
import type {EventsClient} from '@core/playback-v1/events.ts';
import type {PreferenceSnapshot} from '@core/preferences.ts';
import type {ShellArtwork} from '../../shell/artwork';
import type {ShellIdentity} from '../../shell/Shell';
import type {QueueBrowseSelector} from '@core/playback-v1/types.ts';
import type {WatchVersionRequest} from '../watch/version-presentation';

/** Captured by the shell for one server/account/profile/role epoch. Not a wire DTO. */
export type ScreenContext = Readonly<{
  api: HttpLocalApi;
  events: EventsClient;
  scope: ContentScope;
  isCurrent: () => boolean;
  libraryRouteSignal?: AbortSignal;
  artwork: ShellArtwork;
  identity?: ShellIdentity;
  subscribeIdentity?: (listener: () => void) => () => void;
  preferences: Readonly<{getSnapshot: () => PreferenceSnapshot; subscribe: (listener: () => void) => () => void}>;
  effects: Readonly<{
    openEntry: (entry: SearchEntry) => void;
    openSettings: () => void;
    openHome?: () => void;
    back?: () => void;
    chooseProfile?: () => void;
    openBrowse?: (selector: QueueBrowseSelector) => void;
    play: (target: WebPlaybackScreenTarget, signal: AbortSignal, isCurrent?: () => boolean) => Promise<void>;
    playSeason?: (seasonId: string, signal: AbortSignal, isCurrent: () => boolean) => Promise<void>;
    playArtist?: (artistId: string, signal: AbortSignal, isCurrent: () => boolean, shuffle?: boolean) => Promise<void>;
    playAlbum?: (albumId: string, shuffle: boolean, signal: AbortSignal, isCurrent: () => boolean) => Promise<void>;
    playRadio?: (seedId: string, signal: AbortSignal, isCurrent: () => boolean) => Promise<void>;
    chooseVersion?: (request: WatchVersionRequest) => Promise<void>;
    download?: BrowserDownloadPort;
    editMetadata?: EntryMetadataEffect;
    addToPlaylist?: (target: EntryActionTarget, anchor: HTMLElement) => Promise<void>;
    addToCollection?: (target: EntryActionTarget, anchor: HTMLElement) => Promise<void>;
    deleteTitle?: (target: EntryActionTarget, anchor: HTMLElement) => Promise<void>;
    reportError: (error: unknown) => void;
  }>;
}>;

/** An obsolete DOM event is cancelled through the same sanitized error path as async work. */
export function runScreenCommand(context: ScreenContext, command: () => unknown): void {
  try { void Promise.resolve(command()).catch(context.effects.reportError); }
  catch (error) { context.effects.reportError(error); }
}

/** Every request, including reads started by a service retry, checks the captured authority. */
export function screenLifetime(context: ScreenContext) {
  const life = new AbortController();
  const current = () => !life.signal.aborted && !context.libraryRouteSignal?.aborted && context.isCurrent();
  const assert = () => { if (!current()) throw new DOMException('Screen authority changed', 'AbortError'); };
  const inherited = context.libraryRouteSignal;
  const signal = (input?: AbortSignal) => inherited || input ? AbortSignal.any([life.signal, ...(inherited ? [inherited] : []), ...(input ? [input] : [])]) : life.signal;
  const api = {
    async acknowledgeUserMutation(intentId: string): Promise<void> {
      assert();
      await context.api.acknowledgeUserMutation?.(intentId);
      assert();
    },
    async request<T>(path: string, method?: string, body?: unknown, input?: AbortSignal): Promise<T> {
      assert();
      const active = signal(input); active.throwIfAborted();
      const value = await context.api.request<T>(path, method, body, active);
      assert(); active.throwIfAborted(); return value;
    },
    async requestRaw(...args: Parameters<HttpLocalApi['requestRaw']>) {
      const [path, method, options] = args;
      const metadataPreview = path === '/v1/metadata/bulk/preview'
        || /^\/v1\/metadata\/bulk\/preview-queries\/[A-Za-z0-9._~-]{1,128}$/.test(path);
      // A File info window lease is released the same way when its panel or screen goes.
      const fileInfoRelease = method === 'DELETE' && options?.singleAttempt === true && /^\/v1\/file-info-queries\/[A-Za-z0-9._~%-]{1,1024}$/.test(path);
      if (fileInfoRelease) return context.api.requestRaw(path, method, options);
      // This private read handle is cleanup, not a new screen command. The
      // preparation owner must be able to release it after screen retirement.
      if (metadataPreview && method === 'DELETE' && options?.singleAttempt) return context.api.requestRaw(path, method, options);
      assert();
      const active = signal(options?.signal); active.throwIfAborted();
      const value = await context.api.requestRaw(path, method, {...options, signal: active});
      // Let the preparation owner validate/release a late pending envelope.
      if (!metadataPreview) {assert(); active.throwIfAborted();}
      return value;
    },
  };
  return {api, current, assert, signal, dispose: () => life.abort()};
}

/** Preserve the original command/currentness ports while forwarding display observations. */
export function forwardIdentity<T extends Pick<ScreenContext, 'identity' | 'subscribeIdentity'>, E extends object>(context: T, extra: E): T & E {
  return {...context, ...extra, get identity() {return context.identity;}, subscribeIdentity: context.subscribeIdentity} as T & E;
}
export function useScreenIdentity(context: Pick<ScreenContext, 'identity' | 'subscribeIdentity'> | undefined) {
  const subscribe = useCallback((listener: () => void) => context?.subscribeIdentity?.(listener) ?? (() => {}), [context]);
  const read = useCallback(() => context?.identity, [context]);
  return useSyncExternalStore(subscribe, read, read);
}
