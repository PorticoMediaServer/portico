/** Browser speaker limits, never a promise about an HDMI receiver or OS mixer. */
export function destinationChannels(destination: Pick<AudioDestinationNode, 'maxChannelCount' | 'channelCount'>): number {
  const maximum = destination.maxChannelCount;
  return Number.isSafeInteger(maximum) && maximum > 0 ? Math.min(8, maximum) : Math.max(1, Math.min(2, destination.channelCount || 2));
}

/** channelCount defaults to two even on an eight-channel endpoint. Read back the real limit. */
export function configureDestination(destination: AudioDestinationNode): number {
  const count = destinationChannels(destination);
  try { destination.channelCount = count; } catch { /* fixed endpoint: report its actual count */ }
  return Math.max(1, Math.min(count, destination.channelCount || 2));
}

export type BrowserOutputClock = Pick<AudioContext, 'currentTime' | 'state' | 'baseLatency'> & {
  outputLatency?: number;
  getOutputTimestamp?: () => AudioTimestamp;
};

/** A timestamp describes the context endpoint. A MediaStream/element adds unmeasured latency. */
export function outputClockLag(context: BrowserOutputClock, nowMs: number, throughElement: boolean): {seconds: number; basis: 'timestamp' | 'latency' | 'unmeasured-carrier'} | undefined {
  if (context.state !== 'running') return undefined;
  let seconds: number | undefined;
  if (!throughElement) {
    try {
      const stamp = context.getOutputTimestamp?.();
      if (stamp && Number.isFinite(stamp.contextTime) && Number.isFinite(stamp.performanceTime) && stamp.performanceTime! > 0)
        seconds = context.currentTime - (stamp.contextTime! + (nowMs - stamp.performanceTime!) / 1000);
    } catch { /* use reported latencies */ }
  }
  const measured = seconds !== undefined && Number.isFinite(seconds) && seconds > 0;
  if (!measured) seconds = (Number.isFinite(context.outputLatency) ? Math.max(0, context.outputLatency!) : 0) + (Number.isFinite(context.baseLatency) ? Math.max(0, context.baseLatency) : 0);
  return {seconds: Math.max(0, Math.min(0.5, seconds!)), basis: throughElement ? 'unmeasured-carrier' : measured ? 'timestamp' : 'latency'};
}

export type BrowserAudioFacts = Readonly<{
  engine: 'web-audio'; bitPerfect: false; sourceRate: number; contextRate: number;
  resampledToContext: boolean; deviceRate: null; browserMayResample: true;
  sourceChannels: number; outputChannels: number; eqApplied: boolean; levelingApplied: boolean;
  /** Gain actually applied by the current normalization node, never a requested target. */
  levelingGainDb?: number;
  clock: 'timestamp' | 'latency' | 'unmeasured-carrier';
}>;
