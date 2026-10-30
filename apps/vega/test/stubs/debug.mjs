export const debugConsole = false;
export const lines = [];
/** Kept so a failing test can print what the code said it was doing. */
export function trace(text) { lines.push(text); if (lines.length > 400) lines.shift(); }
export const traced = (_name, module) => module;
export const DebugConsole = () => null;
