import type {SubtitleService} from '../../../../../packages/client-core/src/subtitles';
import {serviceText} from '../../../../../packages/client-core/src/presentation/service-text';
import type {NativeSubtitleFileLifetime, NativeSubtitleFileMetadata} from './subtitle-file-upload';

type SourceService = Pick<SubtitleService, 'getSnapshot' | 'refresh' | 'refreshSources' | 'search' | 'apply' | 'save' | 'retry'>;
export type NativePlaybackSubtitleUpload = Parameters<SubtitleService['save']>[0];
export type NativePlaybackSubtitleFileUpload = Parameters<SubtitleService['upload']>[0];
export type NativePlaybackSubtitleControls = Readonly<{
  refresh: () => Promise<void>;
  refreshSources: () => Promise<void>;
  search: (language?: string, query?: string, page?: number) => Promise<void>;
  apply: (candidateId: string, scope: 'personal' | 'shared', rights: string) => Promise<void>;
  save: (input: NativePlaybackSubtitleUpload) => Promise<void>;
  retry: () => Promise<void>;
  /** Present only when an actual platform picker is installed. False means no file was saved. */
  addFile?: (metadata: NativeSubtitleFileMetadata, lifetime: NativeSubtitleFileLifetime) => Promise<boolean>;
}>;

/** The current runtime's service remains the only writer. A retained sheet cannot use these
 * commands after its full playback fence, authority, service or source lifetime changes. */
export function boundSubtitleControls(service: SourceService, isCurrent: () => boolean, addFile?: NonNullable<NativePlaybackSubtitleControls['addFile']>): NativePlaybackSubtitleControls {
  const assertCurrent = () => { if (!isCurrent()) throw new Error(serviceText('player.opt.unavailable')); };
  const run = async (action: () => Promise<void>): Promise<void> => {
    assertCurrent();
    await action();
    assertCurrent();
    // SubtitleService keeps failures in its authoritative snapshot and resolves its queue.
    // Reject here too, so a caller cannot treat a failed receipt as a successful save.
    if (service.getSnapshot().error) throw new Error(serviceText('player.subtitles.changeFailed'));
  };
  return Object.freeze({
    refresh: () => run(() => service.refresh()),
    refreshSources: () => run(() => service.refreshSources()),
    search: (language = '', query = '', page = 1) => run(() => service.search(language, query, page)),
    apply: (candidateId: string, scope: 'personal' | 'shared', rights: string) => run(() => {
      const offered = service.getSnapshot().candidates.filter(value => value.id === candidateId);
      if (offered.length !== 1) throw new Error(serviceText('player.opt.unavailable'));
      return service.apply(offered[0], scope, rights);
    }),
    save: (input: NativePlaybackSubtitleUpload) => run(() => service.save(input)),
    retry: () => run(() => service.retry()),
    ...(addFile ? {addFile: async (metadata: NativeSubtitleFileMetadata, lifetime: NativeSubtitleFileLifetime) => {
      assertCurrent();
      const saved = await addFile(metadata, lifetime);
      if (saved) assertCurrent();
      return saved;
    }} : {}),
  });
}
