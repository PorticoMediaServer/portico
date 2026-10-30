/** A captured remote connection, not authority to read a path. The library
 * writer compares it with its path association in the accepting transaction. */
export type ExpectedRemoteSource = Readonly<{sourceId: string; generation: number}>;
export function expectedRemoteSource(value: ExpectedRemoteSource): ExpectedRemoteSource {
  if (!value || typeof value.sourceId !== 'string' || !value.sourceId || value.sourceId.length > 256 || /[\x00-\x1f\x7f]/.test(value.sourceId)
    || !Number.isSafeInteger(value.generation) || value.generation < 1) throw new Error('Invalid remote source selection.');
  return Object.freeze({sourceId: value.sourceId, generation: value.generation});
}
