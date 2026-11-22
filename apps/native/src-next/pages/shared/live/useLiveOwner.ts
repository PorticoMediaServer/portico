import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import type {View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {importSkipped, liveSourceState} from '../../../../../../packages/client-core/src/server-admin/live-words';
import type {MenuEntry} from '../../../kit/overlays/types';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeLiveOwnerController} from '../../../screens/settings-live/live-owner-controller';
import type {LiveDefaults, LiveSource} from '../../../screens/settings-live/live-owner-model';
import {liveDocumentPicker} from '../../../screens/settings-live/live-document-picker';
import {liveStabilityPatch, liveStabilityValue} from '../../../screens/settings-live/live-defaults-presentation';

export type LiveOwnerTab = 'sources' | 'recording' | 'channels';
type RemoveOpening = Readonly<{token: number; source: LiveSource; controller: NativeLiveOwnerController; revision: number}>;

/** Settings › Server › Live TV & Channels: the owner's real sources (each with what it holds, its tuners, when its guide
 * was updated and how it's doing), adding, discovering or uploading one, its menu, and one guarded Remove. Every read and
 * write is the server's; a row never shows a change it did not accept. Moved from the old screen as it was. */
export function useLiveOwner({controller, ports, runtime, initialAction, initialChannelId}: {controller: NativeLiveOwnerController; runtime: NativeShellRuntime;
  ports: Pick<NativeSettingsLivePorts, 'context'>; initialAction?: 'add' | 'discover'; initialChannelId?: string}) {
  const t = runtime.text.t;
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const [tab, setTab] = useState<LiveOwnerTab>(initialChannelId ? 'channels' : 'sources');
  const [addMenu, setAddMenu] = useState(false), [adding, setAdding] = useState(initialAction === 'add'), [discovering, setDiscovering] = useState(initialAction === 'discover'), [uploading, setUploading] = useState(false), [tuner, setTuner] = useState<string>();
  const addButton = useRef<View | null>(null);
  const [menu, setMenu] = useState<LiveSource>(), [configure, setConfigure] = useState<LiveSource>(), [mapping, setMapping] = useState<LiveSource>();
  // One remove confirmation, exactly the source it was drawn for; the flight keeps that same
  // opening so a delayed close or a second press cannot reach a successor.
  const [removeOpening, setRemoveOpening] = useState<RemoveOpening>();
  const removeRef = useRef<RemoveOpening | undefined>(undefined), removeFlight = useRef<RemoveOpening | undefined>(undefined), removeToken = useRef(0);
  const [removing, setRemoving] = useState(false);
  const menuOpener = useRef<View | null>(null), rowOpeners = useRef(new Map<string, View>()), confirmCancel = useRef<View>(null), owner = useRef(controller);
  const navigation = ports.context.navigation, mounted = useRef(true);
  useLayoutEffect(() => {mounted.current = true; return () => {mounted.current = false; removeRef.current = undefined; removeFlight.current = undefined;};}, []);
  if (owner.current !== controller) {owner.current = controller; removeRef.current = undefined; removeFlight.current = undefined;}
  const role = ports.context.account.account.role === 'owner' ? 'owner' as const : ports.context.account.account.role === 'admin' ? 'administrator' as const : 'member' as const;
  useEffect(() => {void controller.ensure();}, [controller]);
  const sources = state.list?.sources ?? [];
  const refresh = (id: string) => state.list?.refresh.find(value => value.sourceId === id);
  const openRemove = (source: LiveSource) => {
    if (removeFlight.current) return;
    const revision = navigation.getSnapshot().revision;
    const next: RemoveOpening = {token: ++removeToken.current, source, controller, revision}; removeRef.current = next; setRemoveOpening(next);
  };
  const closeRemove = (target: RemoveOpening) => {if (removeRef.current !== target) return; removeRef.current = undefined; setRemoveOpening(current => current === target ? undefined : current);};
  const liveRevision = navigation.getSnapshot().revision;
  if (removeRef.current && (removeRef.current.revision !== liveRevision || removeRef.current.controller !== controller)) {removeRef.current = undefined; removeFlight.current = undefined;}
  if (removeFlight.current && (removeFlight.current.revision !== liveRevision || removeFlight.current.controller !== controller)) removeFlight.current = undefined;
  const shownRemove = removeOpening && removeRef.current === removeOpening && removeOpening.controller === controller && removeOpening.revision === liveRevision ? removeOpening : undefined;
  const removeSource = async (target: RemoveOpening) => {
    if (!mounted.current || removeRef.current !== target || removeFlight.current) return;
    // The route and visibility are re-read at the press, not taken from the drawn render.
    const nav = navigation.getSnapshot();
    if (nav.revision !== target.revision || !navigation.isCurrent()) return;
    if (runtime.getSnapshot().contentVisible !== true || !target.controller.lifetime.current()) return;
    removeFlight.current = target; setRemoving(true);
    try {await target.controller.removeSource(target.source.id);}
    catch (failure) {if (mounted.current && target.controller.lifetime.current() && removeFlight.current === target) runtime.reportError(failure);}
    finally {
      if (removeFlight.current !== target) return;
      removeFlight.current = undefined;
      if (!mounted.current || !target.controller.lifetime.current()) return;
      setRemoving(false);
      if (removeRef.current === target) closeRemove(target);
    }
  };
  const menuItems: MenuEntry[] = menu ? [
    {id: 'settings', icon: 'i-sliders', label: t('web.liveTv.sourceSettings'), onSelect: () => setConfigure(menu)},
    {id: 'channels', icon: 'i-list', label: t('web.liveTv.channelsAndNumbers'), onSelect: () => setMapping(menu)},
    ...(menu.state !== 'disabled' && menu.acquisition !== 'upload' ? [{id: 'refresh', icon: 'i-refresh' as const, label: t('web.liveTv.refreshGuideNow'), onSelect: () => {void controller.refreshSource(menu.id);}}] : []),
    {id: 'toggle', icon: menu.state === 'disabled' ? 'i-play' : 'i-pause', label: t(menu.state === 'disabled' ? 'web.liveTv.enableSource' : 'web.liveTv.pauseSource'), onSelect: () => {void controller.setEnabled(menu.id, menu.state === 'disabled');}},
    {id: 'separator', separator: true},
    {id: 'remove', icon: 'i-trash', label: t('web.liveTv.removeSource'), destructive: true, onSelect: () => openRemove(menu)},
  ] : [];
  const addItems: MenuEntry[] = [
    {id: 'remote', icon: 'i-live', label: t('web.liveTv.remoteSource'), description: t('web.liveTv.remoteSourceMeta'), onSelect: () => {setTuner(undefined); setAdding(true);}},
    {id: 'discover', icon: 'i-search', label: t('web.liveTv.discover'), description: t('web.liveTv.discoverMeta'), onSelect: () => setDiscovering(true)},
    ...(liveDocumentPicker ? [{id: 'upload', icon: 'i-upload' as const, label: t('web.liveTv.upload'), description: t('web.liveTv.uploadMeta'), onSelect: () => setUploading(true)}] : []),
  ];
  const rows = sources.map(source => {
    const status = liveSourceState(source, refresh(source.id)), skipped = importSkipped(t, id => defaultI18n.has(id), source.diagnostics);
    return {source, key: source.id, name: source.name, status: {text: t(status.id), tone: status.tone},
      facts: [t('server.live.channelCount', {count: source.channels}), t('server.live.programCount', {count: source.programmes})].filter(Boolean),
      tuners: source.tunerCount > 0 ? t(source.tunerCountMode === 'discovered' ? 'web.liveTv.tunersDetected' : 'server.live.tunerCount', {count: source.tunerCount}) : undefined,
      updated: source.publishedAt ? t('server.live.guideUpdated', {when: defaultI18n.dateTime(source.publishedAt)}) : undefined,
      skipped: skipped ? [skipped.summary, ...skipped.reasons].join(' ') : undefined,
      open: () => setConfigure(source),
      opener: (node: View | null) => {if (node) rowOpeners.current.set(source.id, node); else rowOpeners.current.delete(source.id);},
      openMenu: () => {menuOpener.current = rowOpeners.current.get(source.id) ?? null; setMenu(source);}};
  });
  return {t, state, role, tab, setTab, sources, rows, addMenu, setAddMenu, addItems, adding, setAdding, discovering, setDiscovering, uploading, setUploading, tuner, setTuner,
    addButton, menu, setMenu, menuItems, menuOpener, configure, setConfigure, mapping, setMapping, shownRemove, removing, confirmCancel, closeRemove,
    removeSource: (target: RemoveOpening) => {void removeSource(target);}, removeFlight,
    checked: state.checkedAt !== undefined ? t('web.servers.checked', {when: defaultI18n.relativeTime(state.checkedAt, undefined, {inSentence: true})}) : undefined};
}

/** Server-wide defaults and limits, saved as one document with its real revision. */
export function useLiveDefaults(controller: NativeLiveOwnerController) {
  const [advanced, setAdvanced] = useState(false), [custom, setCustom] = useState(false);
  const [document, setDocument] = useState<Readonly<{revision: number; settings: LiveDefaults}>>(), [draft, setDraft] = useState<LiveDefaults>(), [error, setError] = useState<unknown>(), [busy, setBusy] = useState(false), [saved, setSaved] = useState(false);
  useEffect(() => {
    let active = true;
    void controller.liveDefaults().then(value => {if (active) {setDocument(value); setDraft(value.settings);}}, value => {if (active) setError(value);});
    return () => {active = false;};
  }, [controller]);
  const dirty = !!document && !!draft && JSON.stringify(draft) !== JSON.stringify(document.settings);
  const patch = (value: Partial<LiveDefaults>) => {setSaved(false); setDraft(current => current ? {...current, ...value} : current);};
  const save = async () => {
    if (!document || !draft) return;
    setBusy(true); setError(undefined);
    try {const next = await controller.saveLiveDefaults(document.revision, draft); setDocument(next); setDraft(next.settings); setSaved(true);}
    catch (failure) {setError(failure);}
    finally {setBusy(false);}
  };
  const guideDayChoices = [...new Set([3, 7, 14, ...(draft && draft.guideDays > 0 ? [draft.guideDays] : [])])].sort((left, right) => left - right);
  return {advanced, setAdvanced, document, draft, error, clearError: () => setError(undefined), busy, saved, dirty, patch, save: () => {void save();}, guideDayChoices,
    stability: draft ? (custom ? 'custom' : liveStabilityValue(draft)) : undefined,
    chooseStability: (value: string) => {const next = liveStabilityPatch(value); setCustom(value === 'custom'); if (next) patch(next); else if (value === 'custom') setAdvanced(true);},
    number: (value: string, minimum = 0) => Math.max(minimum, Math.round(Number(value) || minimum))};
}
