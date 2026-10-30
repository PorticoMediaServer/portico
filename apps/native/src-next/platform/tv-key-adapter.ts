export type TVKeySource = 'native' | 'vega';
export type NativeTVKeyEvent = Readonly<{eventType?: string; eventKeyAction?: number; tag?: number}>;
export type TVKeyEvent = Readonly<{
  key: string;
  phase: 'press' | 'long-begin' | 'long-end';
  repeat: boolean;
  source: TVKeySource;
  tag?: number;
}>;

const directions = new Set(['up', 'down', 'left', 'right', 'rewind', 'fastForward']);
const keys = new Set([...directions, 'select', 'playPause', 'play', 'pause', 'stop', 'next', 'previous', 'menu']);
const vegaNames: Readonly<Record<string, string>> = {
  playpause: 'playPause', play: 'playPause', pause: 'playPause',
  skip_backward: 'rewind', backward: 'rewind', skip_forward: 'fastForward', forward: 'fastForward', fastforward: 'fastForward',
  enter: 'select', back: 'menu',
};

/** Native TV taps may be release-only or down/up pairs. Vega already repeats
 * direction downs. Keep one command per tap without throwing away either form. */
export function createTVKeyAdapter({source = 'native'}: {source?: TVKeySource} = {}) {
  const held = new Map<string, {long: boolean}>();
  return {
    reset() {held.clear();},
    read(raw: NativeTVKeyEvent): TVKeyEvent | undefined {
      if (!raw || typeof raw.eventType !== 'string') return;
      const name = source === 'vega' ? vegaNames[raw.eventType] ?? raw.eventType : raw.eventType;
      const long = /^long[A-Z]/.test(name);
      const key = long ? name.charAt(4).toLowerCase() + name.slice(5) : name;
      if (!keys.has(key)) return;
      const event = (phase: TVKeyEvent['phase'], repeat = false): TVKeyEvent => ({key, phase, repeat, source, tag: raw.tag});
      const previous = held.get(key);
      if (raw.eventKeyAction === undefined) {
        if (!long) return event('press');
        if (previous?.long) return;
        held.set(key, {long: true});
        return event('long-begin');
      }
      if (raw.eventKeyAction === 0) {
        if (long) {
          if (previous?.long) return;
          held.set(key, {long: true});
          return event('long-begin');
        }
        if (previous) return source === 'vega' && directions.has(key) ? event('press', true) : undefined;
        held.set(key, {long: false});
        return event('press');
      }
      if (raw.eventKeyAction === 1) {
        held.delete(key);
        if (long || previous?.long) return event('long-end');
        if (!previous) return event('press');
      }
    },
  };
}
