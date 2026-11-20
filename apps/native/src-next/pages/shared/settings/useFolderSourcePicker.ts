import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import type {LibraryContentApi} from '../../../../../../packages/client-core/src/library-content';
import type {RemoteSource} from '../../../../../../packages/client-core/src/remote-storage';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {NativeRemoteSourcesController} from '../../../screens/settings/remote-sources/controller';
import type {ChosenSourceFolder} from '../../../screens/settings/remote-sources/RemoteSourceForms';
import type {ExplorerFolder} from '../../../screens/settings-live/owner-libraries/folder-explorer-model';

export type LibraryFolderChoice = Readonly<{folders: readonly ExplorerFolder[]; source?: RemoteSource; remoteFolders?: readonly ChosenSourceFolder[]}>;
export type Props = {open: boolean; onOpenChange: (open: boolean) => void; api: LibraryContentApi; serverName: string; chosen: LibraryFolderChoice;
  onUse: (choice: LibraryFolderChoice) => void; multiple?: boolean; ports?: NativeSettingsLivePorts; runtime?: NativeShellRuntime};

export function useFolderSourcePicker(props: Props) {
  const {ports, runtime, open} = props;
  const [controller, setController] = useState<NativeRemoteSourcesController>();
  const currentPorts = useRef(ports); 
  const isOpen = useRef(open); 
  useLayoutEffect(() => {currentPorts.current = ports; isOpen.current = open;}, [open, ports]);
  useEffect(() => {
    if (!open || !ports || !runtime) {setController(undefined); return;}
    const owned = new NativeRemoteSourcesController(ports, () => isOpen.current && currentPorts.current === ports);
    setController(owned); void owned.load();
    return () => {owned.dispose();};
  }, [open, ports, runtime]);
  return controller;
}

type Mode = {kind: 'choose' | 'local' | 'create'} | {kind: 'remote'; source: RemoteSource};
export function usePickFolderSource({controller, runtime, ...props}: Props & {controller: NativeRemoteSourcesController; runtime: NativeShellRuntime}) {
  const {t} = runtime.text;
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const [mode, setMode] = useState<Mode>({kind: 'choose'});
  const active = useRef(mode);
  useLayoutEffect(() => {active.current = mode;}, [mode]);
  const alive = useRef(true), flight = useRef(false);
  useEffect(() => {alive.current = true; return () => {alive.current = false;};}, []);
  const current = () => alive.current && props.open && active.current === mode && controller.getSnapshot().allowed;
  const chooseRemote = async (source: RemoteSource, folders: readonly ChosenSourceFolder[]) => {
    if (flight.current || !current() || !folders.length || props.multiple === false && folders.length !== 1) return;
    flight.current = true;
    try {
      // The folders were listed through the source just now; the server checks them again when they are kept.
      if (current()) {props.onUse({source, folders, remoteFolders: folders}); props.onOpenChange(false);}
    } finally {flight.current = false;}
  };
  const menu = () => {setMode({kind: 'choose'});};
  return {t, snapshot, mode, setMode, current, chooseRemote, menu};
}
