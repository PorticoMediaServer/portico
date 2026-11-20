/** One music section's paged window (screens/destinations/music/paging.ts owns the cursors): what its controls draw. */
export type MusicSection<T> = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error' | 'refresh-required';
  items: readonly T[]; nextCursor: string; offset: number; revision?: string;
  canNext: boolean; canPrevious: boolean; total?: number; totalIsLowerBound?: boolean;
  error?: unknown;
}>;
export type MusicSectionPage<T> = Readonly<{items: readonly T[]; nextCursor: string; revision?: string; total?: number; totalIsLowerBound?: boolean; offset?: number}>;
