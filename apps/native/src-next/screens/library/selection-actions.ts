import {selectionActionTargetInput, type SelectionAction, type SelectionActionInput} from '../../../../../packages/client-core/src/selection-actions';
import {SelectionActionController, type SelectionActionSource} from '../../../../../packages/client-core/src/selection-actions-controller';
import type {BrowseSelectionContext, QuerySelectionTarget, SelectionOperation, SelectionSnapshot, SelectionContext} from '../../../../../packages/client-core/src/selection-model';
import type {HomeSelectionController} from '../../../../../packages/client-core/src/home-selection/index';
import type {DownloadQualityChoice} from '../../../../../packages/client-core/src/downloads';
import {musicQualityLadder} from '../../../../../packages/client-core/src/playback-v1/quality';
import {musicQualityPresentation} from '../../../../../packages/client-core/src/playback-v1/quality-presentation';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {authorityKey} from '../../shell/model';
import {offlineDownloads} from '../../platform/offline-downloads';
import type {NativeDestinationActivationPort} from '../destinations/focus';
import {createNativeDownloadsDevice} from '../downloads/device';
import {nativeScreenServices} from '../native-screen-services';
import type {NativeLibraryDestination} from './model';
import type {NativeSelectionActionDownloadQueue} from './selection-action-downloads';

const unavailable = () => Object.assign(new Error('native_selection_action_unavailable'), {code: 'unavailable'});
const retired = () => Object.assign(new Error('native_selection_action_retired'), {name: 'AbortError', code: 'cancelled'});
type Options = {action: 'delete'; options: {deleteFiles: boolean}} | {action: 'download'; options: {deviceId: string; quality: string}};
type Model<C> = Readonly<{
  captureOperation: () => SelectionOperation<C> | null;
  isOperationCurrent: (operation: SelectionOperation<C>) => boolean;
  subscribe: (listener: () => void) => () => void;
}>;

/** A synchronous offer uses exactly the same canonical guard as submission.
 * Television/Vega have no local file sink even when the server permits downloads. */
export function nativeSelectionActionInput<C>(target: QuerySelectionTarget<C>, action: SelectionAction,
  context: Pick<NativeShellBootstrapContext, 'session'>): SelectionActionInput {
  if (action === 'delete') {
    if (context.session()?.viewer.role !== 'owner') throw unavailable();
    return selectionActionTargetInput(target, {action, options: {deleteFiles: false}});
  }
  const deviceId = context.session()?.deviceId;
  if (!deviceId || !offlineDownloads.getSnapshot().supported) throw unavailable();
  return selectionActionTargetInput(target, {action, options: {deviceId, quality: 'original'}});
}

const principal = (context: NativeShellBootstrapContext) => {
  const session = context.session(), viewer = session?.viewer;
  return session && viewer ? JSON.stringify([viewer.serverId, viewer.authority, viewer.accountId, viewer.profileId,
    viewer.role, session.sessionFamilyId, session.tokenGeneration, session.authorizationHorizon, session.deviceId, session.installationId]) : '';
};

/** One actual selection opening. No Browse ranges, Detail reads or target arrays
 * are consulted. Core owns preparation, confirmation, replay, progress and cancel.
 * Closing this presentation only retires local observation of accepted commands. */
export function createNativeLibrarySelectionAction(context: NativeShellBootstrapContext, library: NativeLibraryDestination,
  activation: NativeDestinationActivationPort, model: Model<BrowseSelectionContext>,
  signal?: AbortSignal, downloads?: NativeSelectionActionDownloadQueue) {
  const opening = library.getSnapshot();
  if (opening.phase !== 'ready' || !opening.collection) throw unavailable();
  return createNativeSelectionAction(context, model, {
    isCurrent: attempted => {
      const now = library.getSnapshot();
      return activation.getSnapshot().visible && now.library.id === opening.library.id
        && now.queryRevision === opening.queryRevision && (attempted ? now.tab.id === opening.tab.id
          : now.tab === opening.tab && now.phase === 'ready' && now.collection === opening.collection && now.revision === opening.revision);
    },
    subscribe: listener => {const off = [library.subscribe(listener), activation.subscribe(listener)]; return () => {off.forEach(stop => stop());};},
    signal,
  }, downloads);
}

/** Home interception consumes the same controller/renderer, retaining the real
 * Home document before confirmation, then observes the submitted command under
 * its original route/principal/fence across its own catalogue publications. */
export function createNativeHomeSelectionAction(context: NativeShellBootstrapContext, home: HomeSelectionController,
  signal?: AbortSignal, downloads?: NativeSelectionActionDownloadQueue) {
  const document = context.home.getSnapshot().document;
  if (!document || document.viewerFence !== context.authority().viewerFence) throw unavailable();
  return createNativeSelectionAction(context, home.selection, {
    isCurrent: attempted => context.authority().viewerFence === document.viewerFence && (attempted || context.home.getSnapshot().document === document),
    subscribe: listener => context.home.subscribe(listener), signal,
  }, downloads);
}

function createNativeSelectionAction<C>(context: NativeShellBootstrapContext, model: Model<C>,
  origin: Readonly<{isCurrent: (attempted: boolean) => boolean; subscribe: (listener: () => void) => () => void; signal?: AbortSignal; requiresOperation?: boolean}>,
  downloads?: NativeSelectionActionDownloadQueue) {
  const operation = model.captureOperation();
  if (!operation || operation.target.mode !== 'query') throw unavailable();
  const target = operation.target, route = context.navigation.getSnapshot(), api = context.api, signal = origin.signal;
  const originalPrincipal = principal(context), originalAuthority = authorityKey(context.authority());
  if (!originalPrincipal || !context.current() || !origin.isCurrent(false) || signal?.aborted) throw retired();
  const abort = new AbortController(), listeners = new Set<() => void>();
  let disposed = false, lease: ReturnType<typeof createNativeDownloadsDevice> | undefined;
  let choices: readonly DownloadQualityChoice[] | undefined;
  let readingChoices: Promise<readonly DownloadQualityChoice[]> | undefined;
  const newRequestId = nativeScreenServices(context).requestId;
  const controller: SelectionActionController = new SelectionActionController({api, requestId: async () => {
    const id = await newRequestId(); check();
    const input = controller.getSnapshot().input;
    if (input?.action === 'download') {
      if (!downloads) throw unavailable();
      await downloads.follow(id, input.options.deviceId); check();
    }
    return id;
  }});
  // Once submission may have reached the server, catalogue publications caused by
  // this very command must not hide its progress. Route/query/principal still bind.
  const attempted = () => {
    const state = controller.getSnapshot();
    return !!state.job || ['submitting', 'uncertain', 'running', 'cancelling', 'result'].includes(state.phase);
  };
  const current = () => {
    if (disposed || abort.signal.aborted || signal?.aborted || !context.current() || context.api !== api
      || principal(context) !== originalPrincipal || authorityKey(context.authority()) !== originalAuthority) return false;
    const navigation = context.navigation.getSnapshot(), submitted = attempted();
    return navigation.route === route.route && navigation.revision === route.revision && origin.isCurrent(submitted)
      && (submitted && !origin.requiresOperation || model.isOperationCurrent(operation));
  };
  const changed = () => {if (!current()) abort.abort(); for (const listener of [...listeners]) listener();};
  const off = [context.subscribeAuthority(changed), context.navigation.subscribe(changed), origin.subscribe(changed), model.subscribe(changed)];
  signal?.addEventListener('abort', changed, {once: true});
  const source: SelectionActionSource = Object.freeze({isCurrent: current, signal: abort.signal, expectedTotal: target.total,
    subscribe: listener => {listeners.add(listener); return () => {listeners.delete(listener);};}});
  const check = () => {if (!current()) throw retired();};
  const input = (options: Options) => {
    check();
    nativeSelectionActionInput(target, options.action, context);
    return selectionActionTargetInput(target, options);
  };
  const device = () => {
    check(); nativeSelectionActionInput(target, 'download', context);
    if (!downloads) throw unavailable();
    return lease ??= createNativeDownloadsDevice(context);
  };
  const downloadQualities = async (): Promise<readonly DownloadQualityChoice[]> => {
    check();
    if (readingChoices) return readingChoices;
    if (choices) return Promise.resolve(choices);
    const owner = device();
    const pending = (async () => {
      await owner.device.open(); check();
      const result: readonly DownloadQualityChoice[] = 'query' in target.selector && target.selector.query.pivot === 'songs'
        ? musicQualityLadder.map(quality => Object.freeze({quality: quality.id, label: musicQualityPresentation(quality.id).label,
          kind: quality.id === 'original' ? 'source' as const : 'optimized' as const, targetDisplayHeight: 0}))
        : await owner.service.qualities(abort.signal);
      check();
      choices = Object.freeze([...result]); return choices;
    })().finally(() => {if (readingChoices === pending) readingChoices = undefined;});
    readingChoices = pending; return pending;
  };
  return Object.freeze({controller, source, target, current,
    getSnapshot: () => current() ? controller.getSnapshot() : undefined,
    subscribe: (listener: () => void) => {
      listeners.add(listener); const stop = controller.subscribe(listener);
      return () => {listeners.delete(listener); stop();};
    },
    downloadQualities,
    openDelete: async (deleteFiles = true) => {
      const request = input({action: 'delete', options: {deleteFiles}});
      await controller.open(request, source);
    },
    openDownload: async (quality: string) => {
      const available = await downloadQualities(); check();
      if (!available.some(choice => choice.quality === quality)) throw unavailable();
      const request = input({action: 'download', options: {deviceId: context.session()!.deviceId!, quality}});
      await controller.open(request, source);
    },
    dispose: () => {
      if (disposed) return;
      disposed = true; abort.abort(); controller.dispose();
      off.forEach(stop => stop()); signal?.removeEventListener('abort', changed);
      listeners.clear(); lease?.dispose(); lease = undefined; choices = undefined;
    },
  });
}

export type NativeLibrarySelectionAction = ReturnType<typeof createNativeLibrarySelectionAction>;
export type NativeSelectionActionPresentation = ReturnType<typeof createNativeSelectionAction>;

export type NativeHomeQueryActions = Readonly<{
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => NativeSelectionActionPresentation | undefined;
  offer: (kind: 'delete' | 'download', view: SelectionSnapshot<SelectionContext>) => boolean;
  open: (kind: 'delete' | 'download', view: SelectionSnapshot<SelectionContext>) => void;
  close: (expected?: NativeSelectionActionPresentation) => void;
}>;
