import React from 'react';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {StateView} from '../../../kit/states';
import {TVPanelTransition} from '../../../kit/overlays/TVPanelMotion';
import {NativeVersionChooser} from '../../../kit/player/VersionChooser';
import {NativeDiscTitlesSheet} from '../../../kit/player/DiscTitlesSheet';
import {WatchFileInfoSurface, WatchManageDialog, WatchTitleMenu, watchPaintPermit} from '../../../kit/player/WatchOverlayViews';
import type {NativeScreenServices} from '../../../screens/screen-services';
import type {NativeWatchOverlay, NativeWatchPresenters} from '../../../screens/watch/presenters';
// Named through pages/shared so the screen harness loads the hooks for real when a test loads this page.
import {useWatchManage, useWatchOverlays} from '../../../pages/shared/player/useWatchOverlays';
import {watchEntryName, watchTitleMenu} from '../../../pages/shared/player/watch-title-menu';

function WatchManage(props: {value: NativeWatchOverlay; presenters: NativeWatchPresenters; common: NativeScreenServices}) {
  return <WatchManageDialog {...useWatchManage(props)}/>;
}

/** The player's overlays: a disc's titles, Play a different version, File info, the manage dialog and the title menu. */
export function WatchOverlaysPage({presenters, runtime, common}: {presenters: NativeWatchPresenters; runtime: NativeShellRuntime; common: NativeScreenServices}) {
  const {value, presence, tv, tvDevice} = useWatchOverlays({presenters, runtime});
  if (!value) return null;
  const transition = (node: React.ReactNode) => tv ? <TVPanelTransition key={value.phase} modal open={presence.open} onExited={presence.exited}>{node}</TVPanelTransition> : node;
  if (value.phase === 'disc-titles' && value.disc) return transition(<NativeDiscTitlesSheet controller={value.disc} paintPermit={watchPaintPermit(value, presenters, runtime)} subject={watchEntryName(value.capture.entry)} close={() => presenters.closeDisc(value)}
    renderError={(error, retry) => <StateView variant="inline" {...common.error(error, retry, 'detail')}/>}/>);
  if (value.phase === 'versions') return transition(<NativeVersionChooser value={value} paintPermit={watchPaintPermit(value, presenters, runtime)} choose={id => {void presenters.chooseVersion(value, id);}} close={() => presenters.closeVersions(value)}/>);
  if (value.phase === 'file-info') return transition(<WatchFileInfoSurface value={value} presenters={presenters}/>);
  if (value.phase !== 'menu' && value.phase !== 'file-pending' && value.phase !== 'versions-pending') return <WatchManage value={value} presenters={presenters} common={common}/>;
  if (value.phase === 'file-pending' || value.phase === 'versions-pending') return null;
  return <WatchTitleMenu menu={watchTitleMenu(value, presenters, runtime, tvDevice)} presenters={presenters} runtime={runtime}/>;
}
