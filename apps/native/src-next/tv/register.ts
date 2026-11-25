/** The TV entry shares the genuine restore/session lifetime and single player.
 * All screen-owned route, destination, and watch registrations are declared in
 * shell/register-routes.ts. */
import {registerNativeRoutes} from '../shell/register-routes';

registerNativeRoutes(true);

export {RestoredShell as TVRoot} from '../shell/RestoredShell';
export {TVCredentialForm, TVCodePresenter, TVVerificationForm, TVProfilePINForm, TVRequiredSetupHandoff, TVManagementHandoff, TVForgotPINHandoff, TVFrontDoor} from './auth';
