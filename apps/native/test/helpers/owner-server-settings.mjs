import {hooks, screenModule} from './screen-harness.mjs';
import {DraftForm, FormSet} from '../../../../packages/client-core/src/server-admin/draft-form.ts';
import * as specification from '../../../../packages/client-core/src/presentation/settings-spec.ts';

export {specification};
export const t = key => key;
export const i18n = {t, locale: 'en-US', number: String, bytes: value => `${value} B`};
export const theme = {colors: new Proxy({}, {get: (_, name) => String(name)}), type: {rowHeading: {}, caption: {}, title: {}}};
export const nodes = value => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...Object.values(value.props).flatMap(child => typeof child === 'object' ? nodes(child) : [])] : [];
export const content = value => Array.isArray(value) ? value.map(content).join(' ') : value?.props ? content(value.props.children) : typeof value === 'string' || typeof value === 'number' ? String(value) : '';
/** Use the repository's real codecs. The offline runner supplies only unavailable, unrelated imports. */
export function bindingModule() {return import('../../../../packages/client-core/src/presentation/settings-bindings.ts');}
/** Executes committed effects explicitly; rendering and native widgets remain shallow. */
export function lifecycleHooks() {
  const host = hooks(), pending = [], cleanups = new Set(), ref = host.react.useRef;
  host.react.useCallback = host.react.default.useCallback = (fn, deps) => host.react.useMemo(() => fn, deps);
  const effect = (run, dependencies) => {
    const slot = ref();
    if (!slot.current || !dependencies || dependencies.some((value, index) => value !== slot.current.dependencies?.[index])) {
      const previous = slot.current; slot.current = {dependencies};
      pending.push(() => {if (previous?.cleanup) {previous.cleanup();cleanups.delete(previous.cleanup);}const cleanup = run();slot.current.cleanup = cleanup;if (cleanup) cleanups.add(cleanup);});
    }
  };
  host.react.useEffect = host.react.default.useEffect = effect;
  host.react.useLayoutEffect = host.react.default.useLayoutEffect = effect;
  return {...host, flush: () => {for (const run of pending.splice(0)) run();}, dispose: () => {for (const cleanup of cleanups) cleanup();cleanups.clear();}};
}
export async function formsFixture(values, statuses = {}) {
  let active = true;
  const context = {serverName: 'Test server', current: () => active, authority: () => ({serverId: 'server', accountId: 'owner', profileId: 'main'}), account: {serverId: 'server', account: {id: 'owner', role: 'owner', primaryProfileId: 'main'}, canManage: true, capabilities: {manageServer: true}}};
  const set = new FormSet();
  /** @type {Record<string, DraftForm<any, any>>} */
  const forms = {};
  for (const [id, saved] of Object.entries(values)) {
    const form = new DraftForm({load: async () => ({value: structuredClone(saved), status: statuses[id]}), save: async draft => ({value: draft, status: statuses[id]})});
    await form.load();forms[id] = form;set.add(id, form);
  }
  const group = {context, forms, set, mount: 1};
  const formHooks = {useStorageForms: () => group, useStorageSet: () => set.getSnapshot(), useFormState: form => form.getSnapshot()};
  return {group, hooks: formHooks, stop: () => {active = false;for (const form of Object.values(forms)) form.dispose();}};
}
export const maintenance = () => ({windows: [
  {id: 'backup', name: 'Backups', enabled: true, cadence: 'nightly', days: ['monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday', 'sunday'], startMinute: 180, durationMinutes: 240, timezone: '', tasks: ['backup']},
  {id: 'heavy', name: 'Analysis', enabled: true, cadence: 'nightly', days: ['monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday', 'sunday'], startMinute: 60, durationMinutes: 300, timezone: '', tasks: ['analysis', 'trickplay']},
], retention: {conversions: 7, prepared: 90, downloads: 30, subtitles: 365, trickplay: 365}, backupKeepCount: 7, backgroundSpeed: 'normal'});

/** Storage & backups: its shared hooks loaded with these namespaces (any other import an empty one), and the page on them. */
export async function storagePage(react, hookModules, pageModules) {
  const hookImports = ['server-admin/server-backups', 'server-admin/restore-gate', 'server-admin/console-words', 'server-admin/cache-source', 'server-admin/server-storage',
    'home-selection/list-destinations', 'presentation/settings-bindings', 'presentation/settings-spec'].map(path => `../../../../../../packages/client-core/src/${path}`);
  const empty = Object.fromEntries([...hookImports, '../../../../../../packages/i18n/src', '../../../screens/settings-live/lifetime',
    '../../../screens/settings-live/use-settings-request-scope', '../../../screens/settings-live/server/storage/space-source'].map(path => [path, {}]));
  const hook = await screenModule(new URL('../../src-next/pages/shared/settings/useServerStorage.ts', import.meta.url), {...empty, react, ...hookModules});
  const kit = Object.fromEntries(['../../../kit/controls', '../../../kit/frames', '../../../kit/overlays', '../../../kit/overlays/surface', '../../../kit/settings/AdvancedSection',
    '../../../kit/settings/SettingControl', '../../../kit/shell', '../../../kit/settings/FolderExplorer'].map(path => [path, {}]));
  const parts = Object.fromEntries(['BackupRow', 'PendingRestoreCard', 'StorageActions', 'StorageColumn', 'StorageList', 'StorageNote', 'StoragePath', 'StorageSheetBody', 'StorageUsage'].map(name => [name, name]));
  return screenModule(new URL('../../src-next/pages/touch/settings/ServerStoragePage.tsx', import.meta.url), {...kit, react, '../../../kit/settings/StorageParts': parts, ...pageModules,
    '../../shared/settings/useServerStorage': hook});
}
