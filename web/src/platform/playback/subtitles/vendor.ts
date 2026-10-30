/**
 * Where the third-party subtitle files are served (`vendoredFiles()` in `web/scripts/config.mjs`
 * serves `third_party/libass-wasm/files` and `third_party/subtitle-fonts/fonts` under these names).
 * They are fetched only when a styled subtitle is selected.
 */
const base = (): string => {
  const root = (import.meta as unknown as {env?: {BASE_URL?: string}}).env?.BASE_URL ?? '/';
  return new URL(root, typeof document === 'undefined' ? 'http://localhost/' : document.baseURI).href;
};
const libass = 'vendor/libass-wasm-4.2.4/', fonts = 'vendor/subtitle-fonts-1/';

export const libassFiles = Object.freeze({
  main: () => base() + libass + 'subtitles-octopus.js',
  worker: () => base() + libass + 'subtitles-octopus-worker.js',
  defaultFont: () => base() + libass + 'default.woff2',
});
export const fallbackFontUrl = (file: string) => base() + fonts + file;
