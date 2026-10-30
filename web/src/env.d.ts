/// <reference types="vite/client" />
interface ImportMetaEnv {
  readonly VITE_HOSTED_WEB?: string;
  readonly VITE_ACCOUNT_WORKSPACE?: string;
  readonly VITE_HOSTED_URL?: string;
  readonly VITE_APP_VERSION?: string;
  /** O-159: the commit this web build came from, stamped at build time; shown in About. */
  readonly VITE_BUILD_ID?: string;
  readonly VITE_CAST_APPLICATION_ID?: string;
  readonly VITE_SERVER_URL?: string;
}
declare module 'virtual:portico-kit-families' {
  type LayoutInputs = Readonly<Record<'referenceHtml' | 'searchHtml' | 'tvHtml' | 'phoneHtml' | 'browseJson' | 'browseHtml' | 'readHtml' | 'phoneReadHtml', string>>;
  type BrowseInputs = Readonly<Record<'movieCapabilities' | 'musicCapabilities' | 'songResponse' | 'drawnListen' | 'drawnTv' | 'drawnBrowse' | 'drawnRead' | 'drawnPhone' | 'albumResponse', string>>;
  export function loadGalleryInputs(family: 'layout'): Promise<LayoutInputs>;
  export function loadGalleryInputs(family: 'browse'): Promise<BrowseInputs>;
  const loaders: readonly ((() => Promise<{default: import('./dev/gallery-registry').GalleryFamily}>) & {readonly familyId: string})[];
  export default loaders;
}
