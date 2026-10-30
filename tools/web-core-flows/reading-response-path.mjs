// Keep reading-session identities out of retained diagnostic reports.
export function readingResponsePath(pathname) {
  if (/^\/v1\/reading\/sessions\/[^/]+\/source$/.test(pathname)) {
    return '/v1/reading/sessions/:session/source';
  }
  if (/^\/v1\/reading\/sessions\/[^/]+\/pages\/[^/]+$/.test(pathname)) {
    return '/v1/reading/sessions/:session/pages/:page';
  }
  return null;
}
