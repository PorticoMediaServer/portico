/** collections#46 draws Favorites on phone; #48 keeps only playback-state toggles on TV.
 * My List stays inside Filters on both platforms. */
export const homeSeeAllQuickIds = ['unwatched', 'in-progress', 'favorites'] as const;

export const homeSeeAllQuickOrder = (id: string, isTV = false): boolean =>
  (homeSeeAllQuickIds as readonly string[]).includes(id) && (!isTV || id !== 'favorites');

/** At the default preference the compact page fits the drawn three columns.
 * The user's card-size scale still changes density deliberately. */
export const homeSeeAllCardWidth = (surface: number, gutter: number, fallback: number, scale: number, phone: boolean): number =>
  Math.max(1, phone ? Math.floor((surface - 2 * gutter - 20) / 3 * scale) : Math.round(fallback * scale));
