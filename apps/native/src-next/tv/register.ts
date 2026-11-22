/** The TV entry shares the genuine restore/session lifetime and single player.
 * Screen-owned registration modules are imported at this platform entry. */
import '../screens/accounts/AccountRoutes';
import '../screens/home/CustomizeRoute';
import '../screens/home/SeeAllRoute';
import '../shell/library-route';
import '../shell/collections-route';
import '../shell/library-overview-route';
// App.tsx loads this only for handsets; the TV rail's Search needs it too.
import '../shell/search-route';
import '../shell/people-route';
import '../shell/live-route';
import '../shell/together-route';
import '../shell/music-route';
import '../shell/audiobooks-route';
import '../shell/saved-destination-route';
import {createNativeWatchBindings, registerNativeWatch} from '../screens/watch';

registerNativeWatch(createNativeWatchBindings);

export {RestoredShell as TVRoot} from '../shell/RestoredShell';
export {TVCredentialForm, TVCodePresenter, TVVerificationForm, TVProfilePINForm, TVRequiredSetupHandoff, TVManagementHandoff, TVForgotPINHandoff, TVFrontDoor} from './auth';
