import {defaultI18n} from '../../../i18n/src/index.ts';
import {NeedsNewerAppError} from '../needs-newer-app.ts';
import type {SetupError, SetupOutcome, SetupView} from '../setup-machine.ts';
import {presentError} from './errors.ts';

/** First-run setup's words for its states, shared by the web page and the app. */

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
  if (outcome === 'expired') return defaultI18n.t('web.setup.earlierExpired');
  return defaultI18n.t('web.claim.expired');
}
/** A phase from a newer server. */
export const setupNewerMessage = () => presentError(new NeedsNewerAppError('setup'), 'sign-in', {operation: 'action', i18n: defaultI18n}).body;
