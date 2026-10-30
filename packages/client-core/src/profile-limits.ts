/** Product creation policy and separate defensive legacy-read bound. */
export const ACCOUNT_PROFILE_LIMIT = 8;
export const ACCOUNT_PROFILE_READ_LIMIT = 100;

/** The server's contentRatings are the only picker options; no country or kids table. */
export function highestRatingChoices(contentRatings:readonly string[]):readonly string[]{
 return Object.freeze([...new Set(contentRatings.filter(v=>typeof v==='string'&&v.length>0&&v.length<=120&&!/[\p{Cc}\p{Cf}]/u.test(v)))]);
}
