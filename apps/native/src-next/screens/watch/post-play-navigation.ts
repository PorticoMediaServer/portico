import {sameFence, type PlaybackFence} from '../../../../../packages/player-core/src/identity';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {sameAuthority} from '../../shell/model';

/** Close the exact completed dock before opening a related title. Dock closure
 * is synchronous; its fenced stop/paused-audio restoration finishes in the adapter. */
export function nativePostPlayNavigation(context: Pick<NativeShellBootstrapContext, 'playback' | 'navigation' | 'authority' | 'current'>,
  fence: PlaybackFence, current: () => boolean, navigate: (entry: ContentEntry) => void, reportError: (error: unknown) => void) {
  const authority = context.authority(), route = context.navigation.getSnapshot();
  const onRoute = () => current() && context.current() && sameAuthority(context.authority(), authority)
    && context.navigation.getSnapshot() === route;
  const completed = () => {
    const source = context.playback.getSnapshot();
    return onRoute() && !source.retired && sameFence(source.fence, fence) && source.playback.phase === 'ended'
      && sameFence(source.dock.local?.fence, fence) && source.dock.local?.expanded === true;
  };
  return {
    current: completed,
    open: (entry: ContentEntry) => {
      if (!completed()) return;
      const transition = context.playback.dock.close(fence);
      const stopping = context.playback.onTransition(transition);
      // A synchronous source or route observer may have superseded the close.
      if (onRoute() && context.playback.dock.getSnapshot() === transition.state) navigate(entry);
      const destination = context.navigation.getSnapshot();
      void stopping.catch(error => {
        if (current() && context.current() && sameAuthority(context.authority(), authority)
          && context.navigation.getSnapshot() === destination && context.playback.dock.getSnapshot() === transition.state) reportError(error);
      });
    },
  };
}
