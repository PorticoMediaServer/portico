import {devE2EUsername, devE2ESignIn} from '../../../../packages/client-core/src/dev-e2e';
import type {ProfileAPI} from '../../../../packages/client-core/src/profile-management';

// Development only: reachable solely through `__PORTICO_DEV_SIGN_IN__` branches, so a bundle
// built without PORTICO_DEV_SIGN_IN=1 (every release bundle) contains none of it. The server
// answers only a loopback peer of a devtrust build with an e2e state directory; an emulator
// reaches it through `adb reverse`. Nobody types or reads a credential.

export const TEST_OWNER_LABEL = 'Sign in as the test owner';

export async function signInTestOwner(api: ProfileAPI, signal: AbortSignal) {
  const username = await devE2EUsername(api, signal);
  if (!username) throw Object.assign(new Error('This server has no test owner.'), {code: 'dev_sign_in_unavailable'});
  return devE2ESignIn(api, username, signal);
}
