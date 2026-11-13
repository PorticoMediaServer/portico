export const bufferingPresets = {
  balanced: {streamBufferMs: 8000, retryWindowMs: 60000},
  smooth: {streamBufferMs: 20000, retryWindowMs: 180000},
  live: {streamBufferMs: 3000, retryWindowMs: 30000},
} as const;
export const paddingPresets = {
  standard: {prePaddingMs: 60000, postPaddingMs: 180000},
  exact: {prePaddingMs: 0, postPaddingMs: 0},
  generous: {prePaddingMs: 300000, postPaddingMs: 600000},
  sport: {prePaddingMs: 60000, postPaddingMs: 900000},
} as const;
export const retentionPresets = {
  all: {keepDays: 0, keepNewest: 0},
  d30: {keepDays: 30, keepNewest: 0},
  d90: {keepDays: 90, keepNewest: 0},
  n5: {keepDays: 0, keepNewest: 5},
  n10: {keepDays: 0, keepNewest: 10},
} as const;
export const spacePresets = {
  free10: {floorBytes: 10 * 1024**3, capBytes: 0},
  up250: {floorBytes: 10 * 1024**3, capBytes: 250 * 1024**3},
  up500: {floorBytes: 10 * 1024**3, capBytes: 500 * 1024**3},
  up1000: {floorBytes: 10 * 1024**3, capBytes: 1000 * 1024**3},
} as const;
export const layoutPresets = {
  own: {folderTemplate: '', movieFolderTemplate: ''},
  season: {folderTemplate: '{series}/Season {season:02}/{series} S{season:02}E{episode:02}', movieFolderTemplate: '{title} ({year})/{title} ({year})'},
  flat: {folderTemplate: '{series} - S{season:02}E{episode:02}', movieFolderTemplate: '{title} ({year})'},
} as const;

/** A preset is exact. Never silently round a custom server value to a preset. */
export function matchingPreset(presets: Readonly<Record<string, Readonly<Record<string, number | string>>>>, value: object): string {
  return Object.keys(presets).find(key => Object.entries(presets[key]!).every(([field, expected]) => (value as Record<string, unknown>)[field] === expected)) ?? 'custom';
}
