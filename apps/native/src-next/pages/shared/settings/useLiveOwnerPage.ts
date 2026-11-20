import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import type {View} from 'react-native';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeLiveOwnerController} from '../../../screens/settings-live/live-owner-controller';
import type {LiveSource} from '../../../screens/settings-live/live-owner-model';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';

type Props = {
  controller: NativeLiveOwnerController; ports: Pick<NativeSettingsLivePorts, 'context' | 'requestId'>;
  runtime: NativeShellRuntime; initialAction?: 'add' | 'discover'; initialChannelId?: string;
};

export function useLiveOwnerPage({controller, ports, runtime, initialAction, initialChannelId}: Props) {
  const {t} = runtime.text;
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const [tab, setTab] = useState(initialChannelId ? 'channels' : 'sources');
  const [addMenu, setAddMenu] = useState(false), [adding, setAdding] = useState(initialAction === 'add'), [discovering, setDiscovering] = useState(initialAction === 'discover'), [uploading, setUploading] = useState(false), [tuner, setTuner] = useState<string>();
  const addButton = useRef<View | null>(null);
  const [menu, setMenu] = useState<LiveSource>(), [configure, setConfigure] = useState<LiveSource>(), [mapping, setMapping] = useState<LiveSource>();
  // One remove confirmation, exactly the source it was drawn for; the flight keeps that same
  // opening so a delayed close or a second press cannot reach a successor.
  type RemoveOpening = Readonly<{token: number; source: LiveSource; controller: NativeLiveOwnerController; revision: number}>;
  const [removeOpening, setRemoveOpening] = useState<RemoveOpening>();
  const removeRef = useRef<RemoveOpening | undefined>(undefined), removeFlight = useRef<RemoveOpening | undefined>(undefined), removeToken = useRef(0);
  const [removing, setRemoving] = useState(false);
  const menuOpener = useRef<View | null>(null), rowOpeners = useRef(new Map<string, View>()), confirmCancel = useRef<View>(null);
  const navigation = ports.context.navigation, mounted = useRef(true);
  useLayoutEffect(() => {mounted.current = true; return () => {mounted.current = false; removeRef.current = undefined; removeFlight.current = undefined;};}, []);

  const role = ports.context.account.account.role === 'owner' ? 'owner' as const : ports.context.account.account.role === 'admin' ? 'administrator' as const : 'member' as const;
  useEffect(() => {void controller.ensure();}, [controller]);
  const sources = state.list?.sources ?? [];
  const refresh = (id: string) => state.list?.refresh.find(value => value.sourceId === id);
  const openRemove = (source: LiveSource) => {
    if (removeFlight.current) return;
    const revision = navigation.getSnapshot().revision;
    const next: RemoveOpening = {token: ++removeToken.current, source, controller, revision}; removeRef.current = next; setRemoveOpening(next);
  };
  const closeRemove = (target: RemoveOpening) => {if (removeRef.current !== target || removeFlight.current === target) return; removeRef.current = undefined; setRemoveOpening(current => current === target ? undefined : current);};
  const liveRevision = navigation.getSnapshot().revision;
  useLayoutEffect(() => {
    const open = removeRef.current, flight = removeFlight.current;
    if (open && (open.revision !== liveRevision || open.controller !== controller)) removeRef.current = undefined;
    if (flight && (flight.revision !== liveRevision || flight.controller !== controller)) {
      removeFlight.current = undefined;
      setRemoving(false);
    }
  }, [controller, liveRevision]);
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
  const menuItems = menu ? [
    {id: 'settings', icon: 'i-sliders' as const, label: t('web.liveTv.sourceSettings'), onSelect: () => setConfigure(menu)},
    {id: 'channels', icon: 'i-list' as const, label: t('web.liveTv.channelsAndNumbers'), onSelect: () => setMapping(menu)},
    ...(menu.state !== 'disabled' && menu.acquisition !== 'upload' ? [{id: 'refresh', icon: 'i-refresh' as const, label: t('web.liveTv.refreshGuideNow'), onSelect: () => {void controller.refreshSource(menu.id);}}] : []),
    {id: 'toggle', icon: menu.state === 'disabled' ? 'i-play' as const : 'i-pause' as const, label: t(menu.state === 'disabled' ? 'web.liveTv.enableSource' : 'web.liveTv.pauseSource'), onSelect: () => {void controller.setEnabled(menu.id, menu.state === 'disabled');}},
    {id: 'separator', separator: true as const},
    {id: 'remove', icon: 'i-trash' as const, label: t('web.liveTv.removeSource'), destructive: true, onSelect: () => openRemove(menu)},
  ] : [];
  return {t, state, tab, setTab, role, sources, refresh, addMenu, setAddMenu, adding, setAdding, discovering, setDiscovering, uploading, setUploading, tuner, setTuner, addButton,
    menu, setMenu, configure, setConfigure, mapping, setMapping, shownRemove, closeRemove, removing, removeSource,
    menuItems, menuOpener, rowOpeners, confirmCancel};
}
