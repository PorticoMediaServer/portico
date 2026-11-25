import type {ComponentType} from 'react';
import type {NativePlaybackRuntime} from './runtime';
import type {NativePlaybackAdapter, NativePlaybackSurfaceProps} from './types';

export type NativePlaybackAdapterReturn = NativePlaybackAdapter & Readonly<{
  runtime: NativePlaybackRuntime;
  setSubtitleStyle: NonNullable<NativePlaybackAdapter['setSubtitleStyle']>;
}>;

/** Builds the shared adapter return object for both Apple and Android playback factories. */
export function createNativePlaybackAdapterReturn(
  runtime: NativePlaybackRuntime,
  Surface: ComponentType<NativePlaybackSurfaceProps>,
  options: Readonly<{offline: boolean; cast: boolean}>,
): NativePlaybackAdapterReturn {
  return {
    runtime, dock: runtime.dock, getSnapshot: runtime.getSnapshot, subscribe: runtime.subscribe, start: runtime.start, startVersion: runtime.startVersion,
    ...(options.offline ? {startOffline: runtime.startOffline} : {}),
    selectionPlayback: runtime.selectionPlayback, Surface,
    cataloguePlayback: runtime.cataloguePlayback,
    change: runtime.change, chooseQuality: runtime.chooseQuality, setRate: runtime.setRate, setForcedOnly: runtime.setForcedOnly, setSubtitleStyle: runtime.setSubtitleStyle, subtitleControls: runtime.subtitleControls, readSession: runtime.readSession, listeningRead: runtime.listeningRead,
    prepareExternalPresentation: runtime.prepareExternalPresentation, requestPictureInPicture: runtime.requestPictureInPicture,
    onTransition: runtime.onTransition, toggle: runtime.toggle, replay: runtime.replay, retry: runtime.retry, setQueuePolicy: runtime.setQueuePolicy, editQueue: runtime.editQueue, selectQueuedEntry: runtime.selectQueuedEntry, seek: runtime.seek, skip: runtime.skip, trickplay: runtime.trickplay, playBookChapter: runtime.playBookChapter,
    policyDismiss: runtime.policyDismiss, policySkip: runtime.policySkip, policyUndo: runtime.policyUndo, policyAdvance: runtime.policyAdvance, policyCancelAutoplay: runtime.policyCancelAutoplay,
    previous: runtime.previous, next: runtime.next, queuePlay: runtime.queue.playEntry.bind(runtime.queue), stop: runtime.stop, goLive: runtime.goLive, setSleepTimer: runtime.setSleepTimer, sleepEnd: runtime.sleepEnd, beginRemote: runtime.beginRemote, updateRemote: runtime.updateRemote, dismissRemote: runtime.dismissRemote, continueHere: runtime.continueHere, restoreBookmark: runtime.restoreBookmark, open: runtime.open, dispose: runtime.dispose,
    ...(options.cast ? {cast: runtime.cast} : {}),
  };
}
