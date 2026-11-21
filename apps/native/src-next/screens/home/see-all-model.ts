/** collections#46 draws Favorites on phone; #48 keeps only playback-state toggles on TV.
 * My List stays inside Filters on both platforms. */
export const homeSeeAllQuickIds = ['unwatched', 'in-progress', 'favorites'] as const;

export const homeSeeAllQuickOrder = (id: string, isTV = false): boolean =>
  (homeSeeAllQuickIds as readonly string[]).includes(id) && (!isTV || id !== 'favorites');
