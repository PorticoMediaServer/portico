/**
 * The devices of a person the viewer manages (`GET /v1/admin/members/{id}/devices`), the list that person sees under
 * Signed-in devices. Read fresh each time People's Devices tab opens; it is never held, because signing one out changes it.
 * The route joins the typed contract with its next refresh, when this becomes a `callServer` read.
 */
export type MemberDevicesApi = Readonly<{request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}>;
export const memberDevicesPath = (accountId: string) => `/v1/admin/members/${encodeURIComponent(accountId)}/devices`;
export const fetchMemberDevices = (api: MemberDevicesApi, accountId: string, signal?: AbortSignal): Promise<unknown> =>
  api.request<unknown>(memberDevicesPath(accountId), 'GET', undefined, signal);
