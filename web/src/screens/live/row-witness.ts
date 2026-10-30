import type {GuideChannelRow} from '@core/guide/index.ts';

/** The exact row a store drew, and that row's membership in the same store.
 * Identity is the row object, never an equal id looked up later. */
export type RowWitness = Readonly<{row: GuideChannelRow; current: () => boolean}>;

/** The admission behind a selection: a store row witness, or a deep-link
 * read's own token, which is retired when a newer read replaces it. */
export type SelectionAdmission = Readonly<{row: GuideChannelRow; current: () => boolean; retire?: () => void}>;
