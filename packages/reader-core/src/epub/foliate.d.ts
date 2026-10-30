declare module 'foliate-js/epub.js' { export const EPUB: new (loader: unknown) => unknown; }
declare module 'foliate-js/paginator.js' { export const Paginator: unknown; }
declare module 'foliate-js/epubcfi.js' {
  export function fromRange(range: Range): string;
  export function joinIndir(base: string, cfi: string): string;
}
