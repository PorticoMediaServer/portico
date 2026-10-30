import type {PlaybackOptions, Session} from '../../../../../packages/client-core/src/playback-v1/types';

export type AudioTrackBinding = Readonly<{intentId: number; sessionId: string; generation: number}>;
export type NativeAudioTrackCommand = Readonly<{binding: AudioTrackBinding; revision: number; streamIndex: number}>;
export type NativeAudioTrackStatus = Readonly<{command: NativeAudioTrackCommand; state: 'pending' | 'applied' | 'failed'}>;

/** V1 audio IDs name absolute demuxer stream indices (a0, a1, ...), not audio-list ordinals.
 * Decode the accepted presentation immediately; an optional catalogue must agree.
 * The engine still verifies a unique actual stream and acknowledges its selection. */
export function directAudioStreamIndex(engine: string, session: Session | undefined, options: PlaybackOptions | undefined): number | undefined {
  if (engine !== 'mpv' || session?.kind !== 'vod' || session.role !== 'local' || session.presentation.mode !== 'direct') return;
  const id = session.presentation.audioTrackId;
  if (!id || !/^a(?:0|[1-9]\d{0,4})$/.test(id)) return;
  if (options) {
    if (options.itemId !== session.itemId) return;
    const versions = options.versions.filter(version => version.id === session.versionId);
    if (versions.length !== 1 || versions[0]!.audio.filter(track => track.id === id).length !== 1) return;
  }
  const index = Number(id.slice(1));
  return index <= 65535 ? index : undefined;
}

const sameBinding = (left: AudioTrackBinding, right: AudioTrackBinding) => left.intentId === right.intentId
  && left.sessionId === right.sessionId && left.generation === right.generation;

/** Requested and applied tracks stay distinct until the current native engine acknowledges. */
export class NativeAudioTrackSelection {
  private revision = 0;
  private status?: NativeAudioTrackStatus;
  getSnapshot(): NativeAudioTrackStatus | undefined { return this.status; }
  select(binding: AudioTrackBinding, streamIndex: number | undefined): NativeAudioTrackCommand | null {
    if (streamIndex === undefined) {this.clear(); return null;}
    if (!Number.isSafeInteger(streamIndex) || streamIndex < 0 || streamIndex > 65535) throw new Error('Invalid audio track.');
    const previous = this.status?.command;
    if (previous && sameBinding(previous.binding, binding) && previous.streamIndex === streamIndex) return previous;
    const command = Object.freeze({binding: Object.freeze({...binding}), revision: ++this.revision, streamIndex});
    this.status = Object.freeze({command, state: 'pending'});
    return command;
  }
  accept(fact: Readonly<{type?: unknown; binding?: AudioTrackBinding; audioTrackRevision?: unknown; streamIndex?: unknown}>): boolean {
    const command = this.status?.command;
    if (!command || !fact.binding || !sameBinding(command.binding, fact.binding) || fact.audioTrackRevision !== command.revision
      || fact.streamIndex !== command.streamIndex || fact.type !== 'audioTrackApplied' && fact.type !== 'audioTrackFailed') return false;
    this.status = Object.freeze({command, state: fact.type === 'audioTrackApplied' ? 'applied' : 'failed'});
    return true;
  }
  clear(): void { this.status = undefined; }
}
