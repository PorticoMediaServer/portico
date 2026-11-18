/** Personal token operations keep the original scope and GET default. */
type RequestOptions = Readonly<{headers?: Readonly<Record<string, string>>}>;
export function ownerAccessTokenApi(scope: {request<T>(path: string, method: string, body?: unknown, signal?: AbortSignal, options?: RequestOptions): Promise<T>}) {
  return {request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal, options?: RequestOptions): Promise<T> => scope.request<T>(path, method ?? 'GET', body, signal, options)};
}
