import type {ResetFileLocation} from '../../../../../packages/client-core/src/account-reset';

/** DESIGN.md's data-folder ruling supersedes the older illustrative G30 paths.
 * These are help for the other systems, never a substitute for a server's path. */
const systems = [
  {kind: 'linux', name: 'Linux', path: '/var/lib/portico/reset-code.txt', detail: {key: 'accounts.recovery.fileManual', path: '~/.local/share/portico/reset-code.txt'}},
  {kind: 'windows', name: 'Windows', path: '%LOCALAPPDATA%\\Portico\\reset-code.txt', detail: {key: 'accounts.recovery.fileWindowsService', path: 'C:\\ProgramData\\Portico\\reset-code.txt'}}, // lint-strings-allow file path, not copy
  {kind: 'macos', name: 'macOS', path: '~/Library/Application Support/Portico/reset-code.txt'},
  {kind: 'docker', name: 'Docker', path: '/config/reset-code.txt', detail: {key: 'accounts.recovery.fileDocker'}},
] as const;

export function ownerResetFilePaths(location?: ResetFileLocation) {
  const current = systems.find(system => system.kind === location?.systemKind) ?? systems[0];
  const separator = current.kind === 'windows' ? '\\' : '/';
  const path = location ? location.fileDirectory.replace(/[\\/]+$/, '') + separator + location.fileName : current.path;
  return {system: current.name, path, alternatives: systems.filter(system => system !== current)};
}
