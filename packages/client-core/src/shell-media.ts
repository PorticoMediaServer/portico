import type {HttpLocalApi} from './index';

/** Resolve a shell media capability through its captured API and origin fences. */
export function resolveShellMediaUrl(api: Pick<HttpLocalApi, 'mediaUrl'>, path: string) {
  return api.mediaUrl(path);
}
