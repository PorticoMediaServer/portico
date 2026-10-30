/** Nonsecret name published by Hosted only after its real Origin/cookie policy succeeds. */
export function browserCsrfCookieName(value:string|null):string|undefined {
  return value&&/^(?:__Host-portico_refresh|portico_refresh_dev)(?:_[a-f0-9]{32})?_csrf$/.test(value)?value:undefined;
}

export const browserCsrfShape=/^[A-Za-z0-9_-]{43}$/;

/** Refuse ambiguity, including a valid token next to a malformed duplicate. */
export function exactBrowserCsrfCookie(cookies:string,name:string):string|undefined {
  const matches=cookies.split(';').map(part=>part.trim()).filter(part=>part.split('=',1)[0]===name);
  if(matches.length!==1)return;
  const token=matches[0].slice(name.length+1);
  return matches[0].startsWith(name+'=')&&browserCsrfShape.test(token)?token:undefined;
}
