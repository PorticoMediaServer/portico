import React, {useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {sourceStateText} from '../../../../../../packages/client-core/src/server-admin/storage-words';
import type {RemoteSource, RemoteState} from '../../../../../../packages/client-core/src/remote-sources';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import type {NativeSettingsLivePorts} from '../../settings-live/ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {Button} from '../../../kit/controls/Button';
import {ServerHeader} from '../../../kit/shell/ServerIdentity';
import {StateView} from '../../../kit/states';
import {shellType} from '../../../kit/shell/visual';
import {useTheme} from '../../../kit/theme';
import {NativeRemoteSourcesController} from './controller';
import {ConnectionEditor, DisconnectEditor, ReadingEditor, Page, SourceWizardContext, receiptCheckRequired, remoteSourceError} from './RemoteSourceForms';

import {SourceLibraryAssignment, type ChosenSourceFolder, type AssignmentDraft} from './SourceLibraryAssignment';
import {SourceAdvancedEditor} from './SourceAdvancedEditor';
import {SourceFolderBrowser} from './SourceFolderBrowser';
import {SourceStatusPanel} from './SourceStatusPanel';
import {AccountConfigEditor} from './AccountConfigEditor';
import {ImportedSourceEditor} from './ImportedSourceEditor';
import {currentSourceGeneration, sourceBackMode, sourceWizardStep, type SourceMode as Mode, type SourceConnectionForm} from './source-flow';
import {SourceCompletion} from './SourceCompletion';

type Props = {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; onBackHandlerChange?: (handler: (() => boolean) | undefined) => void;
  initialCreate?: boolean; multipleFolders?: boolean; onDismiss?: () => void; onChooseFolders?: (source: RemoteSource, folders: readonly ChosenSourceFolder[]) => void | Promise<void>};
/** One drawn subpage and its owner. A callback may only move the exact opening it was drawn for. */
type Opening = Readonly<{id: number; mode: Mode; owner: {live: boolean}; mounted: {live: boolean}; completed: {value: boolean}}>;

/** Canonical WebDAV and imported source management. Settings owns the entry. */
export function NativeRemoteSourcesScreen({ports, runtime, onBackHandlerChange, initialCreate, multipleFolders, onDismiss, onChooseFolders}: Props) {
  const next = useRef(0), currentPorts = useRef(ports);
  currentPorts.current = ports;
  const [owned, setOwned] = useState<{controller: NativeRemoteSourcesController; ports: NativeSettingsLivePorts; key: number}>();
  useEffect(() => {
    const controller = new NativeRemoteSourcesController(ports, () => currentPorts.current === ports);
    setOwned({controller, ports, key: ++next.current});
    void controller.load();
    return () => controller.dispose();
  }, [ports]);
  return owned && owned.ports === ports ? <Viewport key={owned.key} controller={owned.controller} ports={owned.ports} runtime={runtime} onBackHandlerChange={onBackHandlerChange} initialCreate={initialCreate} multipleFolders={multipleFolders} onDismiss={onDismiss} onChooseFolders={onChooseFolders}/>
    : <StateView title={runtime.text.t('next.remoteSources.loading')}/>;
}

function Viewport({controller, ports, runtime, onBackHandlerChange, initialCreate, multipleFolders, onDismiss, onChooseFolders}: Props & {controller: NativeRemoteSourcesController}) {
  const theme = useTheme(), t = runtime.text.t as typeof defaultI18n.t;
  const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const [opening, setOpening] = useState<Opening>(), [error, setError] = useState<string>();
  const attempt = useRef(0), openingRef = useRef<Opening | undefined>(undefined);
  const [committedMount, setCommittedMount] = useState<{live: boolean}>(() => ({live: false}));
  const mounted = useRef<{live: boolean}>(committedMount);
  const rootRevision = useRef(0);
  const wizardDraft = useRef<{sourceId?: string; chosen: readonly ChosenSourceFolder[]; assignment?: AssignmentDraft}>({chosen: []});
  const mode = opening?.mode;
  const show = useCallback((next: Mode) => {
    const source = 'source' in next ? next.source : undefined;
    if (next.kind === 'choose' || next.kind === 'detail' || source && wizardDraft.current.sourceId && source.id !== wizardDraft.current.sourceId) wizardDraft.current = {chosen: []};
    if (source) wizardDraft.current.sourceId = source.id;
    if (next.kind === 'folders') next = {...next, chosen: wizardDraft.current.chosen};
    if (next.kind === 'assignment') {wizardDraft.current.chosen = next.folders;}

    const previous = openingRef.current;
    if (previous) previous.owner.live = false;
    const created: Opening = {id: ++attempt.current, mode: next, owner: {live: true}, mounted: mounted.current, completed: {value: false}};
    rootRevision.current++;
    openingRef.current = created; setOpening(created); setError(undefined);
  }, []);
  // A root Add/Manage press must still belong to the root render that offered it: the same
  // mount, the same list snapshot, no later subpage, and (Manage) the same source generation.
  const showRoot = (next: Mode, root: RemoteState, mount: {live: boolean}, revision: number, source?: RemoteSource) => {
    if (mounted.current !== mount || !mount.live || rootRevision.current !== revision) return;
    if (openingRef.current !== undefined || !controller.getSnapshot().allowed) return;
    if (controller.getSnapshot().sources !== root) return;
    if (source && !root.sources.some(row => row.id === source.id && row.generation === source.generation)) return;
    show(next);
  };
  const closeFrom = useCallback((from: Opening) => {
    if (!from.owner.live || !from.mounted.live || openingRef.current !== from) return;
    from.owner.live = false; openingRef.current = undefined; attempt.current++; rootRevision.current++;
    setOpening(undefined); setError(undefined); onDismiss?.();
  }, [onDismiss]);
  const navigate = useCallback((from: Opening, next: Mode) => {
    if (!from.owner.live || !from.mounted.live || openingRef.current !== from) return;
    const fromSource = 'source' in from.mode ? from.mode.source : undefined;
    const nextSource = 'source' in next ? next.source : undefined;
    if (fromSource && nextSource && (fromSource.id !== nextSource.id || fromSource.generation !== nextSource.generation)) return;
    // If the live list knows this source at another generation, a retained opening is stale.
    if (fromSource) {
      if (!currentSourceGeneration(fromSource, controller.getSnapshot().sources)) return;
    }
    show(next);
  }, [controller, show]);
  // An accepted create or edit may advance the source generation; its own form guard
  // already proved the opening, so only the exact id is required here.
  const saved = (from: Opening, source: RemoteSource) => {
    if (!from.owner.live || !from.mounted.live || openingRef.current !== from) return;
    const fromSource = 'source' in from.mode ? from.mode.source : undefined;
    if (fromSource && fromSource.id !== source.id) return;
    const connection = ['connection', 'imported', 's3', 'drive', 'cloud', 'driveOAuth'].includes(from.mode.kind) ? from.mode.kind as SourceConnectionForm : 'imported';
    show({kind: 'reading', source, wizard: from.mode.wizard, connection});
  };
  const back = useCallback(() => {
    const current = openingRef.current;
    if (!current || !current.owner.live || !current.mounted.live) return;
    const previous = sourceBackMode(current.mode.kind === 'folders' ? {...current.mode, chosen: wizardDraft.current.chosen} : current.mode, current.completed.value);
    if (previous) navigate(current, previous); else closeFrom(current);
  }, [closeFrom, navigate]);
  useLayoutEffect(() => {
    const token = {live: true};
    mounted.current = token;
    // Publish the committed token synchronously so root handlers from the first
    // committed render carry a live token, including after an effect replay.
    setCommittedMount(token);
    return () => {
      token.live = false;
      const current = openingRef.current;
      if (current && current.mounted === token) {
        current.owner.live = false;
        openingRef.current = undefined;
      }
    };
  }, []);
  useEffect(() => {if (initialCreate && committedMount.live && attempt.current === 0) show({kind: 'choose', wizard: true});}, [initialCreate, committedMount, show]);
  const subpage = snapshot.allowed && !!opening;
  useEffect(() => {
    onBackHandlerChange?.(subpage ? () => {if (!openingRef.current) return false; back(); return true;} : undefined);
    return () => onBackHandlerChange?.(undefined);
  }, [subpage, onBackHandlerChange, back]);
  const captured = attempt.current;
  const isCurrent = useCallback(() => controller.getSnapshot().allowed && attempt.current === captured, [controller, captured]);
  if (!snapshot.allowed) {
    const problem = presentError({code: 'forbidden'}, 'server-console');
    return <StateView title={problem.title} body={problem.body} tone="error"/>;
  }
  const step = sourceWizardStep(mode);
  const page = (child: React.ReactNode) => <SourceWizardContext.Provider value={step === undefined ? undefined : {step, back}}>{child}</SourceWizardContext.Provider>;
  if (mode?.kind === 'choose') return page(<Page title={t('next.remoteSources.whatToConnect')} t={t} busy={false} onCancel={() => closeFrom(opening!)} footer={null}>
    <Button variant="glass" onPress={() => navigate(opening!, {kind: 'connection', wizard: true})}>{t('next.remoteSources.connectWebdav')}</Button>
    <Button variant="glass" onPress={() => navigate(opening!, {kind: 'cloud', wizard: true})}>{t('next.remoteSources.connectCloud')}</Button>
    <Button variant="glass" onPress={() => navigate(opening!, {kind: 'driveOAuth', wizard: true})}>{t('next.remoteSources.driveOAuth')}</Button>
    <Button variant="glass" onPress={() => navigate(opening!, {kind: 'drive', wizard: true})}>{t('next.remoteSources.connectDrive')}</Button>
    <Button variant="glass" onPress={() => navigate(opening!, {kind: 's3', wizard: true})}>{t('next.remoteSources.connectS3')}</Button>
    <Button variant="glass" onPress={() => navigate(opening!, {kind: 'imported', wizard: true})}>{t('next.remoteSources.connectRclone')}</Button>
    <Button variant="glass" onPress={() => navigate(opening!, {kind: 'accountConfig', wizard: true})}>{t('next.remoteSources.connectFromConfig')}</Button>
  </Page>);
  if (mode?.kind === 'accountConfig') return page(<AccountConfigEditor controller={controller} t={t} isCurrent={isCurrent} onCancel={() => closeFrom(opening!)}
    onSaved={source => saved(opening!, source)}/>);
  if (mode?.kind === 'imported' || mode?.kind === 's3' || mode?.kind === 'drive' || mode?.kind === 'driveOAuth' || mode?.kind === 'cloud') return page(<ImportedSourceEditor form={mode.kind} key={mode.source?.id ?? mode.kind} controller={controller} source={mode.source} t={t} isCurrent={isCurrent}
    onCancel={() => closeFrom(opening!)} onSaved={source => saved(opening!, source)}/>);
  if (mode?.kind === 'connection') return page(<ConnectionEditor key={mode.source?.id ?? 'new'} controller={controller} source={mode.source} t={t} isCurrent={isCurrent} onCancel={() => closeFrom(opening!)}
    onSaved={source => saved(opening!, source)}/>);
  if (mode?.kind === 'assignment') return page(<SourceLibraryAssignment controller={controller} source={mode.source} folders={mode.folders} t={t} isCurrent={isCurrent}
    initialDraft={wizardDraft.current.assignment} onDraftChange={draft => {if (opening!.owner.live && openingRef.current === opening) wizardDraft.current.assignment = draft;}}
    onCancel={() => closeFrom(opening!)}
    onCompleted={() => {if (opening!.owner.live && openingRef.current === opening) opening!.completed.value = true;}}
    onChooseFolders={() => navigate(opening!, {kind: 'folders', source: mode.source, chosen: mode.folders, from: 'assignment', wizard: mode.wizard, connection: mode.connection})}
    onOpen={() => navigate(opening!, {kind: 'detail', source: mode.source})}
    onAddAnother={() => navigate(opening!, {kind: 'choose', wizard: true})}/>);
  if (mode?.kind === 'advanced') return page(<SourceAdvancedEditor key={`${mode.source.id}:${mode.source.generation}`} controller={controller} source={mode.source} t={t} isCurrent={isCurrent} onCancel={() => closeFrom(opening!)}/>);
  if (mode?.kind === 'folders') return page(<SourceFolderBrowser key={`${mode.source.id}:${mode.source.generation}`} controller={controller} source={mode.source} t={t} isCurrent={isCurrent}
    initial={mode.chosen} multiple={multipleFolders} onSelectionChange={folders => {if (opening!.owner.live && openingRef.current === opening) wizardDraft.current.chosen = folders;}}
    onCancel={() => closeFrom(opening!)}
    onLater={mode.wizard && !onChooseFolders ? () => navigate(opening!, {kind: 'complete', source: mode.source, wizard: true}) : undefined}
    onChoose={folders => {
      if (!opening!.owner.live || openingRef.current !== opening || !isCurrent()) return;
      if (onChooseFolders) return onChooseFolders(mode.source, folders);
      else navigate(opening!, {kind: 'assignment', source: mode.source, folders, wizard: mode.wizard, connection: mode.connection});
    }}/>);
  if (mode?.kind === 'reading') return page(<ReadingEditor key={`${mode.source.id}:${mode.source.generation}:${opening!.id}`} controller={controller} source={mode.source} t={t} isCurrent={isCurrent} onCancel={() => closeFrom(opening!)} onAccepted={source => {
      if (opening!.owner.live && openingRef.current === opening && isCurrent() && source.id === mode.source.id) show({...mode, source});
    }} onSaved={source => {
      if (!opening!.owner.live || openingRef.current !== opening || !isCurrent() || source.id !== mode.source.id) return;
      void controller.load();
      if (mode.wizard) show({kind: 'folders', source, chosen: wizardDraft.current.chosen, from: 'reading', wizard: true, connection: mode.connection});
      else closeFrom(opening!);
    }}/>);
  if (mode?.kind === 'disconnect') return page(<DisconnectEditor key={mode.source.id} controller={controller} source={mode.source} t={t} isCurrent={isCurrent} onCancel={() => closeFrom(opening!)} onSaved={() => closeFrom(opening!)}/>);
  if (mode?.kind === 'complete') return page(<SourceCompletion source={mode.source} controller={controller} t={t} isCurrent={isCurrent} onCancel={() => closeFrom(opening!)} onOpen={() => navigate(opening!, {kind: 'detail', source: mode.source})} onAddAnother={() => navigate(opening!, {kind: 'choose', wizard: true})}/>);
  const state = snapshot.sources, rootMount = committedMount, rootRevisionAtRender = rootRevision.current;
  const needsReceipt = receiptCheckRequired(state), blocked = state.busy || needsReceipt;
  const recover = async () => {
    setError(undefined);
    try {await controller.recover();}
    catch (problem) {if (controller.getSnapshot().allowed) setError(remoteSourceError(t, problem));}
  };
  const detail = mode?.kind === 'detail' ? mode.source : undefined;
  const listed = detail ? state.sources.find(source => source.id === detail.id) : undefined;
  const selected = detail ? listed && listed.generation >= detail.generation ? listed : detail : undefined;
  return <ScrollView testID="native-remote-sources" contentContainerStyle={{padding: 20, paddingBottom: 40, gap: 16}}>
    <ServerHeader serverName={ports.serverName()} viewerRole="owner" audience="owner" variant="phone"/>
    <Text accessibilityRole="header" style={[shellType(28, 800, 33, -.03), {color: theme.colors.text}]}>{selected?.name ?? t('settings.server.remoteSources')}</Text>
    {needsReceipt && <Button disabled={state.busy} onPress={() => {void recover();}}>{t('next.remoteSources.checkResult')}</Button>}
    {(state.error || error) && <StateView title={error ?? remoteSourceError(t, {code: state.errorCode})} tone="error"/>}
    {selected ? <>
      <SourceStatusPanel key={`${selected.id}:${selected.generation}`} controller={controller} source={selected} isCurrent={isCurrent} t={t}/>
      <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{sourceStateText(t, id => defaultI18n.has(id), selected)}</Text>
      {!!selected.origin && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{selected.origin}</Text>}
      <Button variant="text" onPress={() => closeFrom(opening!)}>{t('next.remoteSources.back')}</Button>
      <Button variant="glass" disabled={blocked} onPress={() => navigate(opening!, {kind: 'advanced', source: selected})}>{t('next.remoteSources.advanced')}</Button>
      <Button variant="glass" disabled={blocked} onPress={() => navigate(opening!, {kind: 'folders', source: selected, chosen: [], from: 'detail'})}>{t('next.remoteSources.foldersSection')}</Button>
      <Button variant="glass" disabled={blocked} onPress={() => navigate(opening!, {kind: 'reading', source: selected})}>{t('next.remoteSources.reading')}</Button>
      {(selected.kind === 'webdav' || selected.kind === 'rclone') && <>
        <Button variant="glass" disabled={blocked} onPress={() => navigate(opening!, {kind: selected.kind === 'rclone' ? 'imported' : 'connection', source: selected})}>{t('next.remoteSources.connection')}</Button>
        <Button variant="text" disabled={blocked} onPress={() => navigate(opening!, {kind: 'disconnect', source: selected})}>{t('next.remoteSources.disconnect')}</Button>
      </>}
    </> : <>
      <Button disabled={blocked} onPress={() => showRoot({kind: 'choose', wizard: true}, state, rootMount, rootRevisionAtRender)}>{t('next.remoteSources.add')}</Button>
      <Button variant="text" disabled={blocked || state.loading} onPress={() => {void controller.load();}}>{t('next.remoteSources.refresh')}</Button>
      {state.loading && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t('next.remoteSources.loading')}</Text>}
      {!state.loading && !state.error && !state.sources.length && <StateView title={t('next.remoteSources.empty')} body={t('next.remoteSources.emptyHelp')}/>}
      {state.sources.map(source => <View key={source.id} testID={`remote-source-${source.id}`} style={{paddingVertical: 16, borderBottomWidth: 1, borderBottomColor: theme.colors.line, gap: 6}}>
        <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{source.name}</Text>
        <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{sourceStateText(t, id => defaultI18n.has(id), source)}</Text>
        {!!source.origin && <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{source.origin}</Text>}
        <Button variant="text" disabled={blocked} onPress={() => showRoot({kind: 'detail', source}, state, rootMount, rootRevisionAtRender, source)}>{t('next.remoteSources.manage')}</Button>
      </View>)}
    </>}
  </ScrollView>;
}
