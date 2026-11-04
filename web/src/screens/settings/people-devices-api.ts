import {accessListPath} from '@core/server-admin/panel-logic.ts';
import {parseAccessDevicePage, signOutMemberDevice, signOutMemberDevices} from '@core/server-administration.ts';
import type {SettingsPeopleApi} from '@core/settings-people.ts';
import type {PeopleDevicesApi} from './people-devices-source';

/** Reuse the shared canonical routes, strict access parser and sign-out commands.
 * The account filter stays on every bounded page, including continuations. */
export function createPeopleDevicesApi(api: SettingsPeopleApi, isCurrent: () => boolean): PeopleDevicesApi {
  const captured: SettingsPeopleApi = {request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
    if (!isCurrent()) throw new DOMException('People authority retired', 'AbortError');
    signal?.throwIfAborted();
    const value = await api.request<T>(path, method, body, signal);
    if (!isCurrent()) throw new DOMException('People authority retired', 'AbortError');
    signal?.throwIfAborted(); return value;
  }};
  return {
    list: async (accountId, cursor, signal) => parseAccessDevicePage(await captured.request(accessListPath('devices', cursor) + '&account=' + encodeURIComponent(accountId), 'GET', undefined, signal)),
    signOut: (accountId, deviceId, signal) => deviceId === undefined ? signOutMemberDevices(captured, accountId, signal) : signOutMemberDevice(captured, accountId, deviceId, signal),
  };
}
