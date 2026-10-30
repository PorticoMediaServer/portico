/**
 * Whether rows and pages glide to follow focus (`snap.tsx`). They do unless `portico.animations`
 * is false in `app.json`, which makes them jump at once (a switch for hardware that cannot keep up).
 */
const settings = require('../../app.json') as {portico?: {animations?: boolean}};
export const animations = settings.portico?.animations !== false;
