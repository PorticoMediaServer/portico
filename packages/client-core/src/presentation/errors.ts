import {playbackUserMessage} from '../playback-messages.ts';
import {RouteError} from '../route-identity.ts';
import {defaultI18n, type I18n, type MessageId} from '../../../i18n/src/index.ts';

/**
 * The one error-presentation layer (X-04, PC-VISUAL §15.4–15.6, A11Y-16.3).
 *
 * Errors are mapped by stable **code**, HTTP status and type, never by their
 * message: core, server and browser text is diagnostic evidence and is never
 * shown. Every result says what failed (title), what's safe and what happens
 * next (body), and which recovery action is truthful (`action`).
 *
 * Usage: `const p = presentError(error, 'library'); <StateView title={p.title} body={p.body} action={p.action === 'try-again' ? …}>`.
 * `p.silent` is true for cancellations (an aborted request): render nothing.
 * Copy comes from the Product Language catalogue (`@portico/i18n`, en-US by
 * default; pass `options.i18n` for the viewer's locale).
 */
export type ErrorContext =
  | 'home' | 'library' | 'detail' | 'show' | 'person' | 'search' | 'saved' | 'playlist' | 'collection'
  | 'downloads' | 'live' | 'together' | 'notifications' | 'account' | 'profiles' | 'devices' | 'settings'
  | 'preferences' | 'server-console' | 'sign-in' | 'playback' | 'feedback' | 'generic'
  /** Hosted-backed surfaces: Portico Account pages, claiming a server, account links (verify email, invites). */
  | 'portico-account' | 'claim' | 'link';

export type ErrorOperation = 'load' | 'save' | 'action';

export type ErrorCategory =
  | 'cancelled' | 'offline' | 'unreachable' | 'timeout' | 'busy' | 'unavailable' | 'authentication' | 'permission'
  | 'not-found' | 'changed' | 'rate-limited' | 'unsupported-version' | 'invalid-response' | 'invalid-input'
  | 'storage-full' | 'playback' | 'unknown';

/** The recovery a person can truthfully take. `automatic` = Portico retries by itself; show no button (§15.5). */
export type ErrorAction = 'try-again' | 'refresh' | 'sign-in' | 'update' | 'automatic' | 'none';

export type PresentedError = Readonly<{
  category: ErrorCategory;
  title: string;
  body: string;
  action: ErrorAction;
  /** Button label for `action`, or undefined when there is no button. */
  actionLabel?: string;
  /** Catalogue ID of the body copy. */
  messageId: string;
  /** True when nothing should be shown (the person cancelled or navigated away). */
  silent: boolean;
  /** Seconds to wait before retrying (rate limits), when the server said. */
  retryAfterSeconds?: number;
  /** Diagnostic only: the stable code. Never render it in consumer UI. */
  code?: string;
}>;

export type PresentOptions = {
  operation?: ErrorOperation;
  /** Portico is already retrying automatically: say so and offer no Try again. */
  retrying?: boolean;
  /** `navigator.onLine` / reachability on this device, when known. */
  deviceOnline?: boolean;
  /** Owner-only surfaces (server console) may show the code in a details disclosure. */
  audience?: 'consumer' | 'owner';
  /** The viewer's formatter set; defaults to en-US. */
  i18n?: I18n;
  /**
   * Which service answered (or didn't). `portico-account` is the Portico Account service (Hosted):
   * reachability copy then names the account, never "your server". The `portico-account`, `claim`
   * and `link` contexts imply it; pass it for other contexts backed by Hosted (sign-in with a Portico Account).
   */
  service?: 'server' | 'portico-account';
};

const ACCOUNT_CONTEXTS: ReadonlySet<ErrorContext> = new Set(['portico-account', 'claim', 'link']);
const accountBodies: Partial<Record<ErrorCategory, MessageId>> = {
  unreachable: 'error.account.unreachable', timeout: 'error.account.timeout', busy: 'error.account.busy', unavailable: 'error.account.unavailable',
};

type Body = {id: MessageId; action: ErrorAction};

const bodies: Record<Exclude<ErrorCategory, 'cancelled' | 'playback' | 'unknown'>, Body> = {
  offline: {id: 'error.offline', action: 'try-again'},
  unreachable: {id: 'error.unreachable', action: 'try-again'},
  timeout: {id: 'error.timeout', action: 'try-again'},
  busy: {id: 'error.busy', action: 'try-again'},
  unavailable: {id: 'error.unavailable', action: 'try-again'},
  authentication: {id: 'error.authentication', action: 'sign-in'},
  permission: {id: 'error.permission', action: 'none'},
  'not-found': {id: 'error.notFound', action: 'none'},
  changed: {id: 'error.changed', action: 'refresh'},
  'rate-limited': {id: 'error.rateLimited', action: 'try-again'},
  'unsupported-version': {id: 'error.unsupportedVersion', action: 'update'},
  'invalid-response': {id: 'error.invalidResponse', action: 'try-again'},
  'invalid-input': {id: 'error.invalidInput', action: 'none'},
  'storage-full': {id: 'error.storageFull', action: 'try-again'},
};
const unknownBody: Body = {id: 'error.unknown', action: 'try-again'};

const actionLabelIds: Partial<Record<ErrorAction, MessageId>> = {'try-again': 'action.tryAgain', refresh: 'action.refresh', 'sign-in': 'action.signIn', update: 'action.update'};

type Facts = {code: string; status?: number; retryable?: boolean; retryAfterSeconds?: number; name?: string; isTypeError: boolean};

function factsOf(error: unknown): Facts {
  if (typeof error === 'string') return {code: error, isTypeError: false};
  if (!error || typeof error !== 'object') return {code: '', isTypeError: false};
  const e = error as {code?: unknown; status?: unknown; retryable?: unknown; retryAfterSeconds?: unknown; name?: unknown};
  return {
    code: typeof e.code === 'string' ? e.code : '',
    status: typeof e.status === 'number' ? e.status : undefined,
    retryable: typeof e.retryable === 'boolean' ? e.retryable : undefined,
    retryAfterSeconds: typeof e.retryAfterSeconds === 'number' && Number.isFinite(e.retryAfterSeconds) && e.retryAfterSeconds > 0 ? Math.ceil(e.retryAfterSeconds) : undefined,
    name: typeof e.name === 'string' ? e.name : undefined,
    isTypeError: error instanceof TypeError,
  };
}

const exact: Record<string, ErrorCategory> = {
  cancelled: 'cancelled', canceled: 'cancelled', aborted: 'cancelled',
  network: 'unreachable', request_failed: 'unreachable', server_unavailable: 'unreachable', route_unavailable: 'unreachable', console_unavailable: 'unreachable', delivery_http_failure: 'unreachable', remote_unavailable: 'unreachable',
  timeout: 'timeout', remote_stalled: 'timeout',
  unauthorized: 'authentication', authentication_required: 'authentication', session_expired: 'authentication', invalid_token: 'authentication', unauthenticated: 'authentication',
  forbidden: 'permission', permission_changed: 'permission', access_revoked: 'permission', owner_required: 'permission', remote_denied: 'permission', remote_sign_in_not_allowed: 'permission', outside_allowed_hours: 'permission',
  not_found: 'not-found', gone: 'not-found',
  rate_limited: 'rate-limited', too_many_requests: 'rate-limited',
  unsupported_version: 'unsupported-version', client_too_old: 'unsupported-version',
  storage_full: 'storage-full', insufficient_storage: 'storage-full',
  invalid_request: 'invalid-input', validation_failed: 'invalid-input', method_not_allowed: 'invalid-input',
  response_too_large: 'invalid-response', invalid_response: 'invalid-response', unreadable_response: 'invalid-response',
  artwork_pending: 'unavailable', artwork_gone: 'changed',
  reading_progress_pending: 'unavailable',
  factor_setup_required: 'permission',
  // o3/api-hygiene: a write without the revision it read answers 428 revision_required.
  revision_required: 'changed',
  route_stale: 'changed', refresh_required: 'changed', 'refresh-required': 'changed', stale: 'changed', identity_mismatch: 'changed', session_identity_mismatch: 'changed', stale_continuation: 'changed', wrong_server: 'changed',
  internal_error: 'unknown', unexpected: 'unknown', persistence_error: 'unknown',
};

function categorize(f: Facts, deviceOnline: boolean | undefined): ErrorCategory {
  if (f.name === 'AbortError' || f.code === 'abort') return 'cancelled';
  const byCode = exact[f.code];
  if (byCode) return byCode === 'unreachable' && deviceOnline === false ? 'offline' : byCode;
  const c = f.code;
  if (c) {
    if (/(^|_)(conflict|stale|scope_changed|changed|mismatch|revision)(_|$)/.test(c)) return 'changed';
    if (/(^|_)busy$|_capacity(_|$)|^server_busy$|_occupied$/.test(c)) return 'busy';
    if (/_not_found$/.test(c)) return 'not-found';
    if (/_invalid_input$|^invalid_(address|administration_input|request_id|selection)$/.test(c)) return 'invalid-input';
    if (/^invalid_|_invalid$|_manifest_invalid$/.test(c)) return 'invalid-response';
    if (/_unavailable$|^unavailable$|_unsupported$|^unsupported_/.test(c)) return 'unavailable';
    if (/_timeout$|_timed_out$/.test(c)) return 'timeout';
  }
  if (f.status === 401) return 'authentication';
  if (f.status === 403) return 'permission';
  if (f.status === 404 || f.status === 410) return 'not-found';
  if (f.status === 409 || f.status === 412 || f.status === 428) return 'changed';
  if (f.status === 426) return 'unsupported-version';
  if (f.status === 429) return 'rate-limited';
  if (f.status === 507) return 'storage-full';
  if (f.status === 408 || f.status === 504) return 'timeout';
  if (f.status === 503) return 'busy';
  if (f.status === 400 || f.status === 422) return 'invalid-input';
  // A dropped connection surfaces as a TypeError ("Failed to fetch", "Load failed"): never show that text.
  if (f.isTypeError) return deviceOnline === false ? 'offline' : 'unreachable';
  if (deviceOnline === false) return 'offline';
  return 'unknown';
}

/** Present any thrown value for a person. Never returns raw error text. */
/** Codes with their own sentence, whatever their category. */
const codeBodies: Record<string, MessageId> = {
  reading_progress_pending: 'error.readingProgressPending',
  factor_setup_required: 'error.factorSetupRequired',
  queue_too_large: 'error.queueTooLarge',
  // An online-only action (match search, artwork, lyrics or subtitle search) on a library whose
  // metadata source is local only: the server's reason, and nothing to retry.
  local_metadata_only: 'error.localMetadataOnly',
  // BE-API-08 registered errors: metadata, artwork and web availability, and unsupported actions.
  metadata_unavailable: 'error.metadataUnavailable',
  artwork_pending: 'error.artworkPending',
  artwork_gone: 'error.artworkGone',
  web_unavailable: 'error.webUnavailable',
  method_not_allowed: 'error.methodNotAllowed',
  // Add library with a language the server doesn't offer for the kind/agent (MU12): a definite
  // refusal with its own sentence, whatever the HTTP status category.
  invalid_metadata_language: 'web.libraries.error.metadataLanguage',
  // Keys, tokens and webhooks have a ceiling; a member's tokens can be turned off by the owner.
  credential_limit: 'error.credentialLimit',
  access_tokens_off: 'api.tokens.off',
  webhook_limit: 'error.webhookLimit',
  // What was typed to confirm is not what the server asked for (removing a library).
  confirmation_mismatch: 'error.confirmationMismatch',
  // Live TV sources: what is wrong with an address, a sign-in or a tuner, in a sentence of its own.
  invalid_channel_input: 'error.live.invalidInput',
  source_has_dependencies: 'error.live.hasDependencies',
  source_network_policy: 'error.live.networkPolicy',
  source_credentials_required: 'error.live.credentials',
  source_unavailable: 'error.live.unavailable',
  channel_revision_conflict: 'error.live.previewExpired',
  source_capacity_unavailable: 'error.live.tunersBusy',
  // A picture chosen for a channel's logo that is too large to send.
  image_too_large: 'server.channels.logoTooLarge',
  // Playback that ended before a control sent to it arrived (the owner's Pause, a remote's Stop).
  session_ended: 'error.sessionEnded',
};
/** Codes whose sentence says what to do instead, so the notice offers no Try again. */
const noActionCodes = new Set(['local_metadata_only', 'credential_limit', 'access_tokens_off', 'webhook_limit', 'session_ended', 'image_too_large', 'reading_progress_pending', 'factor_setup_required']);

export function presentError(error: unknown, context: ErrorContext = 'generic', options: PresentOptions = {}): PresentedError {
  const i18n = options.i18n ?? defaultI18n;
  const f = factsOf(error);
  const operation = options.operation ?? 'load';
  const title = i18n.t(`error.title.${operation}` as MessageId, {subject: context});
  const category = categorize(f, options.deviceOnline);
  const label = (action: ErrorAction) => (actionLabelIds[action] ? i18n.t(actionLabelIds[action]!) : undefined);
  if (category === 'cancelled') return Object.freeze({category, title, body: '', action: 'none', messageId: 'error.cancelled', silent: true, code: f.code || undefined});
  if (f.code === 'transport_unavailable') return Object.freeze({category: 'unavailable', title, body: i18n.t('error.transportUnavailable'), action: 'none', messageId: 'error.transportUnavailable', silent: false, code: f.code});
  // Only the local proof refusal establishes this claim. Do not unwrap a failed route
  // race or trust an API payload with the same code, and never turn it into a network retry.
  if (error instanceof RouteError && f.code === 'clocks_disagree') {
    return Object.freeze({category: 'unknown', title, body: i18n.t('error.clocksDisagree'), action: 'try-again', actionLabel: label('try-again'), messageId: 'error.clocksDisagree', silent: false, code: f.code});
  }
  if (context === 'playback') {
    // Playback keeps its own reviewed mapping (codes → copy, allowlisted safe server strings).
    const body = playbackUserMessage(error);
    return Object.freeze({category: 'playback', title, body, action: 'try-again', actionLabel: label('try-again'), messageId: 'error.playback', silent: false, code: f.code || undefined});
  }
  const base = category === 'unknown' || category === 'playback' ? unknownBody : bodies[category];
  let messageId: MessageId = codeBodies[f.code] ?? base.id;
  let action: ErrorAction = base.action;
  let values: Record<string, number> | undefined;
  const account = (options.service ?? (ACCOUNT_CONTEXTS.has(context) ? 'portico-account' : 'server')) === 'portico-account';
  if (account && accountBodies[category]) messageId = accountBodies[category]!;
  if (category === 'rate-limited' && f.retryAfterSeconds) { messageId = 'error.rateLimitedSeconds'; values = {seconds: f.retryAfterSeconds}; }
  if (options.retrying && (category === 'unreachable' || category === 'offline' || category === 'timeout' || category === 'busy')) {
    messageId = category === 'offline' ? 'error.offlineRetrying' : account ? 'error.account.unreachableRetrying' : 'error.unreachableRetrying';
    action = 'automatic';
  }
  if (operation !== 'load' && category === 'invalid-response') { messageId = 'error.unconfirmedChange'; action = 'refresh'; }
  if (noActionCodes.has(f.code)) action = 'none';
  return Object.freeze({
    category, title, body: i18n.t(messageId, values), action,
    actionLabel: label(action),
    messageId,
    silent: false,
    retryAfterSeconds: f.retryAfterSeconds,
    code: f.code || (f.status ? `http_${f.status}` : undefined),
  });
}

/** Shorthand for an inline notice: the body only (use when the surrounding view already names the task). */
export function errorMessage(error: unknown, context: ErrorContext = 'generic', options?: PresentOptions): string {
  const p = presentError(error, context, options);
  return p.silent ? '' : p.body;
}
