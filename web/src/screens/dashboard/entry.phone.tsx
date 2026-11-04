import {useShellFacts} from '../../shell/fact-hooks';
import {useEffect, useState, useSyncExternalStore} from 'react';
import {captureReportContext} from '@core/report-context.ts';
import {encodedRoutePath} from '../../../../packages/contracts/src/routes.ts';
import {SettingsOwnerOnly, SettingsPageFailure} from '../../kit/settings';
import {router} from '../../router';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {profileSettingsDestination} from '../../route-registry-destination';
import {shellLanguage} from '../../shell/settings';
import {createDashboardBindings, type DashboardBindings} from './bindings';
import {DashboardScreen} from './Dashboard';
import {PhonePage, type DashboardPhoneChrome} from './phone';

const noPreferences = () => () => {};

/** The drawn mobile-web Dashboard is its own page, not a Settings section: same bindings and guards, phone chrome. */
export function DashboardPhoneEntry() {
  const protectedContext = useProtectedWebScreenContext();
  const context = protectedContext?.context;
  useShellFacts(context);
  const preferences = useSyncExternalStore(context?.preferences?.subscribe ?? noPreferences, () => context?.preferences?.getSnapshot('web'), () => undefined);
  const [owned, setOwned] = useState<{context: typeof context; dashboard: DashboardBindings; chrome: DashboardPhoneChrome}>();
  const [failure, setFailure] = useState<false | Readonly<{error: unknown}>>(false), [retry, setRetry] = useState(0);
  const identity = context?.facts.identity, viewer = context?.session().viewer;
  const permittedRole: 'owner' | 'admin' | undefined = viewer?.role === 'owner' || viewer?.role === 'admin' ? viewer.role : undefined;
  const permitted = !!context && !!viewer && identity?.primaryProfile === true && identity.capabilities?.manageServer === true && permittedRole !== undefined;
  useEffect(() => {
    if (!context || !permitted || !permittedRole || !context.isCurrent() || !preferences) return;
    let active = true;
    const current = () => active && context.isCurrent();
    const language = shellLanguage(preferences), timeZone = language.region.timeZone || Intl.DateTimeFormat().resolvedOptions().timeZone;
    const openHome = () => {if (current()) void router.navigate({to: '/'});};
    const openSettings = (section: string) => {if (current()) void router.navigate({to: '/settings', search: () => ({scope: 'server', section})});};
    const dashboard = createDashboardBindings({api: context.api, queries: context.freshness?.queries, events: context.events, scope: {serverId: context.session().viewer.serverId, viewerId: JSON.stringify([context.session().viewer.authority, context.session().viewer.accountId, context.session().viewer.profileId])},
      context: captureReportContext(timeZone), role: permittedRole, isCurrent: current, isMainProfile: () => current() && context.facts.identity.primaryProfile === true,
      artwork: (id, kind, revision) => revision ? context.artwork.foreground(encodedRoutePath('get_items_id_art_kind', {id: encodeURIComponent(id), kind}) + '?' + new URLSearchParams({v: revision}), kind === 'backdrop' ? 800 : 400, kind === 'poster' ? 2 / 3 : kind === 'backdrop' ? 16 / 9 : 1) : undefined,
      effects: {openHome, openSettings, reportError: error => {if (current()) setFailure({error});}, openHistory: () => openSettings('history')}});
    setFailure(false);
    setOwned({context, dashboard, chrome: {get serverName() {return context.facts.identity.serverName;}, audience: permittedRole === 'owner' ? 'owner' : 'administrators'}});
    return () => {active = false; dashboard.dispose();};
  }, [context, preferences, retry, permittedRole, permitted]);
  const home = () => {void router.navigate({to: '/'});};
  const mySettings = () => {void router.navigate(profileSettingsDestination);};
  // Before the protected context exists this route draws nothing, exactly like SettingsEntry.
  if (!protectedContext) return null;
  const chrome: DashboardPhoneChrome = {serverName: identity?.serverName ?? '', audience: permittedRole === 'owner' ? 'owner' : 'administrators'};
  // States frame 13c: a member (or a profile that may not manage the server) at an owner page.
  if (!permitted) return <PhonePage chrome={chrome} onBack={home}>
    <SettingsOwnerOnly serverName={identity?.serverName ?? ''} openMySettings={mySettings} home={home} />
  </PhonePage>;
  if (failure) return <PhonePage chrome={owned?.chrome ?? chrome} updatedAt={Date.now()} onBack={home} onRefresh={() => setRetry(value => value + 1)}><SettingsPageFailure retry={() => setRetry(value => value + 1)} home={home} error={failure.error} server={{name: identity?.serverName ?? '', openMySettings: mySettings}} /></PhonePage>;
  if (!owned || owned.context !== context) return null;
  return <DashboardScreen bindings={owned.dashboard} phone chrome={owned.chrome} />;
}
