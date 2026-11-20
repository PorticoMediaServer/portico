import {useCallback, useSyncExternalStore} from 'react';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRoute} from '../../shell/routes';

/** The navigation revision this route is showing under, while it is the route on top (undefined otherwise). A page that
 * arms its actions from the snapshot it was drawn under re-arms when this changes: coming back to it by the system's
 * own Back (iOS's swipe, Android's Back) shows the same route object under a new revision, and a press must be checked
 * against the page as it is drawn now, not before the reader opened over it. */
export function useRouteRevision(context: Pick<NativeShellBootstrapContext, 'navigation'>, route: NativeShellRoute): number | undefined {
  const read = useCallback(() => {
    const snapshot = context.navigation.getSnapshot();
    return snapshot.route === route ? snapshot.revision : undefined;
  }, [context, route]);
  return useSyncExternalStore(context.navigation.subscribe, read, read);
}
