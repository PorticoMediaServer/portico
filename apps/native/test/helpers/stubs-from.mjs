import {posix} from 'node:path';

/** Stubs a fixture wrote relative to one src-next folder, keyed for a module that moved to another (a screen that became
 * a page). Bare module names keep their keys; every relative key names the same file from its new folder. */
export function stubsFrom(fromDir, toDir, stubs) {
  return Object.fromEntries(Object.entries(stubs).map(([key, value]) => {
    if (!key.startsWith('.')) return [key, value];
    const path = posix.relative(posix.join('src-next', toDir), posix.join('src-next', fromDir, key));
    return [path.startsWith('.') ? path : `./${path}`, value];
  }));
}
