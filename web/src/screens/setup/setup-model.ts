import {defaultI18n, type MessageId} from '@i18n';
import {passwordStrength} from '@core/password-strength.ts';
import type {SetupError, SetupOutcome, SetupView} from '@core/setup-machine.ts';
import {NeedsNewerAppError} from '@core/needs-newer-app.ts';
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

/** Which drawn failure a failed step is: 1d-3 when this server can't reach Portico Services, 1d-2 when setup can't
 * start from this address (the browser start refused with 401/403), otherwise the ordinary error. */
export type SetupFailureKind = 'services' | 'address' | 'other';
export function setupFailureKind(view: Extract<SetupView, {kind: 'failed'}>): SetupFailureKind {
  if (view.error.code === 'claim_unavailable') return 'services';
  const step = view.retry.effects[0]?.kind;
  if (step === 'start' && (view.error.code === 'unauthorized' || view.error.code === 'forbidden')) return 'address';
  return 'other';
}
/** The words for a failed step's error: the server's code, never a guess. */
export function setupErrorMessage(error: SetupError): string {
  if (error.code === 'setup_source_not_local') return defaultI18n.t('web.setup.notLocalSource');
  if (error.code === 'claim_unavailable') return defaultI18n.t('web.setup.servicesUnavailableBody');
  if (error.code === 'setup_installing') return defaultI18n.t('web.setup.stillInstalling');
  return presentError(error, 'sign-in', {operation: 'action', i18n: defaultI18n}).body;
}
/** Another browser holds setup: say when this one can start over, in local time. */
export function setupBusyMessage(retryAt: number | undefined): string {
  if (retryAt === undefined || !Number.isFinite(retryAt)) return defaultI18n.t('web.setup.inProgressNoTime');
  return defaultI18n.t('web.setup.inProgress', {time: new Date(retryAt).toLocaleTimeString(undefined, {hour: 'numeric', minute: '2-digit'})});
}
/** How an attempt ended, in the existing words. */
export function setupEndedMessage(outcome: SetupOutcome, server: string): string {
  if (outcome === 'denied') return defaultI18n.t('web.claim.denied', {server});
  if (outcome === 'expired') return defaultI18n.t('web.setup.earlierExpired');
  return defaultI18n.t('web.claim.expired');
}
/** A phase from a newer server. */
export const setupNewerMessage = () => presentError(new NeedsNewerAppError('setup'), 'sign-in', {operation: 'action', i18n: defaultI18n}).body;
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
