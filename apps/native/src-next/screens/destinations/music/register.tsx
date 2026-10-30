import {Platform} from 'react-native';
import {Artwork} from '../../../kit/artwork';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import type {NativeShellBootstrapContext} from '../../../shell/bootstrap';
import {nativeShellRoutes, type NativeRouteRenderer} from '../../../shell/route-registry';
import {nativeDestinationLifetime} from '../lifetime';
import {createNativeEntryActions} from '../entry-actions';
import {createNativeMusicSelection} from './selection';
import {NativeTVMusicController} from './model';
import {TVAlbumScreen, TVArtistScreen} from './TVMusicScreens';
import {PhoneAlbumScreen, PhoneArtistScreen} from './PhoneMusicScreens';
import {createMusicPlaylistPicker, type MusicPlaylistPickerHandle} from './MusicPlaylistPicker';
import {nativeDownloadRequest} from '../../watch/download-request';
import {askNativeDownload} from '../../downloads/request';
import {nativeScreenServices} from '../../native-screen-services';
import {catalogueSelectionSource, createNativeSelection, type NativeCatalogueSelectionSnapshot, type NativeSelection} from '../../selection/native-selection';

type Page = Readonly<{current:()=>boolean;key: string; route: {kind: 'music'; entry: ContentEntry}; entry: ContentEntry; controller: NativeTVMusicController; actions: ReturnType<typeof createNativeEntryActions>; picker: MusicPlaylistPickerHandle; tvSelection?: NativeSelection; touchSelection?: NativeSelection; abort: AbortController}>;

/** tv-more6–9: TV's album and artist pages. One route-owned controller per entry;
 * the shared queue player and the borrowed Watch menu do the work. */
export function createNativeTVMusicRenderer(context: NativeShellBootstrapContext): NativeRouteRenderer {
  const lifetime = nativeDestinationLifetime(context),viewer=context.session()?.viewer;
  if(!viewer||viewer.authority!=='local'&&viewer.authority!=='hosted')throw new Error('native_destination_authority_required');
  // Canonical V1 read-scope labels, as in the native runtime; never a playback fence.
  const similarScope=Object.freeze({serverId:viewer.serverId,authority:viewer.authority as 'local'|'hosted',accountId:viewer.accountId,profileId:viewer.profileId,
    controllerId:'v1',controllerEpoch:'v1',commandLaneId:'v1'});
  let pages = new Map<string, Page>(), disposed = false;
  const close = (page: Page) => {page.abort.abort(); page.picker.dispose(); page.tvSelection?.dispose(); page.touchSelection?.dispose(); page.actions.dispose(); page.controller.dispose();};
  const current = () => !disposed && lifetime.current();
  const pageFor = (route: {kind: 'music'; entry: ContentEntry}): Page | undefined => {
    if (!current() || route.entry.kind !== 'album' && route.entry.kind !== 'artist') return;
    const navigation=context.navigation.getSnapshot();
    if(navigation.route!==route)return;
    const revision=navigation.revision;
    const pageCurrent=()=>current()&&context.navigation.getSnapshot().route===route&&context.navigation.getSnapshot().revision===revision;
    const key=JSON.stringify([route.entry.kind,route.entry.id,route.entry.libraryId,revision]);
    let page = pages.get(key);
    // A page belongs to the exact route object it was opened for: a cached page
    // whose original object was replaced is retired, never drawn dead.
    if(page&&page.route!==route){close(page);pages.delete(key);page=undefined;}
    if (!page) {
      const controller = new NativeTVMusicController({api: context.api, entry:route.entry,similarScope,lifetime:{scope:lifetime.scope,current:pageCurrent},playback: context.playback, requestId: nativeScreenServices(context).requestId});
      const actions = createNativeEntryActions(context, {current:pageCurrent,
        contains: entry => controller.contains(entry), subscribe: controller.subscribe});
      const tvSelection = Platform.isTV ? createNativeMusicSelection(context, controller, key, pageCurrent) : undefined;
      let activationSnapshot: Readonly<{visible: boolean; intent: 'enter'; revision: typeof revision}> | undefined;
      const touchSelection = Platform.isTV ? undefined : createNativeSelection(context, catalogueSelectionSource({subscribe: controller.subscribe, getSnapshot: (): NativeCatalogueSelectionSnapshot => {
        const snapshot = controller.getSnapshot(), projection = controller.selectionProjection(), viewer = context.session()?.viewer, authority = viewer?.authority;
        const admitted = pageCurrent() && snapshot.phase === 'ready' && projection && viewer && (authority === 'local' || authority === 'hosted');
        return {key, witness: snapshot, context: admitted ? {
          scope: {serverId: viewer.serverId, authority, accountId: viewer.accountId, profileId: viewer.profileId,
            viewId: key, privacyFence: projection.scope.viewerFence},
          revision: {catalogRevision: String(projection.revision.catalog), viewerRevision: String(projection.revision.viewer)},
        } : null, sections: [
          {id: 'songs', start: snapshot.songPage.offset, entries: snapshot.songs.filter(controller.contains)},
          {id: 'releases', start: snapshot.releases.offset, entries: snapshot.albums.flatMap(album => {const entry = controller.releaseEntry(album); return entry ? [entry] : [];})},
          {id: 'all-songs', start: snapshot.allSongs.offset, entries: snapshot.allSongs.songs.filter(controller.contains)},
          {id: 'similar', entries: snapshot.similar.filter(entry => controller.similarCurrent(snapshot, entry))},
        ]};
      }}, {subscribe: context.navigation.subscribe, getSnapshot: () => {
        // One snapshot object until it changes, as useSyncExternalStore requires (see saved/model.ts).
        const visible = pageCurrent();
        if (!activationSnapshot || activationSnapshot.visible !== visible || activationSnapshot.revision !== revision) activationSnapshot = Object.freeze({visible, intent: 'enter' as const, revision});
        return activationSnapshot;
      }}), {area: 'saved', aggregates: true});
      page = Object.freeze({current:pageCurrent,key,route,entry: route.entry, controller, actions, tvSelection, touchSelection, picker: createMusicPlaylistPicker(context), abort: new AbortController()});
      pages.set(key, page);
      while (pages.size > 3) {
        const oldest = pages.entries().next().value as [string, Page] | undefined;
        if (!oldest) break;
        pages.delete(oldest[0]); close(oldest[1]);
      }
      void controller.load();
    }
    return page;
  };
  const renderer: NativeRouteRenderer = {
    render: (route, runtime) => {
      if (route.kind !== 'music') return null;
      const page = pageFor(route);
      if (!page) return null;
      const openEntry = (entry: ContentEntry) => {if(page.current()&&page.controller.contains(entry)) context.navigation.open({kind: 'music', entry}, context.authority());};
      const back = () => {if(page.current()) context.navigation.back(context.authority());};
      const download = (entry: ContentEntry) => {if(!page.current()||!page.controller.contains(entry))return;const request = nativeDownloadRequest(entry); if (request) askNativeDownload(request);};
      const pageCurrent=page.current;
      const onPlaylist = page.entry.kind === 'album' ? () => page.picker.openAlbum(
        {albumId: page.entry.id, load: () => page.controller.albumMembership()},
        {signal: page.abort.signal, isCurrent: () => current() && pageCurrent()},
      ) : undefined;
      if (!Platform.isTV) return <>
        {page.entry.kind === 'album'
          ? <PhoneAlbumScreen controller={page.controller} entry={page.entry} runtime={runtime} actions={page.actions} selection={page.touchSelection} openEntry={openEntry} onBack={back} download={download} onPlaylist={onPlaylist}/>
          : <PhoneArtistScreen controller={page.controller} entry={page.entry} runtime={runtime} actions={page.actions} selection={page.touchSelection} openEntry={openEntry} onBack={back}/>}
        {page.picker.render(runtime)}
        {page.touchSelection?.render(path => {const source = runtime.artwork(path); return source ? <Artwork source={source} width={48} height={48}/> : undefined;})}
      </>;
      return <>
        {page.entry.kind === 'album'
          ? <TVAlbumScreen key={page.entry.id} selection={page.tvSelection} controller={page.controller} entry={page.entry} runtime={runtime} actions={page.actions} openEntry={openEntry} onPlaylist={onPlaylist}/>
          : <TVArtistScreen key={page.entry.id} selection={page.tvSelection} controller={page.controller} entry={page.entry} runtime={runtime} actions={page.actions} openEntry={openEntry}/>}
        {page.picker.render(runtime)}
        {page.tvSelection?.render(path => <Artwork source={runtime.artwork(path)} width={40} height={40}/>)}
      </>;
    },
    dispose: () => {
      if (disposed) return;
      disposed = true;
      for (const page of pages.values()) close(page);
      pages.clear();
    },
  };
  return Object.freeze(renderer);
}

nativeShellRoutes.registerRoute('music', createNativeTVMusicRenderer);
