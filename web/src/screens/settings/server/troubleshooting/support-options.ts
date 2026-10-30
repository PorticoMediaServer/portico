import type {SupportComponent} from './support-source';

/** App logs are opt-in for every new export. */
export const DEFAULT_SUPPORT_COMPONENTS: readonly SupportComponent[] = Object.freeze(['runtime', 'audit', 'snapshot']);
export const SUPPORT_WINDOWS = Object.freeze([
  {id: '1h', ms: 60 * 60 * 1000},
  {id: '6h', ms: 6 * 60 * 60 * 1000},
  {id: '24h', ms: 24 * 60 * 60 * 1000},
  {id: '3d', ms: 3 * 24 * 60 * 60 * 1000},
  {id: '7d', ms: 7 * 24 * 60 * 60 * 1000},
]);
