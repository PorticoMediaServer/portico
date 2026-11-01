/** Home registers each television card as its own window. A 1920-wide TV shows up to four
 * poster rows of about nine cards at once, plus five spotlights and the overscan either side,
 * so Home needs more than the model's default 32 (walk, 1 Nov: three poster rows on screen
 * overflowed it). Native's window planner budgets against this same number. */
export const homeSelectionWindowLimit = 64;
