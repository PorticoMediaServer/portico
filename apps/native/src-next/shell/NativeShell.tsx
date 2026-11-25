import React, {useSyncExternalStore} from 'react';
import {OverlaysProvider} from '../kit/overlays';
import {PlayerMorphProvider} from '../kit/dock/PlayerMorphBridge';
import {DockClearanceProvider} from '../kit/dock/DockClearance';
import {ListeningCoverProvider} from '../kit/dock/ListeningCoverBridge';
import {SurfacePaintHost} from '../kit/overlays/SurfacePaintHost';
import {RailHoldProvider} from '../kit/navigation/rail-hold';
import {useTheme} from '../kit/theme';
import {authorityKey} from './model';
import type {NativeShellRuntime} from './runtime';
import {TouchShell} from './TouchShell';
import {TVShell, tvRailScrim} from './TVShell';

export function NativeShell({runtime}: {runtime: NativeShellRuntime}) {
  const snapshot = useSyncExternalStore(runtime.subscribe, runtime.getSnapshot);
  const {isTV} = useTheme();
  const key = authorityKey(snapshot.authority);
  // Permission/profile/server changes cannot reuse component state, focus, selections or images.
  return <OverlaysProvider key={key}><PlayerMorphProvider><ListeningCoverProvider><DockClearanceProvider><SurfacePaintHost><RailHoldProvider>{isTV ? <TVShell key={key} runtime={runtime} snapshot={snapshot}/> : <TouchShell key={key} runtime={runtime} snapshot={snapshot}/>}</RailHoldProvider></SurfacePaintHost></DockClearanceProvider></ListeningCoverProvider></PlayerMorphProvider></OverlaysProvider>;
}

export {TouchShell, TVShell, tvRailScrim};
