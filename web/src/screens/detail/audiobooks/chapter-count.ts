/** The header counts what the chapter list below lists. The entity's own count is its audio files, which can be fewer than its chapters. */
export const audiobookChapterCount = (chapters: {entries: readonly unknown[]; totalCount: number} | undefined, entityCount: number | undefined): number | undefined =>
  chapters?.entries.length ? chapters.totalCount : entityCount;
