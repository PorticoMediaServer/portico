/** Existing source-only TSX fixture transformer. */
export function transformTsx(url: string | URL): string;
/** A TypeScript module that reads Vite's import.meta.env needs the same transform. */
export function transformTs(url: string | URL): string;
export function usesBundlerEnvironment(url: string | URL): boolean;
