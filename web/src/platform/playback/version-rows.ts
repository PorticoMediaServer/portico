import type {MediaVersion} from '@core/playback-v1/types.ts';
import type {WebPlaybackVersionChoices} from './versions.ts';

export type PlaybackVersionRow = Readonly<{id: string; heading: string; detail: string}>;
/** Bind MAIN Settings versionChoiceRows([version], actualCanonicalPort)[0]. Settings owns
 * conditional composition; shared picture/audio names and bytes use the actual locale. */
export type PlaybackVersionRowFormatter = (version: MediaVersion) => Readonly<Pick<PlaybackVersionRow, 'heading' | 'detail'>>;

/** An adapter for the one controlled Watch chooser. It never clones the runtime-issued
 * choices, interprets joined labels, formats a track, or supplies placeholder copy. */
export function playbackVersionRows(choices: WebPlaybackVersionChoices, formatRow: PlaybackVersionRowFormatter): readonly PlaybackVersionRow[] | null {
  if (!choices.isCurrent()) return null;
  const offered = choices.options.versions;
  if (new Set(offered.map(version => version.id)).size !== offered.length) throw new Error('playback_version_rows_invalid');
  const eligible = offered.filter(version => version.id !== choices.failedVersionId);
  if (eligible.length !== choices.versions.length || eligible.some((version, index) => version !== choices.versions[index])) throw new Error('playback_version_rows_invalid');
  const rows: PlaybackVersionRow[] = [];
  for (const version of eligible) {
    const text = formatRow(version);
    if (!choices.isCurrent()) return null;
    if (typeof text.heading !== 'string' || typeof text.detail !== 'string') throw new Error('playback_version_rows_invalid');
    rows.push(Object.freeze({id: version.id, heading: text.heading, detail: text.detail}));
  }
  return Object.freeze(rows);
}
