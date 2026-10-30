import {DockBar} from './DockBar';
import {MediaBar} from './MediaBar';
import {NowPlayingCard} from './NowPlayingCard';
import {dockView, startingView} from './model';
import type {DockHostProps} from './types';

/** Arbitration belongs to PlaybackDock. This host only renders its one accepted occupant. */
export function DockHost({state, presentations, layout, tvExpanded = false, onTVExpandedChange, tvReturnFocusRef, retainExpandedMusic = false, retainExpandedVideo = false, starting, ...props}: DockHostProps) {
  const real = dockView(state, presentations, retainExpandedMusic, retainExpandedVideo);
  // A start already holding the local slot (even expanded) is no longer "starting".
  const view = real ?? (starting && !state.local && !state.remote ? startingView(starting) : null); if (!view) return null;
  if (layout === 'tv') {
    if (!onTVExpandedChange) throw new TypeError('TV dock requires its focus owner.');
    return <NowPlayingCard {...props} view={view} expanded={tvExpanded} onExpandedChange={onTVExpandedChange} returnFocusRef={tvReturnFocusRef} />;
  }
  return layout === 'phone' ? <MediaBar {...props} view={view} /> : <DockBar {...props} view={view} />;
}
