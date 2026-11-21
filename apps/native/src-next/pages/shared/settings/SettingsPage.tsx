import React from 'react';
import {frameSpec} from '@portico/design';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {SettingsPage as Setting} from '../../../../../../packages/client-core/src/presentation/settings-structure';
import {TextField} from '../../../kit/controls';
import {FocusRegion} from '../../../kit/focus';
import {SettingsListFrame, SettingsPageFrame} from '../../../kit/frames';
import {SettingsSectionLabel, SettingsSearchResults, SettingsRowTargetContext, SettingsSheet, ShortcutTiles} from '../../../kit/shell';
import {SettingsProfileCard} from '../../../kit/shell/SettingsProfileCard';
import {ServerHeader} from '../../../kit/shell/ServerIdentity';
import {StateView} from '../../../kit/states';
import {PhoneDashboardCard, SettingsHelp, SettingsKeyHints, SettingsNavigationGroup, SettingsNotificationCount, SettingsPageLink, SettingsSearchGroup} from '../../../kit/settings/SettingsParts';
import {NativeTVKeyboard} from '../../../screens/destinations';
import {nativeSettingsIcon} from '../../../screens/settings-live/settings-icon';
import {ServerLibrariesPage} from '../../touch/settings/ServerLibrariesPage';
import {NativeLiveOwnerScreen} from '../../../screens/settings-live/LiveOwnerScreen';
import {NativeServerTroubleshooting} from '../../../screens/settings-live/server/TroubleshootingPage';
import {ServerStoragePage} from '../../touch/settings/ServerStoragePage';
import {ServerIntegrationsPage} from '../../touch/settings/ServerIntegrationsPage';
import {ServerGeneralPage} from './ServerGeneralPage';
import {ServerRemoteAccessPage} from './ServerRemoteAccessPage';
import {ServerStreamingPage} from '../../touch/settings/ServerStreamingPage';
import {ServerSegmentsPage} from '../../touch/settings/ServerSegmentsPage';
import {ServerPeoplePage} from '../../touch/settings/ServerPeoplePage';
import {NativeServerSignIn} from '../../../screens/settings-live/server/SignInPage';
import {ServerMetadataPage} from '../../touch/settings/ServerMetadataPage';
import {ServerHistoryPage} from '../../touch/settings/ServerHistoryPage';
import {NativeRemoteSourcesScreen} from '../../../screens/settings/remote-sources/NativeRemoteSourcesScreen';
import {NativeDashboardScreen} from '../../../screens/settings-live/DashboardScreen';
import {useSettingsPage, type SettingsProps} from './useSettingsPage';
import {SettingsPreferences} from './SettingsPreferences';

export function NativeSettingsPage(props: SettingsProps) {
  const {ports, controller, runtime, devicePreferences, dashboard, initialChannelId} = props;
  const {theme, t, shell, state, ids, page, compact, pushed, query, setQuery, searchRevision, searching, searchField, keyboard, rows, column,
    selectPage, choose, focusSearch, sheetShortcuts, unreadNotifications, accountGroups, serverGroups, orderedPages, enter, openMatch,
    matches, matchingPages, matchRegions, matchEdges, voiceSearch, sheetChanged, closing, closeSettings, finishClose, phoneBack, signedIn,
    serverPage, viewerRole, dashboardCard, dashboardDenied, openDashboard, openServerPage, updatePending, leaveServerPage, setRemoteBack, setSegmentBack,
    librariesPage, livePage, troubleshootingPage, storagePage, integrationsPage, generalPage, streamingPage, segmentsPage, peoplePage,
    signInPage, metadataPage, historyPage, remoteSourcesPage, remoteAccessPage, dashboardPage, customPage, liveControllers,
    pageScroll, pageScrollLayout, rowTargeting, aimed, reportedOutputs, awayLimitLabel} = useSettingsPage(props);
  const tv = theme.isTV, tvSearch = tv && searching, searchingQuery = query.trim().length >= 2;
  const title = t('next.shell.settings');
  const audience = viewerRole === 'administrator' ? 'administrators' : 'owner';
  const serverHeader = <ServerHeader serverName={ports.serverName()} viewerRole={viewerRole} audience={audience}
    variant={compact ? 'phone' : undefined}/>;
  const retry = state.error !== undefined && <StateView {...runtime.error(state.error, () => {void controller.refresh();}, 'settings')}/>;
  const row = (value: Setting, server = false) => <SettingsPageLink key={value.id} id={value.id} title={t(value.title)}
    icon={nativeSettingsIcon(value.icon)} selected={value.id === page.id} server={server}
    onFocus={tv ? () => selectPage(value.id) : undefined} onPress={() => server ? openServerPage(value.id) : choose(value.id)}/>;
  const search = <FocusRegion id={searchField} entry="input" edges={{left: ids.rail, down: tvSearch ? `${keyboard}.row.0` : column}}>
    <TextField focusId="input" testID="settings-search" variant="find" leadingIcon="i-search" accessibilityLabel={t('next.shell.searchSettings')}
      placeholder={t('next.shell.searchSettings')} value={query} onChangeText={setQuery} showSoftInputOnFocus={!tv} onFocus={focusSearch}/>
  </FocusRegion>;
  const list = tvSearch ? <>
    <NativeTVKeyboard id={keyboard} value={query} onChangeText={setQuery} activation={{visible: true, intent: 'enter', revision: searchRevision}}
      leftRegionId={ids.rail} rightRegionId={matchRegions[0]} width={frameSpec.settings.tvSearchWidth} testID="settings-keyboard"
      onVoice={voiceSearch.start} voiceBusy={voiceSearch.busy}/>
    {voiceSearch.field}
  </> : searchingQuery ? <SettingsSearchResults matches={matches} query={query} onNavigate={openMatch}/> : <>
    <FocusRegion id={column} entry={orderedPages[0]?.id} order={orderedPages.map(value => value.id)} orientation="vertical"
      edges={{left: ids.rail, right: rows, up: searchField}} onLayout={enter}>
      <SettingsSectionLabel label={t('settings.heading.account')} first={!compact}/>
      {accountGroups.map(group => <SettingsNavigationGroup key={group[0]?.id}>{group.map(value => row(value))}</SettingsNavigationGroup>)}
    </FocusRegion>
    {!tv && serverGroups.length > 0 && <SettingsNavigationGroup server>
      {serverHeader}
      {serverGroups.map(group => <SettingsNavigationGroup key={group[0]?.id}>{group.map(value => row(value, true))}</SettingsNavigationGroup>)}
    </SettingsNavigationGroup>}
  </>;
  const profile = compact && !searchingQuery && <SettingsProfileCard testID="settings-profile-card" profile={shell.profile} locked={shell.profile.pinRequired} description={signedIn} onPress={() => choose('profile')}/>;
  const shortcuts = compact && !searchingQuery && <>
    {dashboardCard && <PhoneDashboardCard t={t} update={updatePending} onPress={openDashboard}/>}
    {!!sheetShortcuts.length && <ShortcutTiles testID="settings-shortcuts" actions={sheetShortcuts.map(action => action.id === 'notifications' && unreadNotifications > 0
      ? {...action, trailing: <SettingsNotificationCount count={defaultI18n.number(unreadNotifications)}/>} : action)}/>}
  </>;
  const custom = signInPage ? <NativeServerSignIn ports={ports} runtime={runtime} compactHeader={compact} onOpenPage={choose}/>
    : dashboardPage ? <NativeDashboardScreen controller={dashboard!} ports={ports} runtime={runtime} inline onDenied={dashboardDenied} deniedFallback={null}/>
    : remoteSourcesPage ? <NativeRemoteSourcesScreen ports={ports} runtime={runtime} onBackHandlerChange={setRemoteBack}/>
    : livePage ? <NativeLiveOwnerScreen controller={liveControllers!.owner} recordings={liveControllers!.recordings} ports={ports} runtime={runtime}
      onClose={leaveServerPage} initialChannelId={initialChannelId}/>
    : troubleshootingPage ? <NativeServerTroubleshooting ports={ports} runtime={runtime} compactHeader={compact}/>
    : undefined;
  const preferenceProps = {ports, controller, runtime, devicePreferences, reportedOutputs, awayLimitLabel,
    onStreaming: () => choose('streaming'), onQuality: () => choose('quality')};
  const settingsPage = (!compact || pushed) && (generalPage ? <ServerGeneralPage ports={ports} runtime={runtime} server={serverHeader} retry={retry}
    back={compact ? {label: title, onPress: phoneBack} : undefined}/>
    : remoteAccessPage ? <ServerRemoteAccessPage ports={ports} runtime={runtime} server={serverHeader} retry={retry}
    back={compact ? {label: title, onPress: phoneBack} : undefined}/>
    : streamingPage ? <ServerStreamingPage ports={ports} runtime={runtime} server={serverHeader} retry={retry}
    back={compact ? {label: title, onPress: phoneBack} : undefined}/>
    : historyPage ? <ServerHistoryPage ports={ports} runtime={runtime} server={serverHeader} retry={retry}
    back={compact ? {label: title, onPress: phoneBack} : undefined}/>
    : integrationsPage ? <ServerIntegrationsPage ports={ports} runtime={runtime} compact={compact} server={serverHeader} retry={retry}
    back={compact ? {label: title, onPress: phoneBack} : undefined}/>
    : segmentsPage ? <ServerSegmentsPage lifetime={controller.lifetime} ports={ports} runtime={runtime} server={serverHeader} retry={retry} onBackHandlerChange={setSegmentBack}
      back={compact ? {label: title, onPress: phoneBack} : undefined}/>
    : metadataPage ? <ServerMetadataPage ports={ports} runtime={runtime} server={serverHeader} retry={retry} onLibraries={() => choose('libraries')}
      back={compact ? {label: title, onPress: phoneBack} : undefined}/>
    : storagePage ? <ServerStoragePage ports={ports} runtime={runtime} server={serverHeader} retry={retry} onOpenPage={choose}
      back={compact ? {label: title, onPress: phoneBack} : undefined}/>
    : librariesPage ? <ServerLibrariesPage ports={ports} runtime={runtime} server={serverHeader} retry={retry}
      back={compact ? {label: title, onPress: phoneBack} : undefined}/>
    : peoplePage ? <ServerPeoplePage ports={ports} runtime={runtime} server={serverHeader} retry={retry}
      back={compact ? {label: title, onPress: phoneBack} : undefined}/> : <SettingsPageFrame title={t(page.title)}
    back={compact ? {label: title, onPress: phoneBack} : undefined}
    server={compact && serverPage ? serverHeader : undefined} hideHeader={tvSearch || customPage && !compact}
    intro={page.id === 'playback' ? t('settings.playback.deviceIntro') : undefined}
    scrollRef={pageScroll} onScrollLayout={pageScrollLayout}
    panel={custom ? <>{retry}{custom}</> : undefined}>
    {!custom && retry}
    {tvSearch ? <>
      {searchingQuery && !!matches.length && <SettingsHelp testID="settings-search-count">{t('next.shell.tvSettingsMatchCount', {count: matches.length})}</SettingsHelp>}
      {searchingQuery && !matches.length && <StateView title={t('next.states.noMatches', {query: query.trim()})}/>}
      {matchingPages.map(value => <SettingsSearchGroup key={value.id} label={t(value.title)}>
        <SettingsRowTargetContext.Provider value={{open: rowId => openMatch(value.id, rowId)}}>
          <SettingsPreferences {...preferenceProps} page={value} region={ids.surface(`settings.matches.${value.id}`)}
            column={keyboard} verticalEdges={matchEdges(ids.surface(`settings.matches.${value.id}`))}/>
        </SettingsRowTargetContext.Provider>
      </SettingsSearchGroup>)}
      <SettingsHelp testID="settings-search-help">{t('next.shell.tvSettingsSearchHelp')}</SettingsHelp>
    </> : !custom && <SettingsRowTargetContext.Provider value={rowTargeting}>
      <SettingsPreferences key={page.id} {...preferenceProps} page={page} region={rows} column={column} aimedRow={aimed}/>
    </SettingsRowTargetContext.Provider>}
  </SettingsPageFrame>);
  const content = <SettingsListFrame bar={{title, action: {label: t('action.done'), onPress: closeSettings, testID: 'settings-sheet-done'}}}
    search={search} profile={profile} shortcuts={shortcuts} list={list} page={settingsPage || undefined} searching={tvSearch} closing={closing}
    hints={tv && !tvSearch ? <SettingsKeyHints t={t}/> : undefined}/>;
  return tv ? content : <SettingsSheet open={!closing} onExited={finishClose} host="inline" presentation={compact ? 'sheet' : 'page'}
    profile={null} shortcuts={null} scrollable={false} contentClearsBottom testID="settings-sheet" showGrabber={compact} headerContent={null}
    onOpenChange={sheetChanged}>{content}</SettingsSheet>;
}
