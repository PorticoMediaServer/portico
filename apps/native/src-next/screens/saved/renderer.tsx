import React from 'react';
import type {MessageId} from '../../../../../packages/i18n/src';
import type {NativeOfflineDownloadsDevice} from '../../platform/offline-downloads';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeRouteRenderer} from '../../shell/route-registry';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import type {NativeDestinationActivationPort} from '../destinations/focus';
import {SavedScreen, type NativeSavedActions, type NativeSavedSelectionPort} from './SavedScreen';
import type {NativeSavedDownloadsCopy} from './SavedDownloadCard';
import {NativeSavedDestination} from './model';

export type NativeSavedRendererPorts = Readonly<{
  requestId: () => Promise<string>;
  savedActions: NativeSavedActions;
  savedSelection?: NativeSavedSelectionPort;
  savedDownloadsCopy: NativeSavedDownloadsCopy;
  offline: NativeOfflineDownloadsDevice;
  onThisPhone: MessageId;
  activation: NativeDestinationActivationPort;
}>;

/** The Saved hub can be genuinely assembled while Search Explore is still a
 * separate producer. Accounts chooses this or the combined factory, never both. */
export function createNativeSavedRenderer(context: NativeShellBootstrapContext, ports: NativeSavedRendererPorts): NativeRouteRenderer & {dispose: () => void} {
  const lifetime = nativeDestinationLifetime(context);
  let disposed = false;
  const current = () => !disposed && lifetime.current();
  const saved = new NativeSavedDestination({api: context.api, scope: lifetime.scope, current, requestId: ports.requestId});
  return Object.freeze({
    render: (route, runtime) => current() && route.kind === 'shell' && route.destination.kind === 'saved'
      ? <SavedScreen destination={saved} runtime={runtime} device={ports.offline} actions={ports.savedActions} selection={ports.savedSelection}
        downloadsCopy={ports.savedDownloadsCopy} onThisPhone={ports.onThisPhone} activation={ports.activation}/> : null,
    dispose: () => {if (disposed) return; disposed = true; saved.dispose();},
  } satisfies NativeRouteRenderer & {dispose: () => void});
}
