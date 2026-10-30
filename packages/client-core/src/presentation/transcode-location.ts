import type {TemporaryDirectory} from '../telemetry.ts';

/** The owner preference is independent of the disk conversion folder. Playback
 * falls back to that folder whenever this host cannot safely use memory. */
export type TranscodeLocation = 'memory' | 'disk';

export function transcodeLocationChoice(directory: Pick<TemporaryDirectory, 'memoryAvailable'> | undefined, storedLocation?: string): Readonly<{value: TranscodeLocation; memoryAvailable: boolean}> {
  return Object.freeze({value: storedLocation === 'disk' ? 'disk' : 'memory', memoryAvailable: directory?.memoryAvailable === true});
}
