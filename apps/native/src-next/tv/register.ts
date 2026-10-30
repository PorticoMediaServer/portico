/** The TV entry shares the genuine restore/session lifetime and single player.
 * Screen-owned registration modules are imported at this platform entry. */
import '../screens/accounts/AccountRoutes';
import '../screens/home/CustomizeRoute';
import '../screens/home/SeeAllRoute';
import '../screens/library/register';
import '../screens/collections/register';
import '../screens/library/OverviewRoute';
// App.tsx loads this only for handsets; the TV rail's Search needs it too.
import '../screens/search/register';
import '../screens/people/register';
import '../screens/settings-live/LiveRoute';
import '../screens/settings-live/TogetherRoute';
import '../screens/destinations/music/register';
import '../screens/destinations/audiobooks/register';
import '../screens/destinations/saved/register';
import {createNativeWatchBindings, registerNativeWatch} from '../screens/watch';

registerNativeWatch(createNativeWatchBindings);

export {RestoredShell as TVRoot} from '../shell/RestoredShell';
export {TVCredentialForm, TVCodePresenter, TVVerificationForm, TVProfilePINForm, TVRequiredSetupHandoff, TVManagementHandoff, TVForgotPINHandoff, TVFrontDoor} from './auth';
