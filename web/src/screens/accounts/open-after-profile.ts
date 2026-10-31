import {router} from '../../router';
import {profileSettingsDestination} from '../../route-registry-destination';
import {takeRequestedPath} from '../../platform/requested-path';

/** Where a tab goes once a profile opens: two-step setup when it is owed, otherwise the page it
 * asked for before sign-in or Who's watching came first, otherwise Home. */
export function openAfterProfile(twoStepSetupOwed: boolean): Promise<void> {
  if (twoStepSetupOwed) return router.navigate({...profileSettingsDestination, replace: true});
  const requested = takeRequestedPath();
  return requested ? router.navigate({href: requested, replace: true}) : router.navigate({to: '/', replace: true});
}
