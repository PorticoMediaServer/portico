/**
 * The Fire TV bridge's trace, in a TV browser. A development build (or a page opened with
 * `?trace`) writes each line to the console, where the TV's remote inspector shows it; a release
 * build says nothing. Values that could be credentials never reach a line (the bridge only traces
 * names, states and sizes).
 */
const enabled = (typeof __DEV__ !== 'undefined' && __DEV__) || /[?&]trace\b/.test(globalThis.location?.search ?? '');

export const debugConsole = false;

export function trace(text: string, problem = false): void {
  if (!enabled && !problem) return;
  (problem ? console.warn : console.debug)(`[portico] ${text}`);
}

export function traced<T extends object>(_name: string, module: T): T {
  return module;
}

export function DebugConsole(): null {
  return null;
}
