import {useCallback, useEffect, useLayoutEffect, useMemo, useState, useSyncExternalStore} from 'react';
import type {LibraryContentApi} from '../../../../../../packages/client-core/src/library-content';
import type {ManagedLibrary} from '../../../../../../packages/client-core/src/library-management';
import type {InventorySource} from '../../../../../../packages/client-core/src/library-inventory';
import type {LibraryAdminDocument} from '../../../../../../packages/client-core/src/administration';
import {librarySonicStatus} from '../../../../../../packages/client-core/src/server-admin/library-sonic-words';
import {readLibrarySonicSettingsBinding} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n} from '../../../../../../packages/i18n/src/index';
import type {MenuEntry} from '../../../kit/overlays/types';
import {registerBack} from '../../../kit/navigation/back';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {viewerQueriesFor} from '../../../shell/freshness';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {screenScopeEnded, type NativeScreenLifetime} from '../../../screens/settings-live/lifetime';
import {loadInitialOwnerLibrary} from '../../../screens/settings-live/owner-libraries/initial-library';
import {NativeOwnerLibrariesController, sonicPageLease, type NativeOwnerLibrariesSnapshot, type SonicPageLease} from '../../../screens/settings-live/owner-libraries/controller';
import {libraryListState} from '../../../screens/settings-live/owner-libraries/library-settings-model';
import {ownerLibraryCounts, ownerLibraryScan, ownerLibrarySquare, ownerLibraryState} from '../../../screens/settings-live/owner-libraries/presentation';

type Translate = typeof defaultI18n.t;
export type LibraryPageTab = 'general' | 'folders' | 'metadata' | 'scanning' | 'advanced';

/** Settings › Server › Libraries: the owner's list and one library's page. The transport, controller, the page's
 * open sheets and the Back ladder are the old panel's, moved as they were. */
export function useServerLibraries({ports, runtime, initialLibraryId, lifetime}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; initialLibraryId?: string; lifetime?: NativeScreenLifetime}) {
  const t = runtime.text.t as Translate, context = ports.context, authority = context.authority();
  const transport = useMemo(() => {
    const originalApi = context.api;
    let active = true;
    const requireCurrent = () => {if (!active || !context.current() || context.authority() !== authority) throw screenScopeEnded(); lifetime?.assert();};
    const api: LibraryContentApi = {
      request: async <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
        requireCurrent();
        const value = await (lifetime ? lifetime.request(next => originalApi.request<T>(path, method, body, next), signal) : originalApi.request<T>(path, method, body, signal));
        requireCurrent(); return value;
      },
      requestRaw: async (path, method, options) => {
        requireCurrent();
        const send = (signal?: AbortSignal) => originalApi.requestRaw(path, method, {...options, signal});
        const value = await (lifetime ? lifetime.request(send, options?.signal) : send(options?.signal));
        requireCurrent(); return value;
      },
    };
    return {api, retire: () => {active = false;}};
  }, [context, lifetime, authority]);
  const api = transport.api;
  useLayoutEffect(() => transport.retire, [transport]);
  const scope = useMemo(() => ({serverId: authority.serverId, viewerId: JSON.stringify([authority.epoch, authority.accountId, authority.profileId])}), [authority]);
  const controller = useMemo(() => new NativeOwnerLibrariesController(api, scope, ports.requestId, viewerQueriesFor(context.api, {serverId: authority.serverId})), [api, scope, ports, context, authority]);
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const owner = context.account.account.role === 'owner';
  const parentCurrent = useCallback(() => context.current() && context.authority() === authority, [context, authority]);
  const initialOwnerLibrary = context.current() && context.account.canManage && owner && !context.account.account.disabled && context.account.account.primaryProfileId === authority.profileId ? initialLibraryId : undefined;
  const [openId, setOpenId] = useState<string | undefined>(initialOwnerLibrary), [tab, setTab] = useState<LibraryPageTab>('general');
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
    const subscription = registerBack(() => back(), {scope: 'surface'});
    return () => subscription();
  }, [back]);

  const listPhase = libraryListState(state.directory.phase, libraries.length);
  const rows = libraries.map((value: ManagedLibrary) => {
    const status = ownerLibraryState(value, {t, locale: runtime.text.locale, short: true}), square = ownerLibrarySquare(value.kind);
    return {key: value.id, name: value.name, counts: ownerLibraryCounts(value, t).filter(Boolean), status: status.text, live: status.live, square,
      covers: (value.summary?.coverUrls ?? []).map(path => runtime.artwork(path)).filter((cover): cover is NonNullable<ReturnType<NativeShellRuntime['artwork']>> => Boolean(cover)),
      open: () => open(value.id), menu: () => {setMenuOnPage(false); setLibraryMenu(value);}};
  });
  const menuItems: MenuEntry[] = libraryMenu ? menuOnPage ? [
    {id: 'rename', label: t('next.ownerLibraries.rename'), icon: 'i-pencil', onSelect: () => setRename(true)},
    {id: 'scanAll', label: t('next.ownerLibraries.scanAll'), icon: 'i-refresh', description: t('next.ownerLibraries.scanAllHelp'), disabled: !libraryMenu.actions.includes('scan'), onSelect: () => {void controller.scanAll();}},
  ] : [
    {id: 'edit', label: t('next.ownerLibraries.rename'), icon: 'i-sliders', onSelect: () => open(libraryMenu.id)},
    {id: 'scan', label: t('web.libraries.scanNow'), icon: 'i-refresh', disabled: !libraryMenu.actions.includes('scan'), onSelect: () => {void controller.scanLibrary(libraryMenu.id);}},
    {id: 'attention', label: t('next.ownerLibraries.tab.attention'), icon: 'i-link', onSelect: () => {open(libraryMenu.id); setTab('metadata');}},
    {id: 'more-separator', separator: true},
    {id: 'moveUp', label: t('server.libraries.moveUp'), icon: 'i-up', onSelect: () => {void controller.move(libraryMenu.id, -1).then(() => setLibraryMenu(undefined));}},
    {id: 'moveDown', label: t('server.libraries.moveDown'), icon: 'i-down', onSelect: () => {void controller.move(libraryMenu.id, 1).then(() => setLibraryMenu(undefined));}},
  ] : [];
  const folderMenuItems: MenuEntry[] = folderMenu ? [
    {id: 'scan', label: t('web.libraries.scanFolder'), icon: 'i-refresh', onSelect: () => {void controller.scanFolder(folderMenu.id);}},
    {id: 'check', label: t('web.libraries.checkAvailability'), icon: 'i-disk', onSelect: () => {void controller.checkFolder(folderMenu.id);}},
    {id: 'edit', label: t('web.libraries.editFolder'), icon: 'i-pencil', onSelect: () => setFolderForm({source: folderMenu})},
    {id: 'remove', label: t('web.libraries.removeFolder'), icon: 'i-trash', destructive: true, onSelect: () => setRemovingFolder(folderMenu)},
  ] : [];
  const status = library ? ownerLibraryState(library, {t, locale: runtime.text.locale}) : undefined;
  return {t, api, controller, state, owner, parentCurrent, libraries, rows, listPhase, library, document, openId, tab, setTab, open,
    header: library ? {name: library.name, counts: ownerLibraryCounts(library, t), scanning: Boolean(ownerLibraryScan(library)), status: status!.text, canScan: library.actions.includes('scan')} : undefined,
    adding, setAdding, folderForm, setFolderForm, rename, setRename, leaving, setLeaving, removingFolder, setRemovingFolder,
    folderMenu, setFolderMenu, folderMenuItems, libraryMenu, setLibraryMenu, menuItems, openPageMenu: () => {if (library) {setMenuOnPage(true); setLibraryMenu(library);}},
    skipped, setSkipped, closeLibrary: () => setOpenId(undefined),
    dashboard: () => {context.navigation.open({kind: 'server-dashboard'}, authority);},
    create: (input: Parameters<NativeOwnerLibrariesController['create']>[0]) => {void controller.create(input).then(id => {if (!id) return; setAdding(false); open(id);});},
    saveFolder: (input: Parameters<NativeOwnerLibrariesController['saveFolder']>[1]) => {void controller.saveFolder(folderForm?.source?.id, input).then(ok => {if (ok) setFolderForm(undefined);});},
    removeLibrary: (confirmation: string) => {void controller.remove(confirmation).then(ok => {if (ok) {setLeaving(false); setOpenId(undefined);}});},
    renameLibrary: (name: string) => {void controller.rename(name).then(ok => {if (ok) setRename(false);});},
    removeFolder: () => {const source = removingFolder; if (source) void controller.removeFolder(source.id).then(ok => {if (ok) setRemovingFolder(undefined);});},
    errorText: (error: unknown, operation?: 'save') => presentError(error, 'library', operation ? {operation} : undefined).body};
}

/** The Scanning tab's sonic analysis: a page lease for its commands, the switch's value, and the status line in words. */
export function useLibrarySonic(state: NativeOwnerLibrariesSnapshot, document: LibraryAdminDocument | undefined, parentCurrent: () => boolean, t: Translate) {
  const [pageLease, setPageLease] = useState<SonicPageLease | null>(null);
  useLayoutEffect(() => {
    const controller = new AbortController();
    const lease: SonicPageLease = sonicPageLease(controller, parentCurrent);
    setPageLease(lease);
    return () => {controller.abort();};
  }, [parentCurrent]);
  const sonic = document && state.sonic?.libraryId === document.libraryId ? state.sonic : undefined;
  const toggle = sonic && document ? readLibrarySonicSettingsBinding('libraries.sonicAnalysis', document, sonic) : undefined;
  const line = sonic ? librarySonicStatus(sonic, t, at => defaultI18n.dateTime(new Date(at))) : undefined;
  const unavailable = !!sonic && (!sonic.available || sonic.state === 'unavailable');
  const errorText = state.sonicError !== undefined ? presentError(state.sonicError, 'library', {operation: state.sonicErrorOperation ?? 'load'}).body : undefined;
  return {pageLease, sonic, toggle, line, unavailable, errorText};
}
