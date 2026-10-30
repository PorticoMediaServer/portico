import {defineWebScreen} from '../../route-registry';
import {useSizeClass} from '../../kit/theme';
import {SettingsEntry} from '../settings/Settings.route';
import {DashboardPhoneEntry} from './entry.phone';

/** One drawn route: inside Settings at desktop width, its own page on a phone. */
function DashboardRoute() {
  return useSizeClass() === 'compact' ? <DashboardPhoneEntry /> : <SettingsEntry />;
}
export default defineWebScreen({id: 'dashboard', component: DashboardRoute});
