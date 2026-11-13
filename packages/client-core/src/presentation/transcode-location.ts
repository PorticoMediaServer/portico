import type {TemporaryDirectory} from '../telemetry.ts';

/** The preference and chosen disk folder are independent; Memory uses that folder for spills. */
export type TranscodeLocation = 'separate' | 'memory' | 'folder';

export function transcodeLocationChoice(directory: Pick<TemporaryDirectory, 'memoryAvailable'> | undefined, storedLocation?: string, storedFolder?: unknown): Readonly<{value: TranscodeLocation; memoryAvailable: boolean}> {
  const value = storedLocation === 'memory' ? 'memory' : storedLocation === 'folder' || storedLocation === 'disk' && typeof storedFolder === 'string' && storedFolder.trim() !== '' ? 'folder' : 'separate';
  return Object.freeze({value, memoryAvailable: directory?.memoryAvailable === true});
}
