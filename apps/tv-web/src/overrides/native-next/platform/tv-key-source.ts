import type {TVKeySource} from '../../../../../native/src-next/platform/tv-key-adapter';

/** The remote reports its keys as Fire TV's does (`src/shims/keys.ts`): directions repeat while held. */
export const tvKeySource: TVKeySource = 'vega';
