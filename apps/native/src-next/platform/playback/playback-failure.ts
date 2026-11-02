type EngineFailure = 'decode_error' | 'source_not_supported';
type FailureFact = {httpStatus?: unknown; networkError?: unknown; deliveryError?: unknown; engineCode?: unknown; hasPlayed?: unknown; uncertain?: unknown};

/** Native error wrapping differs, but HTTP delivery and startup recovery have one policy. */
export function classifyPlaybackFailure(fact: FailureFact): {network: boolean; engine: EngineFailure | null} {
  const status = fact.httpStatus;
  if (typeof status === 'number' && Number.isInteger(status) && status >= 400 && status <= 599) {
    return {network: status >= 500 || status === 408 || status === 429, engine: null};
  }
  if (fact.deliveryError === true) return {network: fact.networkError === true, engine: null};
  if (fact.networkError === true) return {network: true, engine: null};
  // A failure before anything has played is taken for an incompatibility, unless the runtime says
  // its cause is uncertain (an interruption, a time-out, a transport that was reporting trouble):
  // that is not the media's fault, and nothing is reported to the server for it.
  const engine = fact.engineCode === 'decode_error' || fact.engineCode === 'source_not_supported' ? fact.engineCode :
    fact.hasPlayed === false && fact.uncertain !== true ? 'source_not_supported' : null;
  return {network: false, engine};
}

/** E-mobile-27: a media failure at the natural end (within a second of a known duration) is the
 * end of the item, not a stop. A short clip's last read can fail on the server after the final
 * frame; the viewer saw it all, so it is Finished with Replay, never "Playback stopped". */
export function atNaturalEnd(position: unknown, duration: unknown): boolean {
  return typeof position === 'number' && typeof duration === 'number' && Number.isFinite(position) && Number.isFinite(duration)
    && duration > 0 && position >= duration - 1;
}

/** N1-7 (states 10 and 11): what the viewer is told about a failure, classified once from the facts
 * the player has. `undefined` keeps the neutral sentence: the cause is genuinely unknown. */
export type NativePlaybackFailureReason = 'connection' | 'stream-ended' | 'unsupported';
export function playbackFailureReason(fact: FailureFact): NativePlaybackFailureReason | undefined {
  const failure = classifyPlaybackFailure(fact);
  if (failure.engine) return 'unsupported';
  if (failure.network) return 'connection';
  // The server answered but would not go on (a 4xx, an ended lease, a stream it no longer has).
  const status = fact.httpStatus;
  if (fact.deliveryError === true || typeof status === 'number' && status >= 400 && status <= 499) return 'stream-ended';
  return undefined;
}
