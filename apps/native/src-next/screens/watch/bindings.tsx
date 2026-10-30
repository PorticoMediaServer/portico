import React from 'react';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {nativeScreenError, nativeScreenServices} from '../native-screen-services';
import {NativeWatchPresenters} from './presenters';
import {NativeWatchOverlays} from './WatchOverlays';
import type {NativeWatchModuleBindings} from './module';
import {NativeWatchExternalRequests} from './external-requests';
import {askNativeDownload} from '../downloads/request';
import {createMusicPlaylistPicker} from '../destinations/music/MusicPlaylistPicker';
import {nativeDownloadRequest} from './download-request';

/** One real authority-local composition, borrowed by handheld and TV entry.
 * It owns Title/File info overlays; shared feedback is mounted by assembly. */
export function createNativeWatchBindings(context: NativeShellBootstrapContext): NativeWatchModuleBindings {
  const common = nativeScreenServices(context), presenters = new NativeWatchPresenters(context, common), playlist = createMusicPlaylistPicker(context);
  const requestPictureInPicture = context.playback.requestPictureInPicture;
  const external = requestPictureInPicture ? new NativeWatchExternalRequests({current: context.current, subscribeCurrent: context.subscribeAuthority,
    navigation: context.navigation, playback: {getSnapshot: context.playback.getSnapshot, subscribe: context.playback.subscribe, requestPictureInPicture}}, error => common.reportError(error)) : undefined;
  return Object.freeze({
    requestId: common.requestId,
    title: {
      aboutLabel: defaultI18n.t('next.browse.native.aboutTitle'), navigate: common.navigate, owner: context.session()?.viewer.role === 'owner', serverName: context.serverName,
      person: personId => {const authority = context.authority(); if (context.current()) context.navigation.open({kind: 'person', personId}, authority);},
      search: query => {const authority = context.authority();
        if (context.current() && query.trim()) context.navigation.open({kind: 'shell', destination: {kind: 'search', query}}, authority);},
      download: entry => {const request = nativeDownloadRequest(entry); if (request) askNativeDownload(request);},
      openCollection: collection => {const authority = context.authority();
        if (context.current()) context.navigation.open({kind: 'collection', libraryId: collection.libraryId, collectionId: collection.id, title: collection.name}, authority);},
      renderError: nativeScreenError(common, 'detail'), reportError: (error: unknown) => common.reportError(error, 'detail'),
      menu: {subscribe: presenters.subscribe, getSnapshot: presenters.getMenuEntry},
    },
    playlistPicker: playlist,
    notice: common.notice, manageFile: presenters.manageFile, openFileInfo: presenters.openFileInfo, reopenFileInfo: presenters.reopenFileInfo, openMenu: presenters.openMenu, openDiscTitles: presenters.openDiscTitles,
    openVersions: presenters.openVersions, beginVersions: presenters.beginVersions, startVersion: context.playback.startVersion,
    renderOverlays: runtime => <><NativeWatchOverlays presenters={presenters} runtime={runtime} common={common}/>{playlist.render(runtime)}</>,
    ...(external ? {onPictureInPicture: (fence: Parameters<NativeWatchExternalRequests['request']>[0]) => {void external.request(fence);}} : {}),
    dispose: () => {external?.dispose(); presenters.dispose(); playlist.dispose();},
  });
}
