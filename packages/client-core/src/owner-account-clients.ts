/** Personal token operations keep the original scope and GET default. */
export function ownerAccessTokenApi(scope: {request<T>(path: string, method: string, body?: unknown, signal?: AbortSignal): Promise<T>}) {
  return {request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => scope.request<T>(path, method ?? 'GET', body, signal)};
}
