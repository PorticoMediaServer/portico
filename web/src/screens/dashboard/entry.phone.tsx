import {useShellFacts} from '../../shell/fact-hooks';
import {useEffect, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {captureReportContext} from '@core/report-context.ts';
import {encodedRoutePath} from '../../../../packages/contracts/src/routes.ts';
import {SettingsPageFailure} from '../../kit/settings';
import {StateLayout} from '../../kit/states';
import {router} from '../../router';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {shellLanguage} from '../../shell/settings';
import {createDashboardBindings, type DashboardBindings} from './bindings';
import {DashboardScreen} from './Dashboard';
import {PhonePage, type DashboardPhoneChrome} from './phone';

const noPreferences = () => () => {};
const t = defaultI18n.t;

/** The drawn mobile-web Dashboard is its own page, not a Settings section: same bindings and guards, phone chrome. */
export function DashboardPhoneEntry() {
  const protectedContext = useProtectedWebScreenContext();
  const context = protectedContext?.context;
  useShellFacts(context);
  const preferences = useSyncExternalStore(context?.preferences?.subscribe ?? noPreferences, () => context?.preferences?.getSnapshot('web'), () => undefined);
  const [owned, setOwned] = useState<{context: typeof context; dashboard: DashboardBindings; chrome: DashboardPhoneChrome}>();
  const [failure, setFailure] = useState(false), [retry, setRetry] = useState(0);
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
    const dashboard = createDashboardBindings({api: context.api, events: context.events, scope: {serverId: context.session().viewer.serverId, viewerId: JSON.stringify([context.session().viewer.authority, context.session().viewer.accountId, context.session().viewer.profileId])},
      context: captureReportContext(timeZone), role: permittedRole, isCurrent: current, isMainProfile: () => current() && context.facts.identity.primaryProfile === true,
      artwork: (id, kind, revision) => revision ? context.artwork.foreground(encodedRoutePath('get_items_id_art_kind', {id: encodeURIComponent(id), kind}) + '?' + new URLSearchParams({v: revision}), kind === 'backdrop' ? 800 : 400, kind === 'poster' ? 2 / 3 : kind === 'backdrop' ? 16 / 9 : 1) : undefined,
      effects: {openHome, openSettings, reportError: () => {if (current()) setFailure(true);}, openHistory: () => openSettings('history')}});
    setFailure(false);
    setOwned({context, dashboard, chrome: {get serverName() {return context.facts.identity.serverName;}, audience: permittedRole === 'owner' ? 'owner' : 'administrators'}});
    return () => {active = false; dashboard.dispose();};
  }, [context, preferences, retry, permittedRole, permitted]);
  const home = () => {void router.navigate({to: '/'});};
  // Before the protected context exists this route draws nothing, exactly like SettingsEntry.
  if (!protectedContext) return null;
  const chrome: DashboardPhoneChrome = {serverName: identity?.serverName ?? '', audience: permittedRole === 'owner' ? 'owner' : 'administrators'};
  if (!permitted) return <PhonePage chrome={chrome} onBack={home}>
    <StateLayout icon="i-lock" title={t('web.console.ownerTitle')} body={t('web.console.ownerBody')} primary={{id: 'home', label: t('next.states.goHome'), onPress: home}} />
  </PhonePage>;
  if (failure) return <PhonePage chrome={owned?.chrome ?? chrome} updatedAt={Date.now()} onBack={home} onRefresh={() => setRetry(value => value + 1)}><SettingsPageFailure retry={() => setRetry(value => value + 1)} home={home} /></PhonePage>;
  if (!owned || owned.context !== context) return null;
  return <DashboardScreen bindings={owned.dashboard} phone chrome={owned.chrome} />;
}
