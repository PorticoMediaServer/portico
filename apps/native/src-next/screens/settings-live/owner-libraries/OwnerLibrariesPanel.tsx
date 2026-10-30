import {jobStateName} from '../../../../../../packages/client-core/src/server-admin/console-catalogue';
import type {LibraryContentApi} from '../../../../../../packages/client-core/src/library-content';
import {EpisodeThumbnailsRow} from './EpisodeThumbnailsRow';
import type {AnalysisPolicy} from '../../../../../../packages/client-core/src/remote-sources';
import {MissingTitlesTab} from '../server/storage/MissingTitlesTab';
import {loadInitialOwnerLibrary} from './initial-library';
import {screenScopeEnded, type NativeScreenLifetime} from '../lifetime';
import React, {useCallback, useEffect, useLayoutEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {BackHandler, ScrollView, Text, View} from 'react-native';
import type {ManagedLibrary} from '../../../../../../packages/client-core/src/library-management';
import type {InventorySource} from '../../../../../../packages/client-core/src/library-inventory';
import type {LibraryAdminDocument, LibrarySettings} from '../../../../../../packages/client-core/src/administration';
import type {LibraryMetadataAgent} from '../../../../../../packages/client-core/src/server-admin/metadata-agent';
import {librarySonicStatus} from '../../../../../../packages/client-core/src/server-admin/library-sonic-words';
import type {LibrarySonicDocument} from '../../../../../../packages/client-core/src/server-admin/library-sonic';
import {readLibrarySonicSettingsBinding} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n} from '../../../../../../packages/i18n/src/index';
import {Button} from '../../../kit/controls/Button';
import {Focusable} from '../../../kit/controls/Focusable';
import {Select} from '../../../kit/controls/Select';
import {Switch} from '../../../kit/controls/Switch';
import {Tabs} from '../../../kit/controls/Tabs';
import {TextField} from '../../../kit/controls/TextField';
import {CoverFan} from '../../../kit/artwork';
import {BottomSheet} from '../../../kit/overlays/BottomSheet';
import {Menu} from '../../../kit/overlays/Menu';
import {Icon} from '../../../kit/Icon';
import {SkeletonBlock, StateView} from '../../../kit/states';
import {ServerHeader} from '../../../kit/shell/ServerIdentity';
import {alpha, shellType} from '../../../kit/shell/visual';
import {useTheme} from '../../../kit/theme';
import type {NativeSettingsLivePorts} from '../ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {AddFolderPage} from './AddFolderPage';
import {AddLibraryPage, MetadataAgentChoice} from './AddLibraryPage';
import {LibraryGroup, LibraryRow, SectionTitle} from './controls';
import {LibrarySettingsRows} from './LibrarySettingsRows';
import {libraryListState} from './library-settings-model';
import {NativeOwnerLibrariesController, sonicPageLease, type NativeOwnerLibrariesSnapshot, type SonicPageLease} from './controller';
import {ownerFolderLocation, ownerFolderState, ownerIssueReason, ownerLibraryAudience, ownerLibraryCounts, ownerLibraryInterval, ownerLibraryKind, ownerLibraryScan, ownerLibrarySquare, ownerLibraryState, ownerSkippedFiles} from './presentation';

type Translate = typeof defaultI18n.t;
type ViewerRole = 'owner' | 'administrator' | 'member';

/** Settings › Server › Libraries: the owner's list and one library's page, on the phone. */
export function NativeOwnerLibrariesPanel({ports, runtime, initialLibraryId, lifetime}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; initialLibraryId?: string; lifetime?: NativeScreenLifetime}) {
  const theme = useTheme(), t = runtime.text.t, context = ports.context, authority = context.authority();
  const transport = useMemo(() => {
    const originalApi = context.api;
    let active = true;
    const requireCurrent = () => {if (!active || !context.current() || context.authority() !== authority) throw screenScopeEnded();lifetime?.assert();};
    const api: LibraryContentApi = {
      ...(originalApi.acknowledgeUserMutation ? {acknowledgeUserMutation: async (id: string) => {
        requireCurrent(); await originalApi.acknowledgeUserMutation(id); requireCurrent();
      }} : {}),
      ...(originalApi.awaitUserMutationsReady ? {awaitUserMutationsReady: async () => {
        requireCurrent(); await originalApi.awaitUserMutationsReady(); requireCurrent();
      }} : {}),
      ...(originalApi.getUserMutationCoordinator ? {getUserMutationCoordinator: () => {
        requireCurrent(); const value = originalApi.getUserMutationCoordinator(); requireCurrent(); return value;
      }} : {}),
      ...(originalApi.getUserMutationScope ? {getUserMutationScope: () => {
        requireCurrent(); const value = originalApi.getUserMutationScope(); requireCurrent(); return value;
      }} : {}),
      request: async <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
        requireCurrent();
        const value = await (lifetime ? lifetime.request(next => originalApi.request<T>(path, method, body, next), signal) : originalApi.request<T>(path, method, body, signal));
        requireCurrent(); return value;
      },
    };
    return {api, retire: () => {active = false;}};
  }, [context, lifetime, authority]);
  const api = transport.api;
  useLayoutEffect(() => transport.retire, [transport]);
  const scope = useMemo(() => ({serverId: authority.serverId, viewerId: JSON.stringify([authority.epoch, authority.accountId, authority.profileId])}), [authority]);
  const controller = useMemo(() => new NativeOwnerLibrariesController(api, scope, ports.requestId), [api, scope, ports]);
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const viewerRole: ViewerRole = context.account.account.role === 'owner' ? 'owner' : context.account.canManage ? 'administrator' : 'member';
  const parentCurrent = useCallback(() => context.current() && context.authority() === authority, [context, authority]);
  const initialOwnerLibrary = context.current() && context.account.canManage && context.account.account.role === 'owner' && !context.account.account.disabled && context.account.account.primaryProfileId === authority.profileId ? initialLibraryId : undefined;
  const [openId, setOpenId] = useState<string | undefined>(initialOwnerLibrary), [tab, setTab] = useState('general');
  const [adding, setAdding] = useState(false), [folderForm, setFolderForm] = useState<{source?: InventorySource}>(),
    [rename, setRename] = useState(false), [leaving, setLeaving] = useState(false), [removingFolder, setRemovingFolder] = useState<InventorySource>(),
    [folderMenu, setFolderMenu] = useState<InventorySource>(), [libraryMenu, setLibraryMenu] = useState<ManagedLibrary>(), [menuOnPage, setMenuOnPage] = useState(false), [skipped, setSkipped] = useState(false);

  useEffect(() => {void loadInitialOwnerLibrary(controller, initialOwnerLibrary); return () => controller.dispose();}, [controller, initialOwnerLibrary]);

  const libraries = state.directory.data?.items ?? [];
  const library = libraries.find((value: ManagedLibrary) => value.id === openId) ?? (state.selectedId === openId ? state.selected.data?.library : undefined);
  const document = state.selectedId === openId ? state.document : undefined;
  const open = (id: string) => {setOpenId(id); setTab('general'); void controller.load(id);};

  const back = useCallback(() => {
    if (skipped) {setSkipped(false); return true;}
    if (leaving) {setLeaving(false); return true;}
    if (removingFolder) {setRemovingFolder(undefined); return true;}
    if (rename) {setRename(false); return true;}
    if (folderForm) {setFolderForm(undefined); return true;}
    if (adding) {setAdding(false); return true;}
    if (openId) {setOpenId(undefined); return true;}
    return false;
  }, [skipped, leaving, removingFolder, rename, folderForm, adding, openId]);

  useEffect(() => {
    const subscription = BackHandler.addEventListener('hardwareBackPress', () => back());
    return () => subscription.remove();
  }, [back]);

  if (adding) return <AddLibraryPage api={api} serverName={context.serverName} ports={ports} runtime={runtime} busy={state.busy} error={state.error}
    onCancel={() => setAdding(false)}
    onCreate={input => {void controller.create(input).then(id => {if (!id) return; setAdding(false); open(id);});}}/>;

  if (folderForm && document) return <AddFolderPage api={api} serverName={context.serverName} ports={ports} runtime={runtime} source={folderForm.source} expectedRevision={state.inventory?.revision}
    busy={state.busy} error={state.error} onCancel={() => setFolderForm(undefined)}
    onSave={input => {void controller.saveFolder(folderForm.source?.id, input).then(ok => {if (ok) setFolderForm(undefined);});}}/>;

  if (library) return <>
    <LibraryPage api={api} library={library} document={document} state={state} tab={tab} tabChange={setTab} t={t} runtime={runtime} serverName={context.serverName} viewerRole={viewerRole}
      back={() => setOpenId(undefined)} menu={() => {setMenuOnPage(true); setLibraryMenu(library);}} scan={() => {void controller.scanNow();}} cancel={() => {void controller.cancelScan();}}
      dashboard={() => {context.navigation.open({kind: 'server-dashboard'}, authority);}}
      renameLibrary={() => setRename(true)} removeLibrary={() => setLeaving(true)} addFolder={() => setFolderForm({})} folderMenu={setFolderMenu} skipped={() => setSkipped(true)}
      moreUnmatched={() => {void controller.loadUnmatchedMore();}}
      save={patch => {void controller.save(patch);}} saveAgent={id => {void controller.saveAgent(id);}} scanAll={() => {void controller.scanAll();}}
      parentCurrent={parentCurrent} retryInventory={() => {void controller.reloadInventory();}} fetchLyrics={(document, current) => controller.fetchLyrics(document, current)}
      strmChange={(enabled, document, policy, lease) => {void controller.setStrm(enabled, document, policy, lease);}} strmRetry={document => {void controller.reloadStrm(document);}}
      sonicToggle={(enabled, document, sonic, lease) => {void controller.setSonicEnabled(enabled, document, sonic, lease);}}
      sonicRetry={(document, sonic, lease) => {void controller.retrySonic(document, sonic, lease);}}
      sonicReload={(document, sonic, lease) => {void controller.reloadSonic(document, sonic, lease);}}/>
    <SkippedSheet open={skipped} onOpenChange={setSkipped} state={state} t={t} scan={() => {void controller.scanNow();}} more={() => {void controller.loadSkippedMore();}}/>
    <RemoveLibrarySheet open={leaving} onOpenChange={setLeaving} library={library} busy={state.busy} error={state.error} t={t}
      remove={confirmation => {void controller.remove(confirmation).then(ok => {if (ok) {setLeaving(false); setOpenId(undefined);}});}}/>
    <RenameSheet open={rename} onOpenChange={setRename} library={library} busy={state.busy} error={state.error} t={t}
      save={name => {void controller.rename(name).then(ok => {if (ok) setRename(false);});}}/>
    <RemoveFolderSheet open={Boolean(removingFolder)} onOpenChange={() => setRemovingFolder(undefined)} source={removingFolder} busy={state.busy} error={state.error} t={t}
      remove={() => {const source = removingFolder; if (source) void controller.removeFolder(source.id).then(ok => {if (ok) setRemovingFolder(undefined);});}}/>
    <Menu open={Boolean(folderMenu)} onOpenChange={() => setFolderMenu(undefined)} title={folderMenu?.name || folderMenu?.path}
      items={folderMenu ? [
        {id: 'scan', label: t('web.libraries.scanFolder'), icon: 'i-refresh', onSelect: () => {void controller.scanFolder(folderMenu.id);}},
        {id: 'check', label: t('web.libraries.checkAvailability'), icon: 'i-disk', onSelect: () => {void controller.checkFolder(folderMenu.id);}},
        {id: 'edit', label: t('web.libraries.editFolder'), icon: 'i-pencil', onSelect: () => setFolderForm({source: folderMenu})},
        {id: 'remove', label: t('web.libraries.removeFolder'), icon: 'i-trash', destructive: true, onSelect: () => setRemovingFolder(folderMenu)},
      ] : []}/>
  </>;

  return <View style={{flex: 1}}>
    <ScrollView contentContainerStyle={{paddingHorizontal: 20, paddingBottom: 32}}>
      <ServerHeader serverName={context.serverName} viewerRole={viewerRole} audience={viewerRole === 'administrator' ? 'administrators' : 'owner'} variant="phone" style={{marginBottom: 12}}/>
      <Text style={[shellType(13.5, 400, 19), {color: theme.colors.textSecondary}]}>{t('next.ownerLibraries.lede')}</Text>
      <View style={{flexDirection: 'row', marginTop: 14, marginBottom: 4}}>
        <Button size="compact" icon="i-plus" onPress={() => setAdding(true)}>{t('web.empty.addLibrary')}</Button>
      </View>
      <MissingTitlesTab ports={ports} runtime={runtime} t={runtime.text.t as Translate}/>
      {state.directory.phase === 'error' && <StateView {...runtime.error(state.directory.error, () => {void controller.refresh();}, 'server-console')}/>}
      {libraryListState(state.directory.phase, libraries.length) === 'empty' && <LibraryGroup style={{marginTop: 12}}>
        <LibraryRow first label={t('next.ownerLibraries.empty')} help={t('next.ownerLibraries.emptyHelp')}/>
      </LibraryGroup>}
      <LibraryGroup style={{marginTop: 14}}>
        {libraries.map((value: ManagedLibrary, index: number) => <LibraryListRow key={value.id} library={value} first={index === 0} t={t} runtime={runtime}
          state={ownerLibraryState(value, {t, locale: runtime.text.locale, short: true})} onOpen={() => open(value.id)} onMenu={() => {setMenuOnPage(false); setLibraryMenu(value);}}/>)}
        {libraryListState(state.directory.phase, libraries.length) === 'loading' && <View accessibilityLabel={t('next.ownerLibraries.loading')} accessibilityRole="progressbar" style={{padding: 14, gap: 16}}>{[0, 1, 2].map(row => <View key={row} style={{flexDirection: 'row', alignItems: 'center', gap: 12}}><SkeletonBlock width={62} height={66}/><View style={{flex: 1, gap: 8}}><SkeletonBlock width="70%" height={18}/><SkeletonBlock width="45%" height={13}/></View></View>)}</View>}
      </LibraryGroup>
      <Text style={[shellType(12, 500, 17), {color: theme.colors.textTertiary, marginTop: 12, paddingHorizontal: 4}]}>{t('next.ownerLibraries.moveHint')}</Text>
    </ScrollView>
    <Menu open={Boolean(libraryMenu)} onOpenChange={() => setLibraryMenu(undefined)} title={libraryMenu?.name}
      items={libraryMenu ? menuOnPage ? [
        {id: 'rename', label: t('next.ownerLibraries.rename'), icon: 'i-pencil', onSelect: () => setRename(true)},
        {id: 'scanAll', label: t('next.ownerLibraries.scanAll'), icon: 'i-refresh', description: t('next.ownerLibraries.scanAllHelp'), disabled: !libraryMenu.actions.includes('scan'), onSelect: () => {void controller.scanAll();}},
      ] : [
        {id: 'edit', label: t('next.ownerLibraries.rename'), icon: 'i-sliders', onSelect: () => open(libraryMenu.id)},
        {id: 'scan', label: t('web.libraries.scanNow'), icon: 'i-refresh', disabled: !libraryMenu.actions.includes('scan'), onSelect: () => {void controller.scanLibrary(libraryMenu.id);}},
        {id: 'attention', label: t('next.ownerLibraries.tab.attention'), icon: 'i-link', onSelect: () => {open(libraryMenu.id); setTab('metadata');}},
        {id: 'more-separator', separator: true},
        {id: 'moveUp', label: t('server.libraries.moveUp'), icon: 'i-up', onSelect: () => {void controller.move(libraryMenu.id, -1).then(() => setLibraryMenu(undefined));}},
        {id: 'moveDown', label: t('server.libraries.moveDown'), icon: 'i-down', onSelect: () => {void controller.move(libraryMenu.id, 1).then(() => setLibraryMenu(undefined));}},
      ] : []}/>
  </View>;
}

function LibraryListRow({library, first, t, runtime, state, onOpen, onMenu}: {library: ManagedLibrary; first: boolean; t: Translate; runtime: NativeShellRuntime; state: Readonly<{text: string; live: boolean}>; onOpen: () => void; onMenu: () => void}) {
  const theme = useTheme();
  const counts = ownerLibraryCounts(library, t), square = ownerLibrarySquare(library.kind);
  const covers = (library.summary?.coverUrls ?? []).map(path => runtime.artwork(path)).filter((value): value is NonNullable<ReturnType<NativeShellRuntime['artwork']>> => Boolean(value));
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, minHeight: 84, paddingHorizontal: 12, paddingVertical: 10, borderTopWidth: first ? 0 : 1, borderTopColor: theme.colors.lineSoft}}>
    <Focusable accessibilityRole="button" accessibilityLabel={library.name} onPress={onOpen} focusKind="row" focusRadius={10} style={{flex: 1, flexDirection: 'row', alignItems: 'center', gap: 12}}>
      <CoverFan covers={covers} shape={square ? 'square' : 'poster'} variant={square ? 'music' : 'library'} width={62} height={square ? 56 : 66}/>
      <View style={{flex: 1, minWidth: 0}}>
        <Text numberOfLines={1} style={[shellType(16, 700), {color: theme.colors.text}]}>{library.name}</Text>
        {counts.length > 0 && <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: 10, rowGap: 3}}>{counts.filter(Boolean).map((part, index) => <Text key={index} numberOfLines={1} style={[shellType(12.5, 500), {color: theme.colors.textTertiary, marginTop: 2}]}>{part}</Text>)}</View>}
        <Text numberOfLines={1} style={[shellType(12.5, 600), {color: state.live ? theme.colors.accentStrong : theme.colors.textTertiary, marginTop: 3}]}>{state.text}</Text>
      </View>
    </Focusable>
    <Focusable accessibilityRole="button" accessibilityLabel={`${library.name}, ${t('next.ownerLibraries.libraryMenu')}`} onPress={onMenu} focusKind="chip" focusRadius={16} hitSlop={8}
      style={{width: 34, height: 34, borderRadius: 17, alignItems: 'center', justifyContent: 'center', backgroundColor: alpha(theme.colors.text, .06)}}><Icon name="i-more" size={18} color={theme.colors.textSecondary}/></Focusable>
  </View>;
}

function LibraryPage({api, library, document, state, tab, tabChange, t, runtime, serverName, viewerRole, back, menu, scan, cancel, dashboard, renameLibrary, removeLibrary, addFolder, folderMenu, skipped, moreUnmatched, save, saveAgent, scanAll, parentCurrent, retryInventory, fetchLyrics, strmChange, strmRetry, sonicToggle, sonicRetry, sonicReload}: {
  api: LibraryContentApi; library: ManagedLibrary; document?: LibraryAdminDocument; state: NativeOwnerLibrariesSnapshot; tab: string; tabChange: (id: string) => void;
  t: Translate; runtime: NativeShellRuntime; serverName: string; viewerRole: ViewerRole; back: () => void; menu: () => void; scan: () => void; cancel: () => void; dashboard: () => void;
  renameLibrary: () => void; removeLibrary: () => void; addFolder: () => void; folderMenu: (source: InventorySource) => void; skipped: () => void; moreUnmatched: () => void;
  save: (patch: Partial<LibrarySettings>) => void; saveAgent: (id: string) => void; scanAll: () => void; parentCurrent: () => boolean;
  retryInventory: () => void; fetchLyrics: (document: LibraryAdminDocument, current: () => boolean) => Promise<void>;
  strmChange: (enabled: boolean, document: LibraryAdminDocument, policy: AnalysisPolicy, lease: SonicPageLease) => void;
  strmRetry: (document: LibraryAdminDocument) => void;
  sonicToggle: (enabled: boolean, document: LibraryAdminDocument, sonic: LibrarySonicDocument, lease: SonicPageLease) => void;
  sonicRetry: (document: LibraryAdminDocument, sonic: LibrarySonicDocument, lease: SonicPageLease) => void;
  sonicReload: (document: LibraryAdminDocument, sonic: LibrarySonicDocument, lease: SonicPageLease) => void;
}) {
  const theme = useTheme(), active = ownerLibraryScan(library), status = ownerLibraryState(library, {t, locale: runtime.text.locale});
  const counts = ownerLibraryCounts(library, t);
  const tabs = [{id: 'general', label: t('next.ownerLibraries.tab.general')}, {id: 'folders', label: t('next.ownerLibraries.folders')}, {id: 'metadata', label: t('next.ownerLibraries.tab.metadata')},
    {id: 'scanning', label: t('next.ownerLibraries.tab.scanning')}, {id: 'advanced', label: t('next.ownerLibraries.tab.advanced')}];
  return <View style={{flex: 1}}>
    <ScrollView contentContainerStyle={{paddingBottom: 32}}>
      <View style={{flexDirection: 'row', alignItems: 'center', paddingHorizontal: 8, paddingTop: 4}}>
        <Button variant="text" size="compact" icon="i-left" onPress={back}>{t('next.ownerLibraries.backLibraries')}</Button>
      </View>
      <View style={{paddingHorizontal: 20}}>
        <ServerHeader serverName={serverName} viewerRole={viewerRole} audience={viewerRole === 'administrator' ? 'administrators' : 'owner'} variant="group" style={{marginBottom: 12}}/>
        <Text accessibilityRole="header" numberOfLines={2} style={[shellType(24, 800, 29), {color: theme.colors.text, marginBottom: 4}]}>{library.name}</Text>
        <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', columnGap: 16, rowGap: 2}}>
          {counts[0] !== undefined && <Text style={[shellType(14, 700), {color: theme.colors.text}]}>{counts[0]}</Text>}
          {counts.slice(1).map(count => <Text key={count} style={[shellType(14, 500), {color: theme.colors.textSecondary}]}>{count}</Text>)}
          {active && <Text style={[shellType(13, 700), {color: theme.colors.accentStrong}]}>{t('next.ownerLibraries.scanning')}</Text>}
        </View>
        {!active && <Text style={[shellType(13, 500), {color: theme.colors.textTertiary, marginTop: 3}]}>{status.text}</Text>}
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 10, marginTop: 14}}>
          <Button size="compact" icon="i-refresh" variant={active ? 'glass' : 'primary'} disabled={Boolean(active) || !library.actions.includes('scan')} loading={state.busy} onPress={scan}>{t(active ? 'next.ownerLibraries.scanning' : 'web.libraries.scanNow')}</Button>
          <Button variant="icon" size="compact" icon="i-more" accessibilityLabel={t('next.ownerLibraries.libraryMenu')} onPress={menu} style={{backgroundColor: alpha(theme.colors.text, .1)}}/>
        </View>
      </View>
      <Tabs testID="owner-libraries-tabs" style={{marginTop: 12}} value={tab} onValueChange={tabChange} accessibilityLabel={library.name} items={tabs.map(value => ({value: value.id, label: value.label}))}/>
      <View style={{paddingHorizontal: 20, paddingTop: 8}}>
        {state.detail === 'loading' && !document && <Text style={[shellType(13.5, 500), {color: theme.colors.textTertiary}]}>{t('web.libraries.loadingOne')}</Text>}
        {state.detail === 'error' && <StateView {...runtime.error(state.detailError, () => {void runtime.reportError(state.detailError);}, 'server-console')}/>}
        {state.error !== undefined && <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.record}]}>{presentError(state.error, 'library', {operation: 'save'}).body}</Text>}
        {tab === 'general' && <GeneralTab library={library} document={document} t={t} renameLibrary={renameLibrary} removeLibrary={removeLibrary} busy={state.busy} save={save}/>}
        {tab === 'folders' && <FoldersTab state={state} retry={retryInventory} t={t} addFolder={addFolder} folderMenu={folderMenu}/>}
        {tab === 'metadata' && <><MetadataTab document={document} metadata={state.metadata} busy={state.busy} t={t} save={save} saveAgent={saveAgent} fetchLyrics={fetchLyrics} state={state} parentCurrent={parentCurrent}/><SectionTitle>{t('next.ownerLibraries.tab.attention')}</SectionTitle><AttentionTab state={state} t={t} more={moreUnmatched}/></>}
        {tab === 'scanning' && <ScanningTab api={api} library={library} document={document} metadata={state.metadata} state={state} t={t} locale={runtime.text.locale ?? defaultI18n.region.formatLocale} skipped={skipped} cancel={cancel} dashboard={dashboard} save={save} scanAll={scanAll} parentCurrent={parentCurrent} strmChange={strmChange} strmRetry={strmRetry} sonicToggle={sonicToggle} sonicRetry={sonicRetry} sonicReload={sonicReload}/>}
        {tab === 'advanced' && <AdvancedTab document={document} t={t} owner={viewerRole === 'owner'} busy={state.busy} save={save}/>}
      </View>
    </ScrollView>
  </View>;
}

function GeneralTab({library, document, t, renameLibrary, removeLibrary, busy, save}: {library: ManagedLibrary; document?: LibraryAdminDocument; t: Translate; renameLibrary: () => void; removeLibrary: () => void; busy: boolean; save: (patch: Partial<LibrarySettings>) => void}) {
  return <>
    <LibraryGroup>
      <LibraryRow first label={t('next.ownerLibraries.nameLabel')} value={library.name} onPress={renameLibrary}/>
      <LibraryRow label={t('web.libraries.type')} value={t(ownerLibraryKind(library.kind).label)}/>
      <LibraryRow label={t('next.ownerLibraries.whoCanSee')} help={ownerLibraryAudience(library, t)}/>
    </LibraryGroup>
    {document && <LibrarySettingsRows tab="general" document={document} busy={busy} t={t} save={save}/>}
    {document && <ContinueWatchingGroup document={document} busy={busy} t={t} save={save}/>}
    <SectionTitle>{t('server.library.remove')}</SectionTitle>
    <LibraryGroup>
      <LibraryRow first icon="i-trash" tone="danger" minHeight={64} label={t('server.library.removeRow')} help={t('server.library.removeHelp')} onPress={removeLibrary}/>
    </LibraryGroup>
  </>;
}

function FoldersTab({state, retry, t, addFolder, folderMenu}: {state: NativeOwnerLibrariesSnapshot; retry: () => void; t: Translate; addFolder: () => void; folderMenu: (source: InventorySource) => void}) {
  const theme = useTheme(), source = state.inventoryError === undefined ? state.inventory?.sources : undefined;
  return <>
    <SectionTitle action={<Focusable accessibilityRole="button" accessibilityLabel={t('web.libraries.addFolder')} disabled={!state.inventory || state.inventoryError !== undefined} onPress={state.inventory && state.inventoryError === undefined ? addFolder : undefined} focusKind="chip" focusRadius={8} hitSlop={10}><Text style={[shellType(13.5, 700), {color: theme.colors.accentStrong}]}>{t('web.libraries.addFolder')}</Text></Focusable>}>{t('next.ownerLibraries.folders')}</SectionTitle>
    {state.inventoryError !== undefined && <><Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.record}]}>{presentError(state.inventoryError, 'library').body}</Text><Button variant="glass" onPress={retry}>{t('action.tryAgain')}</Button></>}
    {state.inventoryLoading && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('web.libraries.loadingOne')}</Text>}
    {source && <LibraryGroup>
      {!source.length && <LibraryRow first label={t('web.libraries.noFolders')}/>}
      {source.map((folder, index) => <LibraryRow key={folder.id} first={index === 0} icon="i-folder" label={folder.name || folder.path} help={`${folder.path} · ${ownerFolderLocation(folder, t)}`} value={ownerFolderState(folder, t)} onPress={() => folderMenu(folder)}/>)}
    </LibraryGroup>}
  </>;
}

/** Root §35: keep the existing five controls and server eligibility, in General. */
function ContinueWatchingGroup({document, busy, t, save}: {document: LibraryAdminDocument; busy: boolean; t: Translate; save: (patch: Partial<LibrarySettings>) => void}) {
  const value = document.settings.continueWatching, eligible = (id: string) => document.eligibleControls.includes(id);
  const update = (patch: Partial<LibrarySettings['continueWatching']>) => {if (!busy) save({continueWatching: {...value, ...patch}});};
  const weekLabel = (weeks: number) => weeks === 26 ? t('next.ownerLibraries.continue.sixMonths') : weeks === 52 ? t('next.ownerLibraries.continue.oneYear')
    : weeks === 104 ? t('next.ownerLibraries.continue.twoYears') : t('next.ownerLibraries.continue.weekCount', {count: weeks});
  const percentLabel = (percent: number) => percent === 100 ? t('next.ownerLibraries.continue.finished') : t('next.ownerLibraries.continue.percent', {percent});
  // A server may hold a valid custom value. Keep it visible until the owner chooses
  // an authored option; opening this page never coerces or writes a preference.
  const numericOptions = (values: readonly number[], current: number, label: (n: number) => string) =>
    [...values, ...(values.includes(current) ? [] : [current])].map(n => ({value: String(n), label: label(n), disabled: !values.includes(n)}));
  const ids = ['libraries.cwWeeks', 'libraries.cwMax', 'libraries.cwPremieres', 'libraries.cwWatched', 'libraries.cwRule'];
  if (!ids.some(eligible)) return null;
  const selectStyle = {backgroundColor: 'transparent', paddingHorizontal: 0};
  return <>
    <SectionTitle>{t('next.ownerLibraries.continueWatching')}</SectionTitle>
    <LibraryGroup>
      {eligible('libraries.cwWeeks') && <LibraryRow first label={t('next.ownerLibraries.continue.weeks')} trailing={<Select disabled={busy} accessibilityLabel={t('next.ownerLibraries.continue.weeks')} value={String(value.weeks)}
        options={numericOptions([4, 8, 16, 26, 52, 104], value.weeks, weekLabel)} onValueChange={next => update({weeks: Number(next)})} style={selectStyle}/>}/>}
      {eligible('libraries.cwMax') && <LibraryRow first={!eligible('libraries.cwWeeks')} label={t('next.ownerLibraries.continue.maximum')} trailing={<Select disabled={busy} accessibilityLabel={t('next.ownerLibraries.continue.maximum')} value={String(value.maximumItems)}
        options={numericOptions([20, 40, 100, 200], value.maximumItems, n => defaultI18n.number(n))} onValueChange={next => update({maximumItems: Number(next)})} style={selectStyle}/>}/>}
      {eligible('libraries.cwPremieres') && <LibraryRow label={t('next.ownerLibraries.continue.premieres')} help={t('next.ownerLibraries.continue.premieresHelp')}
        trailing={<Switch disabled={busy} checked={value.includeSeasonPremieres} accessibilityLabel={t('next.ownerLibraries.continue.premieres')} onCheckedChange={next => update({includeSeasonPremieres: next})}/>}/>}
      {eligible('libraries.cwWatched') && <LibraryRow label={t('next.ownerLibraries.continue.watchedAt')} help={t('next.ownerLibraries.continue.watchedAtHelp')} trailing={<Select disabled={busy} accessibilityLabel={t('next.ownerLibraries.continue.watchedAt')} value={String(value.videoPlayedThreshold)}
        options={numericOptions([80, 85, 90, 95, 100], value.videoPlayedThreshold, percentLabel)} onValueChange={next => update({videoPlayedThreshold: Number(next)})} style={selectStyle}/>}/>}
      {eligible('libraries.cwRule') && <LibraryRow label={t('next.ownerLibraries.continue.rule')} help={t('next.ownerLibraries.continue.ruleHelp')} trailing={<Select disabled={busy} accessibilityLabel={t('next.ownerLibraries.continue.rule')} value={value.videoCompletion}
        options={[{value: 'earliest', label: t('next.ownerLibraries.continue.earliest')}, {value: 'threshold', label: t('next.ownerLibraries.continue.threshold')}, {value: 'credits', label: t('next.ownerLibraries.continue.credits'), description: t('next.ownerLibraries.continue.creditsHelp')}]}
        onValueChange={next => {if (next === 'earliest' || next === 'threshold' || next === 'credits') update({videoCompletion: next});}} style={selectStyle}/>}/>}
    </LibraryGroup>
  </>;
}

function MetadataTab({document, metadata, busy, t, save, saveAgent, fetchLyrics, state, parentCurrent}: {document?: LibraryAdminDocument; metadata?: LibraryMetadataAgent; busy: boolean; t: Translate; save: (patch: Partial<LibrarySettings>) => void; saveAgent: (id: string) => void; fetchLyrics: (document: LibraryAdminDocument, current: () => boolean) => Promise<void>; state: NativeOwnerLibrariesSnapshot; parentCurrent: () => boolean}) {
  const theme = useTheme();
  if (!document) return null;
  return <>
    {metadata && metadata.agents.length > 0 && <>
      <SectionTitle>{t('next.ownerLibraries.metadataSource')}</SectionTitle>
      <LibraryGroup>{metadata.agents.map((option, index) => <MetadataAgentChoice key={option.id} option={option} first={index === 0} checked={option.id === metadata.agent} onPress={() => {if (!busy && option.id !== metadata.agent) saveAgent(option.id);}}/>)}</LibraryGroup>
      <Text style={[shellType(12.5, 500, 17.5), {color: theme.colors.textTertiary, marginTop: 8, paddingHorizontal: 4}]}>{t('next.ownerLibraries.metadataHelp')}</Text>
    </>}
    <LibrarySettingsRows tab="metadata" document={document} busy={busy || !!state.lyricsBusy} t={t} save={save} fetchLyrics={current => fetchLyrics(document, () => parentCurrent() && current())}/>
    {state.lyricsError !== undefined && <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.record}]}>{presentError(state.lyricsError, 'library').body}</Text>}
    {state.lyricsJob && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{jobStateName(t, id => defaultI18n.has(id), state.lyricsJob.state)}</Text>}
    <LibraryGroup style={{marginTop: 18}}><LibraryRow first label={t('server.library.ownKeys')} help={t('server.library.ownKeysHelp')}/></LibraryGroup>
  </>;
}

function AttentionTab({state, t, more}: {state: NativeOwnerLibrariesSnapshot; t: Translate; more: () => void}) {
  const theme = useTheme();
  return <>
    <Text style={[shellType(13, 400, 18.5), {color: theme.colors.textSecondary, marginBottom: 12}]}>{t('next.ownerLibraries.unmatchedLede')}</Text>
    <LibraryGroup>
      {!state.unmatched.length && <LibraryRow first label={t(state.unmatchedLoading ? 'web.libraries.loadingOne' : 'next.ownerLibraries.unmatchedEmpty')}/>}
      {state.unmatched.map((issue, index) => <LibraryRow key={issue.assetId} first={index === 0} icon="i-note" label={issue.sourceName} help={issue.code}/>)}
      {state.unmatchedCursor ? <LibraryRow label={t('next.ownerLibraries.showMore')} onPress={more}/> : null}
    </LibraryGroup>
  </>;
}

function ScanningTab({api, library, document, metadata, state, t, locale, skipped, cancel, dashboard, save, scanAll, parentCurrent, strmChange, strmRetry, sonicToggle, sonicRetry, sonicReload}: {api: LibraryContentApi; library: ManagedLibrary; document?: LibraryAdminDocument; metadata?: LibraryMetadataAgent; state: NativeOwnerLibrariesSnapshot; t: Translate; locale: string; skipped: () => void; cancel: () => void; dashboard: () => void; save: (patch: Partial<LibrarySettings>) => void; scanAll: () => void; parentCurrent: () => boolean;
  strmChange: (enabled: boolean, document: LibraryAdminDocument, policy: AnalysisPolicy, lease: SonicPageLease) => void;
  strmRetry: (document: LibraryAdminDocument) => void;
  sonicToggle: (enabled: boolean, document: LibraryAdminDocument, sonic: LibrarySonicDocument, lease: SonicPageLease) => void;
  sonicRetry: (document: LibraryAdminDocument, sonic: LibrarySonicDocument, lease: SonicPageLease) => void;
  sonicReload: (document: LibraryAdminDocument, sonic: LibrarySonicDocument, lease: SonicPageLease) => void}) {
  const theme = useTheme(), active = ownerLibraryScan(library), settings = document?.settings;
  const [pageLease, setPageLease] = useState<SonicPageLease | null>(null);
  useLayoutEffect(() => {
    const controller = new AbortController();
    const lease: SonicPageLease = sonicPageLease(controller, parentCurrent);
    setPageLease(lease);
    return () => {controller.abort();};
  }, [parentCurrent]);
  if (!document || !settings) return null;
  const sonic = state.sonic?.libraryId === document.libraryId ? state.sonic : undefined;
  const toggle = sonic ? readLibrarySonicSettingsBinding('libraries.sonicAnalysis', document, sonic) : undefined;
  const sonicLine = sonic ? librarySonicStatus(sonic, t, at => defaultI18n.dateTime(new Date(at))) : undefined;
  const sonicUnavailable = !!sonic && (!sonic.available || sonic.state === 'unavailable');
  const sonicErrorText = state.sonicError !== undefined ? presentError(state.sonicError, 'library', {operation: state.sonicErrorOperation ?? 'load'}).body : undefined;
  const skippedCount = ownerSkippedFiles(state.skipped?.items ?? []).length || (library.filesSkipped ?? 0);
  return <>
    {active && <View style={{gap: 8, padding: 12, marginTop: 4, borderRadius: 12, backgroundColor: alpha(theme.colors.accent, .12)}}>
      <View style={{flexDirection: 'row', gap: 10}}>
        <Icon name="i-info" size={18} color={theme.colors.accentStrong}/>
        <Text style={[shellType(12.5, 500, 17.5), {color: theme.colors.textSecondary, flex: 1}]}>{t('next.ownerLibraries.scanningNote')}</Text>
      </View>
      <Button variant="glass" size="xs" trailingIcon="i-right" onPress={dashboard} style={{alignSelf: 'flex-start', marginLeft: 28}}>{t('next.ownerLibraries.openDashboard')}</Button>
    </View>}
    <LibraryGroup style={{marginTop: 12}}>
      <LibraryRow first label={t('next.ownerLibraries.lastScan')} value={ownerLibraryState(library, {t, locale}).text}
        trailing={active ? <Button variant="glass" size="xs" onPress={cancel}>{t('web.libraries.cancelScan')}</Button> : undefined}/>
      <LibraryRow label={t('next.ownerLibraries.filesSkippedTitle')} help={t('next.ownerLibraries.filesSkippedHelp')} value={t('next.ownerLibraries.filesCount', {count: skippedCount})} onPress={skipped}/>
    </LibraryGroup>
    <LibraryGroup style={{marginTop: 12}}>
      <LibraryRow first label={t('next.ownerLibraries.introsAndCredits')} trailing={<Select testID="owner-libraries-segments" accessibilityLabel={t('next.ownerLibraries.introsAndCredits')} value={settings.segmentDetection}
        options={[{value: 'server', label: t('next.ownerLibraries.introsFollow')}, {value: 'standard', label: t('settings.server.segmentMode.standard')}, {value: 'thorough', label: t('settings.server.segmentMode.thorough')}, {value: 'off', label: t('settings.server.segmentMode.off')}]}
        onValueChange={value => save({segmentDetection: value as LibrarySettings['segmentDetection']})} style={{backgroundColor: 'transparent', paddingHorizontal: 0}}/>}/>
      <LibraryRow label={t('next.ownerLibraries.metadataSource')} value={metadata?.agent === 'local' ? t('next.ownerLibraries.metadataLocal') : t('next.ownerLibraries.on')}/>
    </LibraryGroup>
    <LibrarySettingsRows tab="scanning" document={document} busy={state.busy} t={t} save={save}/>
    {(document.libraryKind === 'tv' || document.libraryKind === 'anime') && <EpisodeThumbnailsRow api={api} libraryId={document.libraryId} parentCurrent={parentCurrent} disabled={state.busy} t={t}/>}
    {(state.strm || state.strmError !== undefined) && <LibraryGroup style={{marginTop: 12}}>
      <LibraryRow first label={t('next.settingsSpec.server.libraries.panels.library.tabs.4.groups.0.rows.1.label')}
        help={state.strmError !== undefined ? presentError(state.strmError, 'library').body : t('next.settingsSpec.server.libraries.panels.library.tabs.4.groups.0.rows.1.help')}
        trailing={state.strm ? <Switch testID="libraries.strm" checked={state.strm.enabled} disabled={state.busy || !pageLease || state.strmError !== undefined}
          accessibilityLabel={t('next.settingsSpec.server.libraries.panels.library.tabs.4.groups.0.rows.1.label')}
          onCheckedChange={enabled => {if (pageLease && state.strm) strmChange(enabled, document, state.strm, pageLease);}}/>
          : undefined}/>
      {state.strmError !== undefined && <LibraryRow label={t('action.tryAgain')} onPress={state.busy ? undefined : () => strmRetry(document)}/>}
    </LibraryGroup>}
    {(toggle || sonicLine || sonicErrorText || (state.sonicNeedsReload && sonic)) && <LibraryGroup style={{marginTop: 12}}>
      {toggle && <LibraryRow first label={t('next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.5.label')} help={t('next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.5.help')}
        trailing={<Switch testID="owner-libraries-sonic" checked={toggle.current === true} disabled={state.sonicBusy || state.sonicNeedsReload} accessibilityLabel={t('next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.5.label')} onCheckedChange={enabled => {if (pageLease) sonicToggle(enabled, document, sonic!, pageLease);}}/>}/>}
      {sonicLine ? <LibraryRow first={!toggle} label={t('next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.5.children.0.label')}
        help={state.sonicRetryUnavailable && sonicUnavailable ? t('next.settings.librarySonic.retryUnavailable') : sonicErrorText}
        value={<Text style={[shellType(14.5), {color: sonicLine.tone === 'warn' ? theme.colors.amber : sonicLine.tone === 'ok' ? theme.colors.healthy : theme.colors.textSecondary}]}>{sonicLine.text}</Text>}
        trailing={(sonicUnavailable || state.sonicNeedsReload) && !state.sonicBusy ? <Button variant="glass" size="xs" onPress={() => {if (!pageLease) return; if (state.sonicNeedsReload) sonicReload(document, sonic!, pageLease); else sonicRetry(document, sonic!, pageLease);}}>{t('action.tryAgain')}</Button> : undefined}/>
                : sonicErrorText ? <LibraryRow first label={t('next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.5.children.0.label')} value={<Text accessibilityRole="alert" style={[shellType(13, 600, 18), {color: theme.colors.record}]}>{sonicErrorText}</Text>}
          trailing={state.sonicNeedsReload && sonic && !state.sonicBusy ? <Button variant="glass" size="xs" onPress={() => {if (pageLease) sonicReload(document, sonic!, pageLease);}}>{t('action.tryAgain')}</Button> : undefined}/>
          : state.sonicNeedsReload && sonic ? <LibraryRow first={!toggle} label={t('next.settingsSpec.server.libraries.panels.library.tabs.3.groups.0.rows.5.children.0.label')} trailing={!state.sonicBusy ? <Button variant="glass" size="xs" onPress={() => {if (pageLease) sonicReload(document, sonic!, pageLease);}}>{t('action.tryAgain')}</Button> : undefined}/> : null}
    </LibraryGroup>}
    <LibraryGroup style={{marginTop: 12}}>
      {document.sources.map((folder, index) => <LibraryRow key={folder.id} first={index === 0} label={t('next.ownerLibraries.rescan', {folder: folder.name || folder.path})} value={ownerLibraryInterval(folder.intervalSeconds, t)}/>)}
    </LibraryGroup>
    <LibraryGroup style={{marginTop: 12}}>
      <LibraryRow first icon="i-refresh" label={t('next.ownerLibraries.scanAll')} help={t('next.ownerLibraries.scanAllHelp')} onPress={scanAll}/>
    </LibraryGroup>
  </>;
}

function AdvancedTab({document, t, owner, busy, save}: {document?: LibraryAdminDocument; t: Translate; owner: boolean; busy: boolean; save: (patch: Partial<LibrarySettings>) => void}) {
  if (!document || !owner || !document.eligibleControls.includes('libraries.allowDelete')) return null;
  return <LibraryGroup style={{marginTop: 4}}>
    <LibraryRow first label={t('next.ownerLibraries.allowDeletion')} help={t('next.ownerLibraries.allowDeletionHelp')}
      trailing={<Switch testID="owner-libraries-deletion" disabled={busy} checked={document.settings.allowMediaDeletion} accessibilityLabel={t('next.ownerLibraries.allowDeletion')} onCheckedChange={next => save({allowMediaDeletion: next})}/>}/>
  </LibraryGroup>;
}

function SkippedSheet({open, onOpenChange, state, t, scan, more}: {open: boolean; onOpenChange: (open: boolean) => void; state: NativeOwnerLibrariesSnapshot; t: Translate; scan: () => void; more: () => void}) {
  const theme = useTheme(), items = ownerSkippedFiles(state.skipped?.items ?? []);
  return <BottomSheet open={open} onOpenChange={next => onOpenChange(next)} title={t('next.ownerLibraries.filesSkippedTitle')} description={t('next.ownerLibraries.filesSkippedLede')} scrollable
    footer={<View style={{flexDirection: 'row', gap: 10}}><Button size="small" icon="i-refresh" onPress={scan} style={{flex: 1}}>{t('next.ownerLibraries.scanAgain')}</Button><Button variant="glass" size="small" onPress={() => onOpenChange(false)}>{t('action.close')}</Button></View>}>
    <View style={{paddingBottom: 8}}>
      {state.skippedLoading && <Text style={[shellType(13, 500), {color: theme.colors.textTertiary}]}>{t('next.ownerLibraries.loading')}</Text>}
      {!state.skippedLoading && !items.length && <Text style={[shellType(13, 500), {color: theme.colors.textTertiary}]}>{t('next.ownerLibraries.skippedEmpty')}</Text>}
      {items.map(item => <View key={item.sequence} style={{paddingVertical: 10, borderTopWidth: item.sequence ? 1 : 0, borderTopColor: theme.colors.lineSoft}}>
        <Text style={[shellType(12, 500, 16), {color: theme.colors.textSecondary, fontFamily: 'monospace'}]}>{item.path}</Text>
        <Text style={[shellType(12.5, 500, 17.5), {color: theme.colors.textTertiary, marginTop: 3}]}>{ownerIssueReason(item, t)}</Text>
      </View>)}
      {state.skipped?.hasMore ? <Button variant="glass" size="small" onPress={more} style={{marginTop: 10}}>{t('next.ownerLibraries.showMore')}</Button> : null}
    </View>
  </BottomSheet>;
}

function RemoveLibrarySheet({open, onOpenChange, library, busy, error, t, remove}: {open: boolean; onOpenChange: (open: boolean) => void; library: ManagedLibrary; busy: boolean; error?: unknown; t: Translate; remove: (confirmation: string) => void}) {
  const theme = useTheme(), [typed, setTyped] = useState('');
  useEffect(() => {if (open) setTyped('');}, [open]);
  const confirmed = typed === library.name;
  return <BottomSheet open={open} onOpenChange={next => onOpenChange(next)} title={t('server.library.removeTitle', {name: library.name})} scrollable
    footer={<View style={{flexDirection: 'row', gap: 10}}><Button variant="glass" size="small" onPress={() => onOpenChange(false)}>{t('action.cancel')}</Button>
      <Button variant="glass" size="small" loading={busy} disabled={!confirmed || busy} textStyle={{color: theme.colors.record}} onPress={() => remove(typed)} style={{flex: 1}}>{t('server.library.remove')}</Button></View>}>
    <View style={{gap: 10, paddingBottom: 4}}>
      <Text style={[shellType(13.5, 400, 19), {color: theme.colors.textSecondary}]}>{t('server.library.removeGoes')}</Text>
      <Text style={[shellType(13.5, 400, 19), {color: theme.colors.textSecondary}]}>{t('server.library.removeKept')}</Text>
      <TextField focusId="owner-libraries-remove-name" value={typed} onChangeText={setTyped} autoCapitalize="none" autoCorrect={false} accessibilityLabel={t('next.ownerLibraries.typeToConfirm')} placeholder={t('next.ownerLibraries.typeToConfirm')} showSoftInputOnFocus/>
      {error !== undefined && <Text accessibilityRole="alert" style={[shellType(13, 600, 18), {color: theme.colors.record}]}>{error instanceof Error ? error.message : t('next.ownerLibraries.saveFailed')}</Text>}
    </View>
  </BottomSheet>;
}

function RemoveFolderSheet({open, onOpenChange, source, busy, error, t, remove}: {open: boolean; onOpenChange: (open: boolean) => void; source?: InventorySource; busy: boolean; error?: unknown; t: Translate; remove: () => void}) {
  const theme = useTheme(), [typed, setTyped] = useState('');
  useEffect(() => {if (open) setTyped('');}, [open]);
  const name = source?.name || source?.path || '';
  const confirmed = Boolean(name) && typed === name;
  return <BottomSheet open={open} onOpenChange={next => onOpenChange(next)} title={t('web.libraries.removeFolderTitle', {name})} description={t('next.ownerLibraries.removeFolderBody')}
    footer={<View style={{flexDirection: 'row', gap: 10}}><Button variant="glass" size="small" onPress={() => onOpenChange(false)}>{t('action.cancel')}</Button>
      <Button variant="glass" size="small" loading={busy} disabled={!confirmed || busy} textStyle={{color: theme.colors.record}} onPress={remove} style={{flex: 1}}>{t('web.libraries.removeFolder')}</Button></View>}>
    <View style={{gap: 10, paddingBottom: 4}}>
      <TextField focusId="owner-libraries-remove-folder" value={typed} onChangeText={setTyped} autoCapitalize="none" autoCorrect={false} accessibilityLabel={t('next.ownerLibraries.typeToConfirm')} placeholder={t('next.ownerLibraries.typeToConfirm')} showSoftInputOnFocus/>
      {error !== undefined && <Text accessibilityRole="alert" style={[shellType(13, 600, 18), {color: theme.colors.record}]}>{error instanceof Error ? error.message : t('next.ownerLibraries.saveFailed')}</Text>}
    </View>
  </BottomSheet>;
}

function RenameSheet({open, onOpenChange, library, busy, error, t, save}: {open: boolean; onOpenChange: (open: boolean) => void; library: ManagedLibrary; busy: boolean; error?: unknown; t: Translate; save: (name: string) => void}) {
  const theme = useTheme(), [name, setName] = useState(library.name);
  useEffect(() => {if (open) setName(library.name);}, [open, library.name]);
  const valid = name.trim().length > 0 && name.trim() !== library.name;
  return <BottomSheet open={open} onOpenChange={next => onOpenChange(next)} title={t('next.ownerLibraries.renameTitle')}
    footer={<View style={{flexDirection: 'row', gap: 10}}><Button variant="glass" size="small" onPress={() => onOpenChange(false)}>{t('action.cancel')}</Button>
      <Button size="small" loading={busy} disabled={!valid || busy} onPress={() => save(name.trim())} style={{flex: 1}}>{t('next.ownerLibraries.save')}</Button></View>}>
    <View style={{gap: 10, paddingBottom: 4}}>
      <TextField focusId="owner-libraries-rename" value={name} onChangeText={setName} maxLength={100} accessibilityLabel={t('next.ownerLibraries.nameLabel')} showSoftInputOnFocus/>
      {error !== undefined && <Text accessibilityRole="alert" style={[shellType(13, 600, 18), {color: theme.colors.record}]}>{error instanceof Error ? error.message : t('next.ownerLibraries.saveFailed')}</Text>}
    </View>
  </BottomSheet>;
}
