import type {DAVConnectionResult, SourceReadValues} from '../../../../../../packages/client-core/src/remote-sources';

export type SourceLimit = 'connections' | 'requestsPerSecond' | 'bytesPerSecond';

/** The speed editor is in MB/s; the persisted profile is in bytes/s. */
export function sourceLimitValue(field: SourceLimit, text: string): number | undefined {
  if (field === 'bytesPerSecond' && text.trim() === '') return 0;
  if (!/^\d+$/.test(text.trim())) return;
  const number = Number(text), max = field === 'connections' ? 32 : field === 'requestsPerSecond' ? 50 : 1000;
  if (!Number.isSafeInteger(number) || number < (field === 'bytesPerSecond' ? 0 : 1) || number > max) return;
  return field === 'bytesPerSecond' ? number * 1000000 : number;
}

export function readingDiffers(values: SourceReadValues, baseline: SourceReadValues): boolean {
  return (['scanDepth', 'wholeFileReads', 'connections', 'requestsPerSecond', 'bytesPerSecond'] as const).some(key => values[key] !== baseline[key]);
}

/** Unknown is not evidence of either working or unsupported range reads. */
export function connectionTestTone(result: DAVConnectionResult): 'success' | 'limited' | 'unknown' {
  return result.rangeSupport === 'conditional' ? 'success' : result.rangeSupport === 'unsupported' ? 'limited' : 'unknown';
}

export function connectionTestField(error: unknown): 'password' | undefined {
  return error && typeof error === 'object' && 'code' in error && (error.code === 'source_credentials_required' || error.code === 'remote_credentials_required') ? 'password' : undefined;
}
