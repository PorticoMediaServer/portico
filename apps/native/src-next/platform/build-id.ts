/** The source this bundle was built from (O-159): a short commit stamped at bundle time by
 * babel.config.js, or "dev" where nothing stamped it (Vega, tests). Shown in Settings › About. */
declare const __PORTICO_BUILD_ID__: string | undefined;
export const BUILD_ID: string = typeof __PORTICO_BUILD_ID__ === 'string' && __PORTICO_BUILD_ID__ ? __PORTICO_BUILD_ID__ : 'dev';
