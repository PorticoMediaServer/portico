import {dependsOn} from '@core/freshness-hub.ts';
import {useFreshness} from '../../../platform/freshness';
import {mountQuietInput, SettingsOpenContext} from '../../settings/quiet-refresh';
import {useContext, type ReactNode, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import type {LibraryContentApi} from '@core/library-content.ts';
import {ConsoleClient} from '@core/console.ts';
import {liveDefaultsForm, recordingForm} from '@core/server-admin/server-forms.ts';
import {channelRequestID} from '@core/live-source-draft.ts';
import {errorMessage} from '@core/presentation/errors.ts';
import {Button} from '../../../kit/controls';
import {Dialog} from '../../../kit/overlays';
import {LiveSourceController, type OwnerLiveSource} from './source-controller';
import {RemoteSourceDialog} from './RemoteSourceDialog';
import {UploadSourceDialog} from './UploadSourceDialog';
import {DiscoverTuners} from './DiscoverTuners';
import {openingActionAllowed, sourceAdmissionAllowed} from './dialog-lifetime';
import {SourceList} from './SourceList';
import {LiveDefaults} from './LiveDefaults';
import {AutoSaveForm} from './auto-save-form';
import {SettingsAdvanced} from '../../../kit/settings/SettingsGroups';
import {RecordingAdvanced} from '../../settings/server-livetv/recording/RecordingPage';
import {SourceSettingsController} from '../../settings/server-livetv/source-settings-controller';
import {SourceChoicesController} from '../../settings/server-livetv/source-choices-controller';
import {ChannelMapController} from '../../settings/server-livetv/channel-map-controller';
import {SourceSettingsDialog} from '../../settings/server-livetv/SourceSettingsDialog';
import {ChannelMapDialog} from '../../settings/server-livetv/ChannelMapDialog';
import {terminal} from './lifetime';
import {RecordingRulesController} from '../../settings/server-livetv/recording/rules-controller';
import {RecordingPermissionsController} from '../../settings/server-livetv/recording/permissions-controller';
import {TunerController} from '../../settings/server-livetv/recording/tuner-controller';
import {RecordingStorageController} from '../../settings/server-livetv/recording/storage-controller';
import {RecordingRootController} from '../../settings/server-livetv/recording/root-controller';
import {RecordingPage} from '../../settings/server-livetv/recording/RecordingPage';
import styles from './owner.module.css';
const t = defaultI18n.t;
type OpeningToken = {live: boolean};
type OpeningModal = {kind: 'remote'; id: string; token: OpeningToken; tuner?: string; close: () => void; saved: () => void} |
  {kind: 'upload'; id: string; token: OpeningToken; close: () => void; saved: () => void} |
  {kind: 'discover'; id: string; token: OpeningToken; close: () => void; useTuner: (tuner: string) => void};
type Modal = OpeningModal |
  {kind: 'settings'; source: OwnerLiveSource; controller: SourceSettingsController; choices: {groups: SourceChoicesController; channels: SourceChoicesController}} |
  {kind: 'channels'; source: OwnerLiveSource; controller: ChannelMapController} | {kind: 'remove'; source: OwnerLiveSource};
const isOpening = (modal: Modal | undefined): modal is OpeningModal => modal?.kind === 'remote' || modal?.kind === 'upload' || modal?.kind === 'discover';
export type OwnerSourcesOptions = Readonly<{api: LibraryContentApi; serverId: string; viewerId: string; isCurrent: () => boolean; reportError: (error: unknown) => void}>;
/** Constructed only by the mounted owner admission. Every child captures that
 * same lifetime; replacing the identity retires all reads and private drafts. */
export function createOwnerSources(options: OwnerSourcesOptions) {
  let active = true;const listeners = new Set<() => void>(), children = new Set<{dispose: () => void}>();
  const current = () => active && options.isCurrent();
  const dispose = () => {if (!active) return;active = false;for (const child of children) child.dispose();children.clear();for (const listener of listeners) listener();listeners.clear();};
  const reportError = (error: unknown) => {if (terminal(error)) {dispose();options.reportError(error);}};
  const api: LibraryContentApi = {async request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) {
    if (!current() || signal?.aborted) throw {code: 'cancelled'};
    try {const result = await options.api.request<T>(path, method, body, signal);if (!current() || signal?.aborted) throw {code: 'cancelled'};return result;}
    catch (error) {if (terminal(error)) reportError(error);throw error;}
  }};
  const sources = new LiveSourceController(api, options.serverId, current, reportError);
  const defaults = new AutoSaveForm(liveDefaultsForm({api, serverId: options.serverId, console: new ConsoleClient(api, {serverId: options.serverId, viewerId: options.viewerId}), operationId: channelRequestID}), current, reportError);
  const recording = new AutoSaveForm(recordingForm({api, serverId: options.serverId, console: new ConsoleClient(api, {serverId: options.serverId, viewerId: options.viewerId}), operationId: channelRequestID}), current, reportError);
  const permissions = new RecordingPermissionsController(api, options.serverId, current, reportError);
  const rules = new RecordingRulesController(api, options.serverId, permissions, current, reportError);
  const tuners = new TunerController(api, options.serverId, current, reportError);
  const root = new RecordingRootController(api, current, reportError);
  const storage = new RecordingStorageController(api, options.serverId, current, reportError);
  children.add(root);children.add(sources);children.add(defaults);children.add(recording);children.add(storage);children.add(tuners);children.add(permissions);children.add(rules);
  return {sources, defaults, recording, storage, root, tuners, permissions, rules, api, current, reportError, serverId: options.serverId,
    track<T extends {dispose: () => void}>(child: T): T {if (!current()) child.dispose();else children.add(child);return child;},
    release(child: {dispose: () => void}) {children.delete(child);child.dispose();},
    subscribe: (listener: () => void) => {listeners.add(listener);return () => {listeners.delete(listener);};}, dispose};
}
export type OwnerSources = ReturnType<typeof createOwnerSources>;
export function OwnerSourcesPage({source, libraryChannels}: {source: OwnerSources; libraryChannels?: ReactNode}) {
  const {hub}=useFreshness();
  const visibility=useContext(SettingsOpenContext);
  const snapshot = useSyncExternalStore(source.sources.subscribe, source.sources.getSnapshot, source.sources.getSnapshot);
  const defaults = useSyncExternalStore(source.defaults.subscribe, source.defaults.getSnapshot, source.defaults.getSnapshot);
  const recordingDefaults = useSyncExternalStore(source.recording.subscribe, source.recording.getSnapshot, source.recording.getSnapshot);
  const recordingStorage = useSyncExternalStore(source.storage.subscribe, source.storage.getSnapshot, source.storage.getSnapshot);
  const active = useSyncExternalStore(source.subscribe, source.current, () => false);
  const [modal, setModal] = useState<Modal>(), [advanced, setAdvanced] = useState(false);
  // Every actual selection is its own opening: a same-tick cancel/reopen of the same kind must not
  // reuse the spent layout effect, so the id keys each dialog and travels with its lease. The
  // parent mints the live token in the event and retires it synchronously before the child can act.
  const openings = useRef(0), modalRef = useRef<Modal | undefined>(undefined), loadedSource = useRef<object | undefined>(undefined);
  const [sourceAdmission, setSourceAdmission] = useState<Readonly<{token: object; source: OwnerSources}>>();
  const sourceToken = useRef<object | undefined>(undefined);
  const nextOpening = () => `live-source-opening-${++openings.current}`;
  const admittedSource = sourceAdmission && sourceAdmission.source === source && sourceToken.current === sourceAdmission.token ? sourceAdmission : undefined;
  const sourceCurrent = () => sourceAdmissionAllowed(admittedSource, sourceToken.current, source) && source.current();
  useLayoutEffect(() => {
    const token = {};
    sourceToken.current = token;
    setSourceAdmission({token, source});
    return () => {
      const current = modalRef.current;
      if (current && isOpening(current)) current.token.live = false;
      modalRef.current = undefined;setModal(undefined);
      if (sourceToken.current === token) sourceToken.current = undefined;
      setSourceAdmission(current => current?.token === token ? undefined : current);
    };
  }, [source]);
  const retireOpening = (value: Modal | undefined) => {if (isOpening(value)) value.token.live = false;};
  const replaceModal = (next: Modal | undefined) => {
    if (!sourceCurrent()) return;
    retireOpening(modalRef.current);modalRef.current = next;setModal(next);
  };
  const closeExact = (original: Modal) => {
    if (!sourceCurrent() || modalRef.current !== original || (isOpening(original) && !original.token.live)) return;
    retireOpening(original);modalRef.current = undefined;setModal(undefined);
  };
  useEffect(() => {
    if(!admittedSource?.token)return;
    const dependencies=[dependsOn.list('liveSources'),dependsOn.settings('server')];
    const inputs=[
      mountQuietInput(hub,'settings/live',dependencies,{id:'directory',refresh:source.sources.refreshQuietly,cancel:source.sources.invalidateQuiet,subscribe:source.sources.subscribe,
        blocked:()=>source.sources.getSnapshot().loading,settlement:()=>source.sources.getSnapshot().busy},visibility),
      ...([['defaults',source.defaults],['recording',source.recording]] as const).map(([id,form]) => mountQuietInput(hub,'settings/live',dependencies,
        {id,refresh:form.refresh,cancel:form.invalidateQuiet,subscribe:form.subscribe,blocked:form.quietBlocked,settlement:()=>form.getSnapshot().draft},visibility)),
    ];
    return () => {for(const input of inputs)input.dispose();};
  },[hub,source,admittedSource?.token,visibility]);
  useEffect(() => {
    if(modal?.kind!=='settings')return;
    const controller=modal.controller;
    const input=mountQuietInput(hub,'settings/live',[dependsOn.list('liveSources'),dependsOn.settings('server')],{
      id:`configuration/${controller.sourceId}`,refresh:controller.refreshQuietly,cancel:controller.invalidateQuiet,subscribe:controller.subscribe,blocked:controller.quietBlocked,
      settlement:()=>controller.getSnapshot().draft,
    },visibility);
    return () => input.dispose();
  },[hub,modal,visibility]);
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({sourceCurrent});
  useEffect(() => {setupPorts.current = {sourceCurrent};});
  useEffect(() => {
    const {sourceCurrent} = setupPorts.current;
    // The borrowed source is owned and disposed by Settings.route's layout cleanup, never here.
    const token = admittedSource?.token;
    if (!token || !sourceCurrent() || loadedSource.current === token) return;
    loadedSource.current = token;
    void source.sources.load();void source.defaults.load();
  }, [source, admittedSource?.token]);
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts1 = useRef({admittedSource});
  useEffect(() => {setupPorts1.current = {admittedSource};});
  useEffect(() => {
    const {admittedSource} = setupPorts1.current;
    const exact = admittedSource;
    const current = () => sourceAdmissionAllowed(exact, sourceToken.current, source) && source.current();
    if (!exact || !current() || !snapshot.document?.refresh.some(row => row.state === 'refreshing')) return;
    const timer = setTimeout(() => {if (current() && document.visibilityState !== 'hidden') void source.sources.refreshQuietly();}, 5000);
    return () => clearTimeout(timer);
  }, [source, snapshot.document, admittedSource?.token]);
  useEffect(() => () => {
    if (modal?.kind === 'settings') {source.release(modal.controller);source.release(modal.choices.groups);source.release(modal.choices.channels);}
    else if (modal?.kind === 'channels') source.release(modal.controller);
  }, [modal, source]);
  if (!active || !sourceCurrent()) return null;
  const close = () => {if (modal && sourceCurrent()) closeExact(modal);};
  const openRemote = (tuner?: string) => {
    if (!sourceCurrent()) return;
    const token: OpeningToken = {live: true};let opening: Extract<OpeningModal, {kind: 'remote'}>;
    const closeRemote = () => closeExact(opening);
    const savedRemote = () => {if (!sourceCurrent() || !openingActionAllowed(opening, modalRef.current)) return;opening.token.live = false;modalRef.current = undefined;setModal(undefined);void source.sources.load();};
    opening = {kind: 'remote', id: nextOpening(), token, ...(tuner === undefined ? {} : {tuner}), close: closeRemote, saved: savedRemote};
    replaceModal(opening);
  };
  const openUpload = () => {
    if (!sourceCurrent()) return;
    const token: OpeningToken = {live: true};let opening: Extract<OpeningModal, {kind: 'upload'}>;
    const closeUpload = () => closeExact(opening);
    const savedUpload = () => {if (!sourceCurrent() || !openingActionAllowed(opening, modalRef.current)) return;opening.token.live = false;modalRef.current = undefined;setModal(undefined);void source.sources.load();};
    opening = {kind: 'upload', id: nextOpening(), token, close: closeUpload, saved: savedUpload};
    replaceModal(opening);
  };
  const openDiscover = () => {
    if (!sourceCurrent()) return;
    const token: OpeningToken = {live: true};let opening: Extract<OpeningModal, {kind: 'discover'}>;
    const closeDiscover = () => closeExact(opening);
    const useTuner = (tuner: string) => {
      // The use must belong to this exact discovery opening; open the remote as a new opening.
      if (!sourceCurrent() || !openingActionAllowed(opening, modalRef.current)) return;
      opening.token.live = false;modalRef.current = undefined;
      openRemote(tuner);
    };
    opening = {kind: 'discover', id: nextOpening(), token, close: closeDiscover, useTuner};
    replaceModal(opening);
  };
  const advancedSummary: string[] = [];
  const liveDraft = defaults.draft, recordingDraft = recordingDefaults.draft, storageDraft = recordingStorage.draft;
  if (liveDraft) {
    if (liveDraft.streamBufferMs !== 8000) advancedSummary.push(t('web.dvrSettings.buffer'));
    if (liveDraft.retryWindowMs !== 60000) advancedSummary.push(t('web.dvrSettings.retryWindow'));
    if (liveDraft.guideDays !== 7) advancedSummary.push(t('web.dvrSettings.guideDays'));
    if (liveDraft.userAgent) advancedSummary.push(t('web.dvrSettings.userAgent'));
    if (!liveDraft.discoveryEnabled) advancedSummary.push(t('web.dvrSettings.discovery'));
    if (liveDraft.importLimits.channels !== 50000 || liveDraft.importLimits.programmes !== 5000000 || liveDraft.importLimits.bytes !== 512 * 1048576) advancedSummary.push(t('server.live.limits'));
  }
  if (recordingDraft) {
    if (recordingDraft.prePaddingMs !== 60000 || recordingDraft.postPaddingMs !== 180000) advancedSummary.push(t('next.settingsSpec.server.live.groups.6.rows.0.label'));
    if (recordingDraft.folderTemplate || recordingDraft.movieFolderTemplate) advancedSummary.push(t('next.settingsSpec.server.live.advanced.groups.4.rows.0.label'));
    if (recordingDraft.keepPolicy.mode !== 'keep-all') advancedSummary.push(t('next.settingsSpec.server.live.advanced.groups.4.rows.3.label'));
  }
  if (storageDraft) {
    if (storageDraft.retentionDays || storageDraft.episodeLimit) advancedSummary.push(t('next.settingsSpec.server.live.groups.6.rows.2.label'));
    if (storageDraft.floorBytes !== 10e9 || storageDraft.capBytes) advancedSummary.push(t('next.settingsSpec.server.live.groups.7.rows.1.label'));
  }
  return <>
    {!!snapshot.error && <p role="alert" className={styles.error}>{errorMessage(snapshot.error)}</p>}
    <SourceList snapshot={snapshot} refresh={() => {if (sourceCurrent()) void source.sources.load();}} add={kind => {
      if (!sourceCurrent()) return;
      if (kind === 'remote') openRemote();else if (kind === 'discover') openDiscover();else openUpload();
    }} configure={selected => {
      if (!sourceCurrent()) return;
      replaceModal({kind: 'settings', source: selected, controller: source.track(new SourceSettingsController(source.api, source.serverId, selected.id, source.current, source.reportError)), choices: {
        groups: source.track(new SourceChoicesController(source.api, selected.id, 'groups', source.current, source.reportError)), channels: source.track(new SourceChoicesController(source.api, selected.id, 'channels', source.current, source.reportError)),
      }});
    }} channels={selected => {if (sourceCurrent()) replaceModal({kind: 'channels', source: selected, controller: source.track(new ChannelMapController(source.api, source.serverId, selected.id, source.current, source.reportError))});}}
      act={(selected, action) => {if (!sourceCurrent()) return;if (action === 'remove') replaceModal({kind: 'remove', source: selected});else void source.sources.act(selected, action);}}/>
    {!!defaults.loadError && <p role="alert" className={styles.error}>{errorMessage(defaults.loadError)}<Button size="small" onClick={() => {if (sourceCurrent()) void source.defaults.load();}}>{t('action.tryAgain')}</Button></p>}
    {!!defaults.saveError && <p role="alert" className={styles.error}>{errorMessage(defaults.saveError)}</p>}
    <LiveDefaults form={source.defaults} isCurrent={sourceCurrent}/>
    <RecordingPage source={source} sources={snapshot.document?.sources ?? []}/>
    {libraryChannels}
    <SettingsAdvanced open={advanced} onOpenChange={setAdvanced} summary={advancedSummary}>
      <LiveDefaults form={source.defaults} isCurrent={sourceCurrent} advanced/>
      <RecordingAdvanced source={source}/>
    </SettingsAdvanced>
    {modal?.kind === 'remote' && <RemoteSourceDialog key={modal.id} openingId={modal.id} opening={modal.token} api={source.api} serverId={source.serverId} isCurrent={source.current} reportError={source.reportError} tuner={modal.tuner} close={modal.close} saved={modal.saved}/>}
    {modal?.kind === 'upload' && <UploadSourceDialog key={modal.id} openingId={modal.id} opening={modal.token} api={source.api} serverId={source.serverId} isCurrent={source.current} reportError={source.reportError} close={modal.close} saved={modal.saved}/>}
    {modal?.kind === 'discover' && <DiscoverTuners key={modal.id} openingId={modal.id} opening={modal.token} api={source.api} serverId={source.serverId} isCurrent={source.current} reportError={source.reportError} close={modal.close} onUseTuner={modal.useTuner}/>}
    {modal?.kind === 'settings' && <SourceSettingsDialog controller={modal.controller} choices={modal.choices} name={modal.source.name} guides={snapshot.document?.sources ?? []} acquisition={modal.source.acquisition} enabled={snapshot.document?.sources.some(row => row.id === modal.source.id) ? snapshot.document.sources.find(row => row.id === modal.source.id)!.state !== 'disabled' : undefined} sourceBusy={!!snapshot.busy || snapshot.loading} setEnabled={enabled => {
      const latest = source.sources.getSnapshot(), selected = latest.document?.sources.find(row => row.id === modal.source.id);
      if (sourceCurrent() && modalRef.current === modal && !latest.busy && !latest.loading && selected && (selected.state !== 'disabled') !== enabled) void source.sources.act(selected, 'toggle');
    }} close={close} changed={() => {if (sourceCurrent() && modalRef.current === modal) void source.sources.load();}}/>}
    {modal?.kind === 'channels' && <ChannelMapDialog controller={modal.controller} name={modal.source.name} close={close}/>}
    {modal?.kind === 'remove' && <Dialog open title={t('web.liveTv.removeTitle', {name: modal.source.name})} description={t('web.liveTv.removeBody')} onOpenChange={open => {if (!open) close();}}
      footer={<><Button size="small" variant="text" onClick={close}>{t('action.cancel')}</Button><Button size="small" variant="primary" loading={!!snapshot.busy} onClick={() => {if (sourceCurrent()) void source.sources.act(modal.source, 'remove').then(ok => {if (ok && sourceCurrent()) closeExact(modal);});}}>{t('web.liveTv.removeSource')}</Button></>}><></></Dialog>}
  </>;
}
