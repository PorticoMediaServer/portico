import {sameFence} from '../../../../../packages/player-core/src/index';
import type {PlaybackFence} from '../../../../../packages/player-core/src/index';
import type {NativeShellPlaybackAdapter} from '../../shell/playback';
import {commandIsCurrent} from './model';
import type {DockCapabilities, DockCommand} from './types';

/** Actual runtime structurally extends the shell snapshot with its authoritative capabilities. */
export type NativeDockAdapter = Omit<NativeShellPlaybackAdapter, 'getSnapshot'> & {
  getSnapshot: () => ReturnType<NativeShellPlaybackAdapter['getSnapshot']> & {capabilities: DockCapabilities};
  restoreBookmark: (expectedBookmarkFence: PlaybackFence, playExplicitly: boolean) => Promise<PlaybackFence | undefined>;
};
export type RemoteDockCommandBinding = (command: DockCommand & {remote: NonNullable<DockCommand['remote']>}) => Promise<void>;

/** A real adapter binding, not an engine simulation. Errors remain the caller's failure flow. */
export function bindNativeDock(adapter: NativeDockAdapter, options: {remote?: RemoteDockCommandBinding} = {}) {
  return async (command: DockCommand): Promise<boolean> => {
    // A Cast view identity has no local engine authority. Its caller owns dispatch.
    if(command.fence.kind==='cast-view')return false;
    if (!commandIsCurrent(adapter.dock, command)) return false;
    if (command.remote) {
      if (command.type === 'continue-here') {
        const current = adapter.dock.getSnapshot().remote;
        if (current?.phase !== 'stopped') return false;
        adapter.dock.continueHere(current.id);
        return !!(await adapter.restoreBookmark(command.fence, true));
      }
      if (!options.remote) throw new Error('A remote dock requires its actual controller binding.');
      await options.remote({...command, remote: command.remote});
      return true;
    }
    const snapshot = adapter.getSnapshot(), cap = snapshot.capabilities;
    const current = sameFence(snapshot.fence, command.fence);
    switch (command.type) {
      case 'restore-audio': case 'restore-video': {
        return !!(await adapter.restoreBookmark(command.fence, true));
      }
      case 'expand': if (!current) return false; await adapter.onTransition(adapter.dock.expand(command.fence)); adapter.open(command.fence); return true;
      case 'collapse': if (!current) return false; await adapter.onTransition(adapter.dock.collapse(command.fence)); return true;
      case 'close': case 'stop': if (!current || !cap.stop) return false; await adapter.onTransition(adapter.dock.close(command.fence)); return true;
      case 'pause': if (!current || !cap.pause || snapshot.playback.intent !== 'playing') return false; await adapter.toggle(command.fence); return true;
      case 'play': if (!current || !cap.play || snapshot.playback.intent !== 'paused') return false; await adapter.toggle(command.fence); return true;
      case 'replay': if (!current || !cap.play) return false; await adapter.replay(command.fence); return true;
      case 'seek': if (!current || !cap.seek || command.positionSeconds === undefined || !Number.isFinite(command.positionSeconds)) return false; await adapter.seek(command.fence, command.positionSeconds); return true;
      case 'previous': if (!current || !cap.previous) return false; await adapter.previous(command.fence); return true;
      case 'next': if (!current || !cap.next) return false; await adapter.next(command.fence); return true;
      case 'cast': if (!current || !cap.cast || !adapter.cast) return false; await adapter.cast(command.fence, command.anchorRef); return true;
      default: return false;
    }
  };
}
