import type {ResetFileLocation} from '../../../../../packages/client-core/src/account-reset';

/** DESIGN.md's data-folder ruling supersedes the older illustrative G30 paths.
 * Since O-169 a server no longer publishes its operating system or data folder
 * before sign-in, so these are help for each system, never a substitute for
 * the server's path: the file is always reset-code.txt in its Keys folder. */
const systems = [
  {kind: 'linux', name: 'Linux', path: '/var/lib/portico/Keys/reset-code.txt', detail: {key: 'accounts.recovery.fileManual', path: '~/.local/share/portico/Keys/reset-code.txt'}},
  {kind: 'windows', name: 'Windows', path: '%LOCALAPPDATA%\\Portico\\Keys\\reset-code.txt', detail: {key: 'accounts.recovery.fileWindowsService', path: 'C:\\ProgramData\\Portico\\Keys\\reset-code.txt'}}, // lint-strings-allow file path, not copy
  {kind: 'macos', name: 'macOS', path: '~/Library/Application Support/Portico/Keys/reset-code.txt'},
  {kind: 'docker', name: 'Docker', path: '/config/Keys/reset-code.txt', detail: {key: 'accounts.recovery.fileDocker'}},
] as const;

export function ownerResetFilePaths(location?: Pick<ResetFileLocation, 'fileName'>) {
  return {fileName: location?.fileName ?? 'reset-code.txt', systems};
}
