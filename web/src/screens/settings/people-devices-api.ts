import {readMemberDevices, signOutMemberDevice, signOutMemberDevices} from '@core/server-administration.ts';
import type {SettingsPeopleApi} from '@core/settings-people.ts';
import type {PeopleDevicesApi} from './people-devices-source';
/** A person's devices and signing them out, on the rebuilt server's member device routes. */
export function createPeopleDevicesApi(api: SettingsPeopleApi, isCurrent: () => boolean): PeopleDevicesApi {
  const captured: SettingsPeopleApi = {request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
    if (!isCurrent()) throw new DOMException('People authority retired', 'AbortError');
    signal?.throwIfAborted();
    const value = await api.request<T>(path, method, body, signal);
    if (!isCurrent()) throw new DOMException('People authority retired', 'AbortError');
    signal?.throwIfAborted(); return value;
  }};
  return {
    // The rebuilt server answers with the whole list: there is no second page.
    list: async (accountId, cursor, signal) => cursor ? Object.freeze({limit: 1, nextCursor: '', items: Object.freeze([])}) : readMemberDevices(captured, accountId, signal),
    signOut: (accountId, deviceId, signal) => deviceId === undefined ? signOutMemberDevices(captured, accountId, signal) : signOutMemberDevice(captured, accountId, deviceId, signal),
  };
}
