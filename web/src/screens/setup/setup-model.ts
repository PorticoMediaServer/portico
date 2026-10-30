import {defaultI18n, type MessageId} from '@i18n';
import {passwordStrength} from '@core/password-strength.ts';
import {addressSetupFailure, notLocalSourceFailure, setupInProgressFailure} from '../../platform/setup-failure.ts';
import {presentError} from '@core/presentation/errors.ts';
import {libraryKindDescription, libraryKindLabel, libraryKindMessages} from '../settings/libraries/model';

export type SetupMode = 'hosted' | 'local';
export type SetupLibraryKind = 'movie' | 'tv' | 'anime' | 'music' | 'audiobook' | 'book' | 'comic';

type SetupKindIcon = 'i-film' | 'i-tvlib' | 'i-category' | 'i-music' | 'i-headph' | 'i-sbook' | 'i-book';

/** The seven drawn library kinds (owner-libraries#13), in frame order, with the
 * frame's own name and one-line description for each. */
export const setupLibraryKinds: readonly Readonly<{id: SetupLibraryKind; label: MessageId; description: MessageId; icon: SetupKindIcon}>[] = Object.freeze([
  {id: 'movie', ...libraryKindMessages.movie, icon: 'i-film'},
  {id: 'tv', ...libraryKindMessages.tv, icon: 'i-tvlib'},
  {id: 'anime', ...libraryKindMessages.anime, icon: 'i-category'},
  {id: 'music', ...libraryKindMessages.music, icon: 'i-music'},
  {id: 'audiobook', ...libraryKindMessages.audiobook, icon: 'i-headph'},
  {id: 'book', ...libraryKindMessages.book, icon: 'i-sbook'},
  {id: 'comic', ...libraryKindMessages.comic, icon: 'i-book'},
]);

export {libraryKindDescription, libraryKindLabel};

export function defaultLibraryName(kind: SetupLibraryKind): string {
  return libraryKindLabel(kind);
}

/** A server left named "Portico" is indistinguishable from every other one; suggest the host. */
export function defaultSetupServerName(current: string, hostname: string): string {
  if (current && current !== 'Portico') return current;
  if (!hostname || /^(localhost|\d{1,3}(\.\d{1,3}){3}|\[.*\])$/.test(hostname)) return defaultI18n.t('web.setup.defaultServerName');
  return hostname.replace(/\.(local|lan|home)$/i, '').replace(/[-_]+/g, ' ');
}

/** The drawn local owner: 3 to 64 characters, no spaces or @. */
export function validLocalUsername(value: string): boolean {
  return /^[^\s@]{3,64}$/.test(value.trim());
}

export function setupNameValid(value: string): boolean {
  const name = value.trim();
  return name.length > 0 && name.length <= 80;
}

export function localPasswordValid(password: string, confirm: string): boolean {
  return passwordStrength(password).acceptable && password === confirm;
}

/** A setup that cannot be authorized at this address answers 401/403; it is
 * never "sign in again" because no account exists yet. */
export function setupErrorMessage(error: unknown, now = Date.now()): string {
  // Only the browser-start refusal has the drawn "can't start here" words; any
  // other auth failure uses the ordinary error body.
  if (notLocalSourceFailure(error)) return defaultI18n.t('web.setup.notLocalSource');
  if (addressSetupFailure(error)) return defaultI18n.t('web.setup.notOnNetwork');
  // Another browser's attempt is still live; say when this one can start over, in local time.
  if (setupInProgressFailure(error)) {
    const seconds = (error as {retryAfterSeconds?: unknown}).retryAfterSeconds;
    if (typeof seconds !== 'number' || !Number.isFinite(seconds) || seconds <= 0) return defaultI18n.t('web.setup.inProgressNoTime');
    return defaultI18n.t('web.setup.inProgress', {time: new Date(now + Math.ceil(seconds) * 1000).toLocaleTimeString(undefined, {hour: 'numeric', minute: '2-digit'})});
  }
  // A slow certificate can still be finishing after the Portico Account approved the server.
  if ((error as {code?: unknown} | null)?.code === 'setup_installing') return defaultI18n.t('web.setup.stillInstalling');
  return presentError(error, 'sign-in', {operation: 'action', i18n: defaultI18n}).body;
}

export type SetupStep = Readonly<{id: 'server' | 'account' | 'library'; state: 'done' | 'current' | 'next'; title: string; body: string}>;

/** Steps follow the drawn two-step local setup and three-step Portico setup. */
export function setupSteps(input: Readonly<{serverName: string; mode: SetupMode; phase: 'form' | 'library'; local: boolean}>): readonly SetupStep[] {
  const server = {id: 'server' as const, state: input.phase === 'library' ? 'done' as const : 'current' as const,
    title: defaultI18n.t('web.setup.stepServer'),
    body: input.phase === 'library' ? input.serverName : input.local ? defaultI18n.t('web.setup.stepServerLocalBody') : defaultI18n.t('web.setup.stepServerBody')};
  if (input.mode === 'local') return [server, {id: 'library', state: input.phase === 'library' ? 'current' : 'next',
    title: defaultI18n.t('web.setup.stepLibrary'), body: input.phase === 'library' ? defaultI18n.t('web.setup.stepLibraryLater') : defaultI18n.t('web.setup.stepLibraryBody')}];
  return [server,
    {id: 'account', state: input.phase === 'library' ? 'done' : 'next', title: defaultI18n.t('web.setup.stepAccount'),
      body: input.phase === 'library' ? defaultI18n.t('web.setup.ownsServer') : defaultI18n.t('web.setup.stepAccountBody')},
    {id: 'library', state: input.phase === 'library' ? 'current' : 'next', title: defaultI18n.t('web.setup.stepLibrary'),
      body: input.phase === 'library' ? defaultI18n.t('web.setup.stepLibraryLater') : defaultI18n.t('web.setup.stepLibraryBody')}];
}
