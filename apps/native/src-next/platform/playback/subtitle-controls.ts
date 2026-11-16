import type {SubtitleService} from '../../../../../packages/client-core/src/subtitles';
import {serviceText} from '../../../../../packages/client-core/src/presentation/service-text';

type SourceService = Pick<SubtitleService, 'getSnapshot' | 'refresh' | 'search' | 'apply' | 'retry'>;
export type NativePlaybackSubtitleControls = Readonly<{
  refresh: () => Promise<void>;
  search: (language?: string) => Promise<void>;
  /** Downloads one offered result and turns it on (`choose` with the new track). */
  apply: (candidateId: string) => Promise<void>;
  retry: () => Promise<void>;
}>;

/** The current runtime's service remains the only writer. A retained sheet cannot use these
 * commands after its full playback fence, authority, service or source lifetime changes. */
export function boundSubtitleControls(service: SourceService, isCurrent: () => boolean, choose: (trackId: string) => Promise<void>): NativePlaybackSubtitleControls {
  const assertCurrent = () => { if (!isCurrent()) throw new Error(serviceText('player.opt.unavailable')); };
  const run = async (action: () => Promise<void>): Promise<void> => {
    assertCurrent();
    await action();
    assertCurrent();
    // SubtitleService keeps failures in its snapshot and resolves; reject here too, so a
    // caller cannot treat a failed download as a success.
    if (service.getSnapshot().error) throw new Error(serviceText('player.subtitles.changeFailed'));
  };
  return Object.freeze({
    refresh: () => run(() => service.refresh()),
    search: (language = '') => run(() => service.search(language)),
    apply: (candidateId: string) => run(async () => {
      const offered = service.getSnapshot().candidates.filter(value => value.id === candidateId);
      if (offered.length !== 1) throw new Error(serviceText('player.opt.unavailable'));
      const added = await service.apply(offered[0]);
      if (added && isCurrent()) await choose(added);
    }),
    retry: () => run(() => service.retry()),
  });
}
