import {ArtworkScheduler, type ArtworkFailure, type ArtworkRequestOptions, type ArtworkSchedulerOptions} from '@core/artwork/index.ts';
import type {AmbientArtworkSource} from '@core/artwork/ambient.ts';
import type {AmbientArtSource, ArtworkImage, ArtworkSource, ArtworkStatus} from './types';

export function scheduledAmbientSource(scheduler: ArtworkScheduler<ArtworkImage>, source: AmbientArtworkSource, request?: Omit<ArtworkRequestOptions, 'signal'>): AmbientArtSource {
  return Object.freeze({scheduler, key: source.cacheKey, variant: source.variant, maxEdge: source.maxEdge, version: source.version, ...(request ? {request} : {})});
}

export type ArtworkRead = Readonly<{status: ArtworkStatus; image?: ArtworkImage; cached?: boolean}>;
/** One mounted interest, sharing the canonical queue, retries and cooldown. */
export function observeArtwork(source: ArtworkSource, receive: (read: ArtworkRead) => void): () => void {
  const controller = new AbortController();
  const release = source.retain?.();
  const cached = source.scheduler.peek(source.key);
  if (cached) receive({status: 'ready', image: cached, cached: true});
  else receive({status: 'loading'});
  void source.scheduler.load(source.key, {...source.request, signal: controller.signal}).then(image => {
    if (!controller.signal.aborted) receive({status: 'ready', image, cached: !!cached});
  }, () => {
    if (!controller.signal.aborted) receive({status: 'error'});
  });
  return () => { controller.abort(); release?.(); };
}

export type ArtworkLoadRequest = Readonly<{url: string; cacheKey: string; kind: 'foreground' | 'ambient'}>;
export type ArtworkRuntimeOptions = Readonly<{
  /** A different runtime per server/account/profile/current authority. Dispose on scope change. */
  scopeKey: string;
  load: (request: ArtworkLoadRequest, signal: AbortSignal) => Promise<ArtworkImage>;
  /** The platform classifies its actual HTTP failures, including Retry-After. */
  classify: (error: unknown) => ArtworkFailure;
  release?: (image: ArtworkImage) => void;
  policy?: Omit<ArtworkSchedulerOptions<ArtworkImage>, 'load' | 'classify' | 'release' | 'isPinned' | 'sizeOf'>;
}>;

/** No transport or default executor: the selected platform supplies authentication and I/O. */
export function createArtworkRuntime(options: ArtworkRuntimeOptions) {
  if (!options.scopeKey) throw new Error('Artwork scope is required.');
  let disposed = false;
  const retainScope = Object.freeze({});
  const pins = new WeakMap<ArtworkImage, number>();
  const identities = new WeakMap<ArtworkImage, string>();
  const interests = new Map<string, number>();
  const scheduler = new ArtworkScheduler<ArtworkImage>({
    maxBytes: 32 * 1024 * 1024, ...options.policy,
    load: async (key, signal) => {
      const [scopeKey, cacheKey, url, kind] = JSON.parse(key) as [string, string, string, ArtworkLoadRequest['kind']];
      if (disposed || scopeKey !== options.scopeKey) throw new Error('Artwork authority changed.');
      const value = await options.load(Object.freeze({url, cacheKey, kind}), signal);
      if (disposed || signal.aborted) { options.release?.(value); throw new Error('Artwork authority changed.'); }
      let image: ArtworkImage;
      image = Object.freeze({...value, retain: () => {
        pins.set(image, (pins.get(image) ?? 0) + 1);
        const release = value.retain?.(); let released = false;
        return () => { if (released) return; released = true; pins.set(image, Math.max(0, (pins.get(image) ?? 0) - 1)); release?.(); };
      }});
      identities.set(image, key);
      return image;
    },
    classify: options.classify, release: options.release,
    sizeOf: image => image.bytes ?? 0, isPinned: image => (pins.get(image) ?? 0) > 0 || (interests.get(identities.get(image) ?? '') ?? 0) > 0,
  });
  const source = (input: ArtworkLoadRequest, request?: Omit<ArtworkRequestOptions, 'signal'>): ArtworkSource => {
    if (disposed || !input.url || !input.cacheKey) throw new Error('Artwork source is unavailable.');
    const key = JSON.stringify([options.scopeKey, input.cacheKey, input.url, input.kind]);
    return Object.freeze({scheduler, key, retainScope, ...(request ? {request} : {}), retain: () => {
      if (disposed) return () => {};
      interests.set(key, (interests.get(key) ?? 0) + 1); let released = false;
      return () => { if (released) return; released = true; const count = (interests.get(key) ?? 1) - 1; if (count > 0) interests.set(key, count); else interests.delete(key); };
    }});
  };
  return Object.freeze({
    scheduler,
    source: (input: Omit<ArtworkLoadRequest, 'kind'>, request?: Omit<ArtworkRequestOptions, 'signal'>) => source({...input, kind: 'foreground'}, request),
    ambient: (input: AmbientArtworkSource, request?: Omit<ArtworkRequestOptions, 'signal'>): AmbientArtSource => Object.freeze({
      ...source({url: input.url, cacheKey: input.cacheKey, kind: 'ambient'}, request), variant: input.variant, maxEdge: input.maxEdge, version: input.version,
    }),
    dispose: () => { if (!disposed) { disposed = true; scheduler.dispose(); interests.clear(); } },
  });
}
