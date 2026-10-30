import React from 'react';
import type {MessageId} from '../../../../../packages/i18n/src';
import type {NativeOfflineDownloadsDevice} from '../../platform/offline-downloads';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeRouteRenderer} from '../../shell/route-registry';
import {SearchScreen} from '../search/SearchScreen';
import {NativeSearchDestination} from '../search/model';
import type {NativeSearchActions, NativeSearchExplorePort, NativeSearchSelectionPort, NativeSearchTVCopy} from '../search/types';
import type {NativeSavedActions, NativeSavedSelectionPort} from '../saved/SavedScreen';
import {createNativeSavedRenderer} from '../saved/renderer';
import type {NativeSavedDownloadsCopy} from '../saved/SavedDownloadCard';
import {DownloadsScreen, type NativeDownloadsCopy} from '../downloads/DownloadsScreen';
import type {OfflineEntry} from '../../platform/offline-downloads';
import {nativeDestinationLifetime} from './lifetime';
import type {NativeDestinationActivationPort} from './focus';

export type NativeDestinationPorts = Readonly<{
  context: NativeShellBootstrapContext;
  requestId: () => Promise<string>;
  rememberHistory: () => boolean;
  reportError: (error: unknown) => void;
  activation: (kind: 'search' | 'saved') => NativeDestinationActivationPort;
  searchExplore: NativeSearchExplorePort;
  searchTVCopy: NativeSearchTVCopy;
  searchActions: NativeSearchActions;
  searchSelection?: NativeSearchSelectionPort;
  savedActions: NativeSavedActions;
  savedSelection?: NativeSavedSelectionPort;
  savedDownloadsCopy: NativeSavedDownloadsCopy;
  offline: NativeOfflineDownloadsDevice;
  downloads: Readonly<{copy: NativeDownloadsCopy; back: () => void; openTitle: (entry: OfflineEntry) => void}>;
  onThisPhone: MessageId;
}>;
export type NativeDestinationRenderers = Readonly<{
  search: NativeRouteRenderer;
  saved: NativeRouteRenderer;
  downloads: NativeRouteRenderer;
  dispose: () => void;
}>;

export type NativeDownloadsRendererPorts = Readonly<{
  offline: NativeOfflineDownloadsDevice;
  copy: NativeDownloadsCopy; back: () => void; openTitle: (entry: OfflineEntry) => void;
}>;

/** The STOP1 empty Downloads route has no dependency on Search discovery or selection.
 * Accounts can assemble this checked descriptor while the other producers are landing. */
export function createNativeDownloadsRenderer(context: NativeShellBootstrapContext, ports: NativeDownloadsRendererPorts): NativeRouteRenderer {
  const lifetime = nativeDestinationLifetime(context);
  return {render: (_route, runtime) => lifetime.current() ? <DownloadsScreen runtime={runtime} device={ports.offline} copy={ports.copy} back={ports.back} openTitle={ports.openTitle}/> : null};
}

/** Accounts assembles these descriptors into the sole shell dispatcher and the actual
 * downloads route. Importing this module never registers global routes or a mock host. */
export function createNativeDestinationRenderers(ports: NativeDestinationPorts): NativeDestinationRenderers {
  const lifetime = nativeDestinationLifetime(ports.context);
  let disposed = false;
  const current = () => !disposed && lifetime.current();
  const search = new NativeSearchDestination({api: ports.context.api, scope: lifetime.scope, current, rememberHistory: ports.rememberHistory, reportError: ports.reportError});
  const saved = createNativeSavedRenderer(ports.context, {...ports, activation: ports.activation('saved')});
  return Object.freeze({
    search: {render: (route, runtime) => current() && route.kind === 'shell' && route.destination.kind === 'search'
      ? <SearchScreen destination={search} runtime={runtime} explore={ports.searchExplore} actions={ports.searchActions} selection={ports.searchSelection} activation={ports.activation('search')} tvCopy={ports.searchTVCopy}/> : null},
    saved,
    downloads: {render: (_route, runtime) => current() ? <DownloadsScreen runtime={runtime} device={ports.offline} {...ports.downloads}/> : null},
    dispose: () => {if (disposed) return; disposed = true; search.dispose(); saved.dispose();},
  } satisfies NativeDestinationRenderers);
}

export type {NativeSearchActions, NativeSearchExplorePort, NativeSearchExploreSnapshot, NativeSearchGenre, NativeSearchSelectionPort, NativeSearchTVCopy} from '../search/types';
export {NativeSearchExplore} from '../search/explore';
export type {NativeSearchGenresSnapshot} from '../search/explore';
export {NativeGenreBrowseDestination} from '../search/genre-browse';
export type {NativeGenreBrowseSnapshot} from '../search/genre-browse';
export {nativeSearchRememberHistory, openNativeSearchGenre, openNativeSearchGenres, createNativeSearchGenreController} from '../search/navigation';
export {createNativeSearchController} from '../search/controller';
export type {NativeSavedActions, NativeSavedSelectionPort} from '../saved/SavedScreen';
export {openNativeSavedView, createNativeSavedHubController, createNativeSavedViewController} from '../saved/navigation';
export type {NativeSavedViewRoute} from '../saved/navigation';
export {createNativeSavedState} from '../saved/state';
export type {NativeSavedDownloadsCopy} from '../saved/SavedDownloadCard';
export {createNativeSavedRenderer};
export type {NativeSavedRendererPorts} from '../saved/renderer';
export type {NativeDownloadsCopy} from '../downloads/DownloadsScreen';
export {NativeTVKeyboard} from '../search/TVKeyboard';
export type {NativeTVKeyboardProps} from '../search/TVKeyboard';
export type {NativeDestinationActivation, NativeDestinationActivationPort} from './focus';
export {createNativeLibraryRenderer} from '../library';
export type {NativeLibraryRendererPorts, NativeLibraryActions, NativeLibrarySelectionPort} from '../library';
