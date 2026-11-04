import React, {useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import type {LibraryContentApi} from '../../../../../packages/client-core/src/library-content';
import {Button} from '../../kit/controls';
import {StateView} from '../../kit/states';
import {shellType} from '../../kit/shell/visual';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeLiveOwnerController} from './live-owner-controller';
import {FolderExplorer} from './owner-libraries/FolderExplorer';
import {NativeRecordingRoot, type RecordingRootState} from './recording-folder-store';

const idle: RecordingRootState = Object.freeze({loading: false, saving: false});
const none = () => () => {};
type Browsing = Readonly<{abort: AbortController; api: LibraryContentApi; folder: string}>;

/** live-owner 16c (phone) / 10: the Recordings folder row. Choose opens the server's folder
 * explorer for one folder, and choosing saves it straight away. */
export function RecordingFolder({controller, runtime}: {controller: NativeLiveOwnerController; runtime: NativeShellRuntime}) {
  const theme = useTheme(), t = runtime.text.t;
  const [store, setStore] = useState<NativeRecordingRoot>(), [browsing, setBrowsing] = useState<Browsing>();
  // The explorer opening, retired with its store: its listings never outlive the controller.
  const opening = useRef<Browsing | undefined>(undefined);
  useLayoutEffect(() => {
    const next = new NativeRecordingRoot({read: signal => controller.recordingRoot(signal), save: (intent, signal) => controller.saveRecordingRoot(intent, signal), current: controller.current});
    setStore(next); void next.load();
    return () => {next.dispose(); opening.current?.abort.abort(); opening.current = undefined; setStore(current => current === next ? undefined : current); setBrowsing(undefined);};
  }, [controller]);
  const state = useSyncExternalStore(store?.subscribe ?? none, store?.getSnapshot ?? (() => idle), store?.getSnapshot ?? (() => idle));
  if (!store || state.unsupported) return null;
  const folder = store.folder();
  const close = (shown: Browsing) => {shown.abort.abort(); if (opening.current !== shown) return; opening.current = undefined; setBrowsing(undefined);};
  // Use belongs to the explorer opening that offered it: a cancelled, replaced or retired opening saves nothing.
  const use = (shown: Browsing, folders: readonly Readonly<{path: string}>[]) => {
    const current = opening.current === shown && !shown.abort.signal.aborted;
    close(shown);
    if (current && folders.length === 1 && folders[0]?.path) void store.choose(folders[0].path);
  };
  const open = () => {
    if (!store.canChoose()) return;
    const abort = new AbortController(), next: Browsing = Object.freeze({abort, api: controller.folderBrowser(abort.signal), folder});
    opening.current?.abort.abort(); opening.current = next; setBrowsing(next);
  };
  return <View style={{gap: 6}} testID="live-owner-recording-folder">
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 12}}>
      <View style={{flex: 1, gap: 2}}>
        <Text style={[shellType(15, 600), {color: theme.colors.text}]}>{t('web.liveTv.recordingFolder')}</Text>
        <Text selectable style={[shellType(13), {color: theme.colors.textSecondary}]}>{folder || t('web.liveTv.recordingFolderUnset')}</Text>
      </View>
      <Button testID="live-owner-recording-folder-choose" size="small" variant="glass" loading={state.saving} disabled={!state.status || state.loading || state.saving || !!state.uncertain || state.status.operatorControlled} onPress={open}>{t('server.live.choose')}</Button>
    </View>
    <Text style={[shellType(12.5), {color: theme.colors.textTertiary}]}>{t('web.liveTv.recordingFolderHelp')}</Text>
    {state.status?.pendingRestart && <Text accessibilityRole="text" style={[shellType(12.5), {color: theme.colors.amber}]}>{t('web.liveTv.recordingFolderPending')}</Text>}
    {state.status?.operatorControlled && <Text style={[shellType(12.5), {color: theme.colors.textTertiary}]}>{t('web.liveTv.recordingFolderOperator')}</Text>}
    {state.retired && <Text style={[shellType(12.5), {color: theme.colors.textSecondary}]}>{t('web.liveTv.recordingFolderRequestRetired')}</Text>}
    {state.error !== undefined && <StateView testID="live-owner-recording-folder-error" {...runtime.error(state.error, () => void store.retry(state), 'live')}/>}
    {browsing && <FolderExplorer open testID="live-owner-recording-folder-explorer" onOpenChange={value => {if (!value) close(browsing);}} api={browsing.api} title={t('web.liveTv.recordingFolder')}
      chosen={browsing.folder ? [Object.freeze({path: browsing.folder, name: browsing.folder})] : []} multiple={false}
      onUse={folders => use(browsing, folders)}/>}
  </View>;
}
