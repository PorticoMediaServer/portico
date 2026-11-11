export {ServicesError, type Tokens} from '@core/portico-account.ts';

/** What "Signed-in devices" shows for this browser: its name and system, never more. */
export function thisDevice(agent = typeof navigator === 'undefined' ? '' : navigator.userAgent): {name: string; kind: string} {
  const browser = /Edg\//.test(agent) ? 'Edge' : /Firefox\//.test(agent) ? 'Firefox' : /Chrome\//.test(agent) ? 'Chrome' : /Safari\//.test(agent) ? 'Safari' : 'Web browser';
  const system = /iPhone|iPad/.test(agent) ? 'iOS' : /Android/.test(agent) ? 'Android' : /Mac OS X/.test(agent) ? 'macOS' : /Windows/.test(agent) ? 'Windows' : /Linux|CrOS/.test(agent) ? 'Linux' : '';
  return {name: system ? `${browser}, ${system}` : browser, kind: 'web'};
}
