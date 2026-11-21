import React from 'react';
import {Button} from '../../../kit/controls';
import {FocusRegion} from '../../../kit/focus';
import {SettingsListFrame, SettingsPageFrame} from '../../../kit/frames';
import {SettingsControlRows, SettingsManagementCard, SettingsNavigationGroup, SettingsPageLink} from '../../../kit/settings/SettingsParts';
import {ServerBand, ServerHeader, SettingsRow} from '../../../kit/shell';
import {StateView} from '../../../kit/states';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {NativeDashboardScreen} from '../../../screens/settings-live/DashboardScreen';
import type {NativeDashboardController} from '../../../screens/settings-live/dashboard-controller';
import {ownerLibraryCounts, ownerLibraryKind, ownerLibraryState} from '../../../screens/settings-live/owner-libraries/presentation';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {TVPeoplePanel} from '../../../screens/settings-live/server/TVPeoplePanel';
import {tvServerPageDefinition, tvServerPagesFor, type TVServerPage} from '../../../screens/settings-live/server/TVServerPageMenu';
import {ServerDetails} from './ServerDetails';
import {useTVServerSettings} from '../../shared/settings/useTVServerSettings';

export function ServerSettingsPage({ports, runtime, dashboard, initialPage = 'general'}: {
  ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; dashboard: NativeDashboardController; initialPage?: TVServerPage;
}) {
  const {t} = runtime.text;
  const {page, role, audience, lifetime, state, service, column, rows, rail, enter, url, select} = useTVServerSettings({ports, runtime, initialPage});
  const serverName = ports.serverName();
  const list = <>
    <ServerHeader serverName={serverName} viewerRole={role} audience={role === 'owner' ? 'owner' : 'administrators'} variant="group"/>
    <FocusRegion id={column} entry={page} orientation="vertical" edges={{left: rail, right: rows}} onLayout={enter}>
      <SettingsNavigationGroup server>
        {tvServerPagesFor(role).map(item => <SettingsPageLink key={item.id} id={item.id} testID={`server-page-${item.id}`}
          selected={page === item.id} title={t(item.label)} icon={item.icon} server
          onFocus={page !== item.id ? () => select(item.id) : undefined} onPress={() => select(item.id)}/>)}
      </SettingsNavigationGroup>
    </FocusRegion>
  </>;
  const body = page === 'dashboard'
    ? <NativeDashboardScreen controller={dashboard} ports={ports} runtime={runtime} deniedFallback={null} tvContainer={{column, rows}}/>
    : <SettingsPageFrame title={t(tvServerPageDefinition(page).label)}
      server={<ServerHeader serverName={serverName} viewerRole={role} audience={audience} viewOnly/>}>
      <SettingsControlRows key={page} id={rows} orientation="vertical" edges={{left: column}}>
        {page === 'people' ? <TVPeoplePanel ports={ports} runtime={runtime} lifetime={lifetime}/>
          : page !== 'libraries' ? <ServerDetails page={page} ports={ports} runtime={runtime} lifetime={lifetime}/>
          : <>
            {state.phase === 'loading' && <StateView title={t('status.loading')}/>}
            {state.error && <StateView {...runtime.error(state.error, () => {void service.loadLibraries();})}/>}
            {state.data?.items.map(library => {
              const status = ownerLibraryState(library, {t});
              const counts = ownerLibraryCounts(library, t);
              return <SettingsRow key={library.id} testID={library.id} readable first label={library.name} icon={ownerLibraryKind(library.kind).icon}
                description={[counts.join(' · '), library.source.path].filter(Boolean)}
                value={status.text} accessibilityLabel={[library.name, ...counts, library.source.path, status.text].filter(Boolean).join('. ')}/>;
            })}
            {state.canPrevious && <Button focusId="previous" variant="glass" onPress={() => {void service.previousLibraries();}}>{t('web.pager.previous')}</Button>}
            {state.data?.nextCursor && <Button focusId="next" variant="glass" onPress={() => {void service.nextLibraries();}}>{t('web.pager.next')}</Button>}
          </>}
      </SettingsControlRows>
      {url && <SettingsManagementCard url={url} text={runtime.text} server/>}
    </SettingsPageFrame>;
  return <SettingsListFrame testID="native-tv-server-settings" bar={{title: t('next.shell.serverSettings')}}
    backdrop={<ServerBand viewerRole={role} audience={audience}/>} list={list} page={body}/>;
}
