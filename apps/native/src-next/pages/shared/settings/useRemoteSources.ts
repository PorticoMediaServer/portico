import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import type {RemoteSource} from '../../../../../../packages/client-core/src/remote-storage';
import {NativeRemoteSourcesController, type NativeRemoteSourcesSnapshot} from '../../../screens/settings/remote-sources/controller';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';

type Mode = {kind: 'list'} | {kind: 'add'} | {kind: 'manage' | 'folders'; source: RemoteSource};
const empty: NativeRemoteSourcesSnapshot = {allowed: true, loading: true, sources: []};
const getEmpty = () => empty;
const noSubscription = () => () => {};

export function useRemoteSources(ports: NativeSettingsLivePorts, initialCreate?: boolean, onDismiss?: () => void, onBackHandlerChange?: (handler: (() => boolean) | undefined) => void) {
  const currentPorts = useRef(ports);
  const [opened, setOpened] = useState<{ports: NativeSettingsLivePorts; controller: NativeRemoteSourcesController}>();
  useLayoutEffect(() => {currentPorts.current = ports;}, [ports]);
  useEffect(() => {
    const controller = new NativeRemoteSourcesController(ports, () => currentPorts.current === ports);
    setOpened({ports, controller});
    void controller.load();
    return () => controller.dispose();
  }, [ports]);
  const controller = opened?.ports === ports ? opened.controller : undefined;
  const snapshot = useSyncExternalStore(controller?.subscribe ?? noSubscription, controller?.getSnapshot ?? getEmpty);
  const first: Mode = initialCreate ? {kind: 'add'} : {kind: 'list'};
  const [view, setView] = useState<{ports: NativeSettingsLivePorts; mode: Mode}>({ports, mode: first});
  const mode = view.ports === ports ? view.mode : first;
  const setMode = (mode: Mode) => setView({ports, mode});
  const list = () => setMode({kind: 'list'});
  const back = () => {
    if (mode.kind === 'list') return false;
    if (initialCreate && mode.kind === 'add') onDismiss?.();
    else list();
    return true;
  };
  useEffect(() => {
    onBackHandlerChange?.(back);
    return () => onBackHandlerChange?.(undefined);
  });
  const current = () => !!controller && currentPorts.current === ports && controller.getSnapshot().allowed;
  return {controller, snapshot, mode, setMode, list, back, current};
}
