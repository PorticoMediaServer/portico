import {useCallback, useEffect, useLayoutEffect, useMemo, useState, useSyncExternalStore} from 'react';
import type {LibraryContentApi} from '../../../../../../packages/client-core/src/library-content';
import type {InventorySource} from '../../../../../../packages/client-core/src/library-inventory';
import type {ManagedLibrary} from '../../../../../../packages/client-core/src/library-management';
import {registerBack} from '../../../kit/navigation/back';
import {NativeOwnerLibrariesController} from '../../../screens/settings-live/owner-libraries/controller';
import {loadInitialOwnerLibrary} from '../../../screens/settings-live/owner-libraries/initial-library';
import {screenScopeEnded, type NativeScreenLifetime} from '../../../screens/settings-live/lifetime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {viewerQueriesFor} from '../../../shell/freshness';
import type {NativeShellRuntime} from '../../../shell/runtime';

type ViewerRole = 'owner' | 'administrator' | 'member';

/** Settings › Server › Libraries: the owner's list and one library's page, on the phone. */
export function useOwnerLibraries({ports, runtime, initialLibraryId, lifetime}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; initialLibraryId?: string; lifetime?: NativeScreenLifetime}) {
  const t = runtime.text.t, context = ports.context, authority = context.authority();
  const transport = useMemo(() => {
    const originalApi = context.api;
    let active = true;
    const requireCurrent = () => {if (!active || !context.current() || context.authority() !== authority) throw screenScopeEnded();lifetime?.assert();};
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
    const subscription = registerBack(() => back(), {scope: 'surface'});
    return () => subscription();
  }, [back]);

  return {t, context, authority, api, controller, state, viewerRole, parentCurrent, back, libraries, library, document,
    open, openId, setOpenId, tab, setTab, adding, setAdding, folderForm, setFolderForm, rename, setRename, leaving, setLeaving,
    removingFolder, setRemovingFolder, folderMenu, setFolderMenu, libraryMenu, setLibraryMenu, menuOnPage, setMenuOnPage, skipped, setSkipped};
}
