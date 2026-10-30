import type {AmbientArtworkSource} from '../../../../../packages/client-core/src/artwork/index';
import type {AmbientArtSource} from './types';

/** Only binds the accepted canonical source to the existing authenticated transport. */
export function nativeAmbientSource(source: AmbientArtworkSource | null | undefined, resolveUri: (url: string) => string | undefined, headers?: Readonly<Record<string, string>>): AmbientArtSource | undefined {
  if (!source || source.variant !== 'ambient-v1' || source.maxEdge !== 64 || !source.version || !source.cacheKey) return undefined;
  const uri = resolveUri(source.url);
  return uri ? {...source, uri, ...(headers ? {headers} : {})} : undefined;
}
