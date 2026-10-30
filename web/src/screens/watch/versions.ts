import type {MediaVersion, PlaybackOptions} from '@core/playback-v1/types.ts';

/** A structural view of the owner's genuine fresh read. Keep the original object: the
 * runtime validates its provenance, complete fence and current authority at the command. */
export type WatchVersionSelection = Readonly<{
  options: PlaybackOptions;
  versions: readonly MediaVersion[];
  selectedVersionId?: string;
  failedVersionId?: string;
  isCurrent: () => boolean;
}>;
/** Canonical §21/23 VS-3e text: edition heading leaves picture/audio/size in detail;
 * picture heading leaves only audio/size. Audio describes this version's file default
 * (first marked default, otherwise first actual Audio[] track in stream order), never
 * global preferred audio or a viewer plan. No generic version label, language prefix or tags. */
export type WatchVersionRow = Readonly<{id: string; heading: string; detail: string}>;
export type WatchVersionBinding<Choices extends WatchVersionSelection = WatchVersionSelection> = Readonly<{
  choices: Choices;
  rows: readonly WatchVersionRow[];
  /** Settles only after the real guarded exact-version start; never an ordinary Play fallback. */
  playVersion: (request: Readonly<{choices: Choices; versionId: string; signal: AbortSignal}>) => Promise<void>;
  reportError: (error: unknown) => void;
}>;

/** Options already supplies server groups in File info order. The failed grouped ID is the
 * only exclusion; a remaining single version is still a valid failure-secondary chooser. */
export function watchVersionRows<Choices extends WatchVersionSelection>(binding: WatchVersionBinding<Choices>): readonly WatchVersionRow[] | null {
  const {choices, rows} = binding;
  if (!choices.isCurrent()) return null;
  const offered = choices.options.versions;
  if (!offered.length || new Set(offered.map(version => version.id)).size !== offered.length) return null;
  const available = offered.filter(version => version.id !== choices.failedVersionId);
  if (available.length !== choices.versions.length || rows.length !== available.length
    || available.some((version, index) => version.id !== choices.versions[index]?.id || version.id !== rows[index]?.id)) return null;
  return rows;
}

/** No start position or remembered version is owned by the screen. The command re-reads
 * actual options/resume and qualifies the exact version using its captured context/fence. */
export async function dispatchWatchVersion<Choices extends WatchVersionSelection>(binding: WatchVersionBinding<Choices>, versionId: string, signal: AbortSignal): Promise<boolean> {
  if (signal.aborted || !watchVersionRows(binding)?.some(row => row.id === versionId)) return false;
  await binding.playVersion({choices: binding.choices, versionId, signal});
  return !signal.aborted;
}
