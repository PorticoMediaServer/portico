import type {LocalSession} from '@core/index.ts';
import type {SelectionContext} from '@core/selection-model/index.ts';

/** Only the authenticated local or hosted authority may bind a selection controller. */
export function shellSelectionScope(viewer: LocalSession['viewer'], viewId: string): Omit<SelectionContext['scope'], 'privacyFence'> {
  const {serverId, authority, accountId, profileId} = viewer;
  if (authority !== 'local' && authority !== 'hosted') throw new Error('unsupported-home-authority');
  return {serverId, authority, accountId, profileId, viewId};
}
