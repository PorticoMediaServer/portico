import React, {useSyncExternalStore} from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {PlaylistCard} from '../../../../../../packages/client-core/src/saved';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {Menu, type MenuEntry} from '../../../kit/overlays';
import {nativeDestinationLifetime} from '../lifetime';
import {nativeScreenServices} from '../../native-screen-services';
import {MusicPlaylistPort, type MusicAlbumResult, type MusicPlaylistSnapshot} from './playlist-port';

const t = defaultI18n.t;
const emptyPlaylists: MusicPlaylistSnapshot = Object.freeze({phase: 'idle', playlists: [], busy: false});
type Lifetime = Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;
type Added = (playlist: Readonly<{id: string; name: string}>) => void;
export type AlbumSource = Readonly<{albumId: string; load: () => Promise<readonly string[]>}>;

type OpenState = Readonly<{open: boolean; itemId?: string; album?: AlbumSource; port?: MusicPlaylistPort; lifetime?: Lifetime; onAdded?: Added}>;

/** The one playlist picker. A screen (or the shared TV entry sheet) opens it with
 * the item and the page lifetime; the host renders the returned surface once.
 * A whole album is opened with its complete-membership producer, never its id. */
export class MusicPlaylistPickerController {
  private context: NativeShellBootstrapContext;
  private scope: ReturnType<typeof nativeDestinationLifetime>['scope'];
  private requestId: () => Promise<string>;
  private state: OpenState = Object.freeze({open: false});
  private listeners = new Set<() => void>();
  private abortCleanup?: () => void;
  private disposed = false;
  constructor(context: NativeShellBootstrapContext) {
    this.context = context;
    this.scope = nativeDestinationLifetime(context).scope;
    this.requestId = nativeScreenServices(context).requestId;
  }
  getSnapshot = (): OpenState => this.state;
  subscribe = (listener: () => void): (() => void) => {if (this.disposed) return () => {}; this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private emit(): void {for (const listener of [...this.listeners]) listener();}
  private current(lifetime: Lifetime): boolean {return !this.disposed && this.context.current() && !lifetime.signal.aborted && lifetime.isCurrent();}
  open = (itemId: string, lifetime: Lifetime, onAdded?: Added): void => {
    if (this.disposed || !this.context.current() || !itemId || !this.current(lifetime)) return;
    this.begin({itemId}, lifetime, onAdded);
  };
  openAlbum = (album: AlbumSource, lifetime: Lifetime, onAdded?: Added): void => {
    if (this.disposed || !this.context.current() || !album.albumId || !this.current(lifetime)) return;
    this.begin({album}, lifetime, onAdded);
  };
  private begin(content: Readonly<{itemId?: string; album?: AlbumSource}>, lifetime: Lifetime, onAdded?: Added): void {
    this.close();
    const port = new MusicPlaylistPort({api: this.context.api, scope: this.scope, current: () => this.current(lifetime), requestId: this.requestId,
      ...(content.album ? {album: content.album.load} : {})});
    this.state = Object.freeze({open: true, ...content, port, lifetime, onAdded});
    this.emit();
    void port.load();
    const abort = () => {if (this.state.port === port) this.close();};
    lifetime.signal.addEventListener('abort', abort, {once: true});
    this.abortCleanup = () => lifetime.signal.removeEventListener('abort', abort);
  }
  add = async (playlist: PlaylistCard): Promise<void> => {
    const state = this.state, port = state.port;
    if (!state.open || !port || !state.lifetime || !this.current(state.lifetime)) return;
    if (state.album) {
      const result = await port.addAlbum({id: playlist.id, name: playlist.title});
      this.settle(result, playlist);
      return;
    }
    if (!state.itemId) return;
    try {
      await port.add(playlist.id, state.itemId, playlist.title);
      if (!this.disposed && state.lifetime && this.current(state.lifetime)) {
        state.onAdded?.({id: playlist.id, name: playlist.title});
        this.close();
      }
    } catch (error) {
      if (!this.disposed && state.lifetime && this.current(state.lifetime)) throw error;
    }
  };
  retryLeaf = async (): Promise<void> => {
    const state = this.state, port = state.port, leaf = port?.getSnapshot().leaf;
    if (!state.open || !port || !state.lifetime || !leaf || !this.current(state.lifetime)) return;
    const settled = await port.retryLeaf();
    if (settled && !this.disposed && state.lifetime && this.current(state.lifetime)) {
      state.onAdded?.({id: leaf.playlistId, name: leaf.playlistName ?? ''});
      this.close();
    }
  };
  retryAlbum = async (): Promise<void> => {
    const state = this.state, port = state.port, playlist = port?.getSnapshot().album;
    if (!state.open || !port || !state.lifetime || !playlist || !this.current(state.lifetime)) return;
    const result = await port.resumeAlbum();
    this.settle(result, {id: playlist.playlistId, title: playlist.playlistName ?? ''});
  };
  private settle(result: MusicAlbumResult, playlist: Readonly<{id: string; title: string}>): void {
    const state = this.state;
    if (!state.lifetime || !this.current(state.lifetime)) return;
    if (result.phase === 'done') {
      state.onAdded?.({id: playlist.id, name: playlist.title});
      this.close();
      return;
    }
    // A partial result stays in the picker's own error area; zero confirmed
    // additions use the existing failure state (the caller reports the error).
    if (result.phase === 'failed' && result.error) throw result.error;
  }
  retry = (): Promise<void> => {
    const port = this.state.port;
    if (!this.state.open || !port) return Promise.resolve();
    const snapshot = port.getSnapshot();
    return snapshot.album ? this.retryAlbum() : snapshot.leaf ? this.retryLeaf() : port.load();
  };
  close = (): void => {
    if (!this.state.open) return;
    this.abortCleanup?.(); this.abortCleanup = undefined;
    this.state.port?.dispose();
    this.state = Object.freeze({open: false});
    this.emit();
  };
  dispose = (): void => {
    if (this.disposed) return;
    this.disposed = true; this.close(); this.listeners.clear();
  };
}

function PickerSurface({controller, runtime}: {controller: MusicPlaylistPickerController; runtime: NativeShellRuntime}) {
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const port = state.port;
  const snapshot: MusicPlaylistSnapshot = useSyncExternalStore(port?.subscribe ?? (() => () => {}), port?.getSnapshot ?? (() => emptyPlaylists));
  if (!state.open || !port) return null;
  const album = snapshot.album, leaf = snapshot.leaf, adding = album?.phase === 'loading' || album?.phase === 'adding';
  const partial = album?.phase === 'partial' && album.added > 0;
  const failed = album?.phase === 'failed';
  const busy = snapshot.busy || snapshot.phase === 'loading';
  const report = (work: Promise<void>) => {void work.catch(error => runtime.reportError(error));};
  const items: MenuEntry[] = partial || failed
    ? [{id: 'retry-album', label: t('saved.retry'), disabled: busy, closeOnSelect: false, onSelect: () => report(controller.retryAlbum())}]
    : leaf
      ? [{id: 'leaf-playlist', label: leaf.playlistName || t('entry.addToPlaylist'), disabled: true, onSelect: () => {}},
        {id: 'retry-leaf', label: t('saved.retry'), disabled: busy, closeOnSelect: false, onSelect: () => report(controller.retryLeaf())}]
    : snapshot.phase === 'loading' || !snapshot.playlists.length && snapshot.phase !== 'error'
      ? snapshot.phase === 'loading' ? [{id: 'loading', label: t('saved.loading'), disabled: true, onSelect: () => {}}] : [{id: 'empty', label: t('web.saved.empty.playlists'), disabled: true, onSelect: () => {}}]
      : snapshot.phase === 'error' ? [{id: 'retry', label: t('saved.retry'), closeOnSelect: false, onSelect: () => report(controller.retry())}]
        : snapshot.playlists.map(playlist => ({id: playlist.id, label: playlist.title, trailing: playlist.count === undefined ? undefined : defaultI18n.number(playlist.count),
          disabled: busy, closeOnSelect: false, onSelect: () => report(controller.add(playlist))}));
  const description = partial ? t('next.selection.playlistAlbumPartial', {count: album?.added ?? 0})
    : adding && album?.playlistName ? t('next.selection.playlistAlbumProgress', {playlist: album.playlistName}) : undefined;
  return <Menu testID="music-playlist-picker" open onOpenChange={open => {if (!open) controller.close();}}
    title={t('entry.addToPlaylist')} description={description} items={items}/>;
}

export type MusicPlaylistPickerHandle = Readonly<{
  open: (itemId: string, lifetime: Lifetime, onAdded?: Added) => void;
  openAlbum: (album: AlbumSource, lifetime: Lifetime, onAdded?: Added) => void;
  render: (runtime: NativeShellRuntime) => React.ReactNode;
  dispose: () => void;
}>;

/** Create one picker per authority-local screen module and render its surface once. */
export function createMusicPlaylistPicker(context: NativeShellBootstrapContext): MusicPlaylistPickerHandle {
  const controller = new MusicPlaylistPickerController(context);
  return Object.freeze({open: controller.open, openAlbum: controller.openAlbum, render: runtime => <PickerSurface controller={controller} runtime={runtime}/>, dispose: controller.dispose});
}
