import {useEffect} from 'react';
import {defineWebScreen} from '../../route-registry';
import {router} from '../../router';
import {currentWebEnvironment} from '../../platform/environment';

/** The drawn Help action is an external destination, not a new in-app page. */
function HelpScreen() {useEffect(() => {location.replace('https://getportico.tv/docs/');}, []); return null;}
/** Server-local account controls are the existing Profile & account section. */
// At web.getportico.tv /account is the Portico Account itself, drawn by the account pages; this
// alias never runs there, even for the moment the router keeps it while leaving (H14).
function AccountScreen() {useEffect(() => {if (!currentWebEnvironment().hostedWeb) void router.navigate({to: '/settings/profile', replace: true});}, []); return null;}

export default [defineWebScreen({id: 'help', component: HelpScreen, chrome: 'standalone'}), defineWebScreen({id: 'account', component: AccountScreen, chrome: 'standalone'})] as const;
