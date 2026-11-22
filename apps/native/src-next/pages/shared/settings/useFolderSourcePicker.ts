import {useEffect, useRef, useState, useSyncExternalStore} from 'react';
import type {LibraryContentApi} from '../../../../../../packages/client-core/src/library-content';
import type {RemoteSource} from '../../../../../../packages/client-core/src/remote-storage';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {NativeRemoteSourcesController} from '../../../screens/settings/remote-sources/controller';
import type {ChosenSourceFolder} from '../../../screens/settings/remote-sources/RemoteSourceForms';
import type {ExplorerFolder} from '../../../kit/settings/FolderExplorer';

export type LibraryFolderChoice = Readonly<{folders: readonly ExplorerFolder[]; source?: RemoteSource; remoteFolders?: readonly ChosenSourceFolder[]}>;
export type FolderSourcePickerProps = {open: boolean; onOpenChange: (open: boolean) => void; api: LibraryContentApi; serverName: string; chosen: LibraryFolderChoice;
  onUse: (choice: LibraryFolderChoice) => void; multiple?: boolean; ports?: NativeSettingsLivePorts; runtime?: NativeShellRuntime};

/** The owner's saved remote sources while the picker is open (setup, with no owner ports, keeps the local explorer only).
 * Moved from the old picker as it was. */
export function useRemoteSourcesWhileOpen({open, ports, runtime}: Pick<FolderSourcePickerProps, 'open' | 'ports' | 'runtime'>) {
  const [controller, setController] = useState<NativeRemoteSourcesController>();
  const currentPorts = useRef(ports); currentPorts.current = ports;
  const isOpen = useRef(open); isOpen.current = open;
  useEffect(() => {
    if (!open || !ports || !runtime) {setController(undefined); return;}
    const owned = new NativeRemoteSourcesController(ports, () => isOpen.current && currentPorts.current === ports);
    setController(owned); void owned.load();
    return () => {owned.dispose();};
  }, [open, ports, runtime]);
  return controller;
}

export type FolderSourceMode = {kind: 'choose' | 'local' | 'create'} | {kind: 'remote'; source: RemoteSource};
/** Choosing where a library's folder comes from: this server's folders, a saved remote's folders (listed through it just
 * now; the server checks them again when they are kept), or a new remote. Moved from the old picker as it was. */
export function useFolderSource(controller: NativeRemoteSourcesController, props: FolderSourcePickerProps) {
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const [mode, setMode] = useState<FolderSourceMode>({kind: 'choose'});
  const active = useRef(mode); active.current = mode;
  const alive = useRef(true), flight = useRef(false);
  useEffect(() => {alive.current = true; return () => {alive.current = false;};}, []);
  const current = () => alive.current && props.open && active.current === mode && controller.getSnapshot().allowed;
  const chooseRemote = async (source: RemoteSource, folders: readonly ChosenSourceFolder[]) => {
    if (flight.current || !current() || !folders.length || props.multiple === false && folders.length !== 1) return;
    flight.current = true;
    try {
      if (current()) {props.onUse({source, folders, remoteFolders: folders}); props.onOpenChange(false);}
    } finally {flight.current = false;}
  };
  return {snapshot, mode, setMode, current, chooseRemote, menu: () => {setMode({kind: 'choose'});}};
}
