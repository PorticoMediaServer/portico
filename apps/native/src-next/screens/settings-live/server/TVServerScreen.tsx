import React, {useEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {ScrollView, Text, View} from 'react-native';
import QRCode from 'react-native-qrcode-svg';
import {LibraryManagementService} from '../../../../../../packages/client-core/src/library-management';
import {Button, Focusable} from '../../../kit/controls';
import {FocusRegion} from '../../../kit/focus';
import {Icon} from '../../../kit/Icon';
import {ServerBand, ServerHeader} from '../../../kit/shell';
import {shellType} from '../../../kit/shell/visual';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {tvFocusIds} from '../../../shell/tv-focus';
import {publicTVHandoffAddress} from '../../../tv/auth/TVHandoff';
import {TVRemoteAccessPanel} from './TVRemoteAccessPanel';
import {TVPeoplePanel} from './TVPeoplePanel';
import {TVServerPageMenu, tvServerPageDefinition, type TVServerPage} from './TVServerPageMenu';
import {TVServerReadOnlyPanel} from './TVServerReadOnlyPanel';
import {TVServerSettingsPage} from './TVServerSettingsPage';
import {TVMetadataPanel} from './TVMetadataPanel';
import {TVLiveDefaults, TVRecordingDefaults} from './TVLiveDefaults';
import {NativeDashboardScreen} from '../DashboardScreen';
import type {NativeDashboardController} from '../dashboard-controller';
import {createTVServerReader, nativeServerAdmission} from './tv-server-reader';
import {NativeScreenLifetime} from '../lifetime';
import {ownerLibraryCounts, ownerLibraryKind, ownerLibraryState} from '../owner-libraries/presentation';
import type {NativeSettingsLivePorts} from '../ports';
import {Screen, useScreenEntry} from '../Screen';

// DECISIONS 28 Oct (replaces G35): the owner changes these pages' settings on TV. Dashboard,
// Remote sources and Sign-in stay view only; an administrator's pages are always view only.
const tvEditable: ReadonlySet<TVServerPage> = new Set(['general', 'history', 'libraries', 'metadata', 'segments', 'live', 'people', 'remote', 'integrations', 'streaming', 'storage']);

/** TV36: the library directory is GET-only; the owner's settings below it edit through the
 * phone's revisioned forms. Composition admits only the owner's primary profile, and an
 * administrator's pages mount no writer at all. */
export function NativeTVServerScreen({ports, runtime, initialPage = 'general', dashboard}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; initialPage?: TVServerPage; dashboard: NativeDashboardController}) {
  const theme = useTheme(), t = runtime.text.t;
  const [page, setPage] = useState<TVServerPage>(initialPage);
  const lifetime = useMemo(() => new NativeScreenLifetime(ports.context), [ports]);
  const admission = nativeServerAdmission(ports.context.account, lifetime);
  const role: 'owner' | 'administrator' = admission === 'owner' ? 'owner' : 'administrator';
  const owner = admission === 'owner', editing = owner && tvEditable.has(page);
  // Dashboard and People are shared with administrators; the other read-only
  // settings retain their existing owner admission.
  const audience = owner && page !== 'people' && page !== 'dashboard' ? 'owner' : 'administrators';
  const service = useMemo(() => new LibraryManagementService({
    api: createTVServerReader(ports.context, lifetime),
    scope: {serverId: lifetime.authority.serverId, viewerId: JSON.stringify([lifetime.authority.epoch, lifetime.authority.accountId, lifetime.authority.profileId])},
  }), [ports, lifetime]);
  const state = useSyncExternalStore(service.subscribe, service.getSnapshot).directory;
  const ids = useMemo(() => tvFocusIds(lifetime.authority), [lifetime]);
  const column = ids.surface('server.settings.pages'), rows = ids.surface(`server.settings.${page}`);
  const enter = useScreenEntry({regionId: column, childId: page}, lifetime.current);
  useEffect(() => {
    const retire = () => service.cancel();
    lifetime.signal.addEventListener('abort', retire, {once: true});
    // Libraries is the owner's page; an administrator's screen never asks the
    // catalogue reader for it, and the reader would refuse that path anyway.
    if (owner) void service.loadLibraries();
    return () => {lifetime.signal.removeEventListener('abort', retire); service.dispose(); lifetime.dispose();};
  }, [owner, service, lifetime]);
  const address = useMemo(() => {
    if (page !== 'libraries' && page !== 'remote') return undefined;
    try {return publicTVHandoffAddress(new URL(page === 'libraries' ? '/settings/server-libraries' : '/settings/server-remote', ports.context.api.baseUrl).href);} catch {return undefined;}
  }, [ports, page]);
  return <Screen style={{paddingTop: 56}}>
    <ServerBand viewerRole={role} audience={audience}/>
    <View style={{flex: 1, flexDirection: 'row', gap: 80}}>
      <TVServerPageMenu role={role} page={page} serverName={ports.serverName()} runtime={runtime} column={column} rows={rows} rail={ids.rail} onLayout={enter}
        onSelect={next => {if (lifetime.current() && (owner || next === 'dashboard' || next === 'people')) setPage(next);}}/>
      {page === 'dashboard' ? <NativeDashboardScreen controller={dashboard} ports={ports} runtime={runtime} deniedFallback={null} tvContainer={{column, rows}}/> : <ScrollView style={{flex: 1}} contentContainerStyle={{paddingBottom: 50}}>
        <View style={{height: 60, overflow: 'hidden'}}><ServerHeader serverName={ports.serverName()} viewerRole={role} audience={audience} viewOnly={!editing}/></View>
        <Text accessibilityRole="header" style={[shellType(44, 800), {color: theme.colors.text, marginBottom: 28}]}>{t(tvServerPageDefinition(page).label)}</Text>
        <FocusRegion key={page} id={rows} orientation="vertical" edges={{left: column}}>
          {page === 'people' ? <><TVPeoplePanel ports={ports} runtime={runtime} lifetime={lifetime}/>{editing && <TVServerSettingsPage page="people" ports={ports} runtime={runtime}/>}</>
          : page === 'remote' ? editing ? <><TVRemoteAccessPanel ports={ports} runtime={runtime} lifetime={lifetime} statusOnly/><TVServerSettingsPage page="remote" ports={ports} runtime={runtime}/></> : <TVRemoteAccessPanel ports={ports} runtime={runtime} lifetime={lifetime}/>
          : editing && page === 'metadata' ? <TVMetadataPanel ports={ports} runtime={runtime}/>
          : editing && page === 'live' ? <><TVServerReadOnlyPanel page="live" ports={ports} runtime={runtime} lifetime={lifetime}/><TVLiveDefaults ports={ports} runtime={runtime}/><TVRecordingDefaults ports={ports} runtime={runtime}/></>
          : editing && (page === 'segments' || page === 'streaming') ? <TVServerSettingsPage page={page} ports={ports} runtime={runtime}/>
          : editing && (page === 'general' || page === 'storage' || page === 'history' || page === 'integrations') ? <><TVServerReadOnlyPanel page={page} ports={ports} runtime={runtime} lifetime={lifetime} statusOnly/><TVServerSettingsPage page={page} ports={ports} runtime={runtime}/></>
          : page !== 'libraries' ? <TVServerReadOnlyPanel page={page} ports={ports} runtime={runtime} lifetime={lifetime}/> : <>
          {state.phase === 'loading' && <StateView title={t('status.loading')}/>}
          {state.error && <StateView {...runtime.error(state.error, () => {void service.loadLibraries();})}/>}
          {state.data?.items.map(library => {
            const status = ownerLibraryState(library, {t});
            return <Focusable key={library.id} focusId={library.id} focusKind="row" accessibilityRole="text" accessibilityLabel={[library.name, ...ownerLibraryCounts(library, t), library.source.path, status.text].filter(Boolean).join('. ')} style={{flexDirection: 'row', gap: 20, alignItems: 'center', padding: 22, marginBottom: 8, borderRadius: 16, backgroundColor: theme.colors.raised}}>
              <Icon name={ownerLibraryKind(library.kind).icon} size={30}/>
              <View style={{flex: 1, gap: 5}}><Text style={[shellType(26, 700), {color: theme.colors.text}]}>{library.name}</Text>
                <Text style={[shellType(20), {color: theme.colors.textSecondary}]}>{ownerLibraryCounts(library, t).join(' · ')}</Text>
                <Text style={[shellType(18), {color: theme.colors.textTertiary}]}>{library.source.path}</Text></View>
              <Text style={[shellType(20, 600), {color: status.live ? theme.colors.accentStrong : theme.colors.textSecondary, maxWidth: '40%'}]}>{status.text}</Text>
            </Focusable>;
          })}
          {state.canPrevious && <Button focusId="previous" variant="glass" onPress={() => {void service.previousLibraries();}}>{t('web.pager.previous')}</Button>}
          {state.data?.nextCursor && <Button focusId="next" variant="glass" onPress={() => {void service.nextLibraries();}}>{t('web.pager.next')}</Button>}
          {editing && <View style={{marginTop: 30}}><TVServerSettingsPage page="libraries" ports={ports} runtime={runtime}/></View>}
          </>}
        </FocusRegion>
        {address && <View style={{flexDirection: 'row', alignItems: 'center', gap: 22, padding: 24, borderRadius: 18, backgroundColor: theme.colors.raised, marginTop: 24}}>
          <View style={{padding: 10, borderRadius: 10, backgroundColor: theme.colors.white}}><QRCode value={address.url} size={100}/></View>
          <View style={{flex: 1, gap: 10}}><Text style={[shellType(24, 700), {color: theme.colors.text}]}>{t('tv.server.handoffTitle')}</Text><Text style={[shellType(20, 400, 28), {color: theme.colors.textSecondary}]}>{t('tv.server.handoffBody', {url: address.display})}</Text></View>
        </View>}
      </ScrollView>}
    </View>
  </Screen>;
}
