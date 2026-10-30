import {IdentityClient, type IdentityUpload} from '@core/identity-client.ts';
import {MemberProfilesClient} from '@core/member-profiles.ts';
import type {RatingSystem} from '@core/identity-restrictions.ts';
import type {SettingsPeopleApi} from '@core/settings-people.ts';
import type {ShellArtwork} from '../../shell/artwork';

export type PeopleMemberProfilesApi = Readonly<{forPerson: (accountId: string) => MemberProfilesClient; ratingSystems: (signal?: AbortSignal) => Promise<readonly RatingSystem[]>;
  picture: ShellArtwork['memberProfileAvatar']}>;

/** Another person's profiles, through People's own authority: a retired
 * workspace never sends or publishes a profile command. */
export function createPeopleMemberProfilesApi(api: SettingsPeopleApi, isCurrent: () => boolean, upload: IdentityUpload, artwork: Pick<ShellArtwork, 'memberProfileAvatar'>): PeopleMemberProfilesApi {
  const captured: SettingsPeopleApi = {request: async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
    if (!isCurrent()) throw new DOMException('People authority retired', 'AbortError');
    signal?.throwIfAborted();
    const value = await api.request<T>(path, method, body, signal);
    if (!isCurrent()) throw new DOMException('People authority retired', 'AbortError');
    signal?.throwIfAborted(); return value;
  }};
  const capturedUpload: IdentityUpload = async (path, body, contentType) => {
    if (!isCurrent()) throw new DOMException('People authority retired', 'AbortError');
    const value = await upload(path, body, contentType);
    if (!isCurrent()) throw new DOMException('People authority retired', 'AbortError');
    return value;
  };
  const identity = new IdentityClient({request: captured.request});
  return Object.freeze({forPerson: (accountId: string) => new MemberProfilesClient(captured, accountId, capturedUpload), ratingSystems: (signal?: AbortSignal) => identity.ratingSystems(signal),
    picture: (...args: Parameters<ShellArtwork['memberProfileAvatar']>) => isCurrent() ? artwork.memberProfileAvatar(...args) : undefined});
}
