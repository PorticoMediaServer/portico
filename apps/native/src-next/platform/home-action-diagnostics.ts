import {
  routePattern,
  type RouteId,
} from '../../../../packages/contracts/src/routes';
import type {RouteProbeFailure} from '../../../../packages/client-core/src/route-connection';

// Only fixed protocol names cross this boundary. Dynamic route parameters, query
// strings, native request IDs and request/response payloads never enter a record.
const routeRules = [
  ['GET', 'get_home'],
  ['GET', 'get_home_row'],
  ['GET', 'get_me'],
  ['GET', 'get_capabilities'],
  ['POST', 'prove_network_identity'],
  ['GET', 'get_playback_options'],
  ['PUT', 'put_device_capabilities'],
  ['GET', 'get_listening_preferences'],
  ['POST', 'create_queue'],
  ['GET', 'get_queue'],
  ['GET', 'get_queue_window'],
  ['GET', 'list_queue_entries'],
  ['POST', 'prepare_queue_next'],
  ['POST', 'commit_queue_next'],
  ['POST', 'advance_queue'],
  ['POST', 'start_playback_session'],
  ['GET', 'get_playback_session'],
  ['PATCH', 'change_playback_session'],
  ['DELETE', 'stop_playback_session'],
] as const satisfies readonly (readonly [string, RouteId])[];
type SafeRoute = (typeof routeRules)[number][1];
const compiled = routeRules.map(([method, route]) => ({
  method,
  route,
  pattern: new RegExp(
    '^' +
      routePattern(route)
        .split(/(\{[^}]+\})/)
        .map(part =>
          part.startsWith('{')
            ? '[^/?#]+'
            : part.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'),
        )
        .join('') +
      '$',
  ),
}));
const codes = new Set([
  'unoffered_home_playback',
  'native_home_authority_required',
  'stale_home_context',
  'home-visible-registration-limit',
  'invalid_response',
  'invalid_home',
  'invalid_scope',
  'invalid_request',
  'request_failed',
  'network_error',
  'network_unavailable',
  'offline',
  'timeout',
  'cancelled',
  'unauthorized',
  'forbidden',
  'access_refused',
  'identity_mismatch',
  'route_unavailable',
  'certificate_unverified',
  'certificate_untrusted',
  'identity_code_needed',
  'invalid_identity',
  'invalid_signature',
  'invalid_address',
  'clocks_disagree',
  'route_stale',
  'random_unavailable',
  'not_found',
  'unavailable',
  'unsupported',
  'unsupported_media',
  'no_playable_version',
  'capabilities_required',
  'revision_required',
  'stale_revision',
  'conflict',
  'session_expired',
  'lease_expired',
  'stale_selection_context',
  'invalid_playback_target',
  'artwork_pending',
  'rate_limited',
  'server_busy',
]);
const classes = new Set([
  'Error',
  'TypeError',
  'ReferenceError',
  'RangeError',
  'SyntaxError',
  'AbortError',
  'ApiError',
  'PlaybackApiError',
  'RouteError',
  'ContractError',
  'RequestTimeoutError',
]);
// Keep probe observations narrower than generic error reports: no status, stack or message.
const probeCodes = new Set<RouteProbeFailure['code']>([
  'invalid_address', 'invalid_identity', 'invalid_response', 'identity_mismatch', 'invalid_signature', 'invalid_routes',
  'route_stale', 'clocks_disagree', 'insecure_context', 'authentication_required', 'access_denied', 'rate_limited',
  'route_unavailable', 'certificate_unverified', 'certificate_untrusted', 'cancelled', 'other',
]);
const probeNames = new Set<RouteProbeFailure['name']>(['RouteError', 'Error', 'TypeError', 'SyntaxError', 'ReferenceError', 'RangeError', 'AbortError', 'unknown']);
const origins = new Set([
  'home-card',
  'home-assembly',
  'assembly-error',
  'restore-error',
  'route-memory',
  'route-probe',
  'home-registration',
  'artwork-error',
  'native-http',
  'app-render',
  'library-menu',
  'shell-lifetime',
  'boundary-cancelled',
]);
const shellLifetimeEvents = [
  'root-begin', 'root-restart', 'root-retire', 'root-runtime-retired', 'root-selection-changed', 'root-selection-read-failed',
  'root-account-fence', 'root-account-settled', 'root-renewal', 'root-authority-ended',
  'bootstrap-begin', 'bootstrap-dispose', 'bootstrap-owner-abort', 'bootstrap-authority-ended', 'bootstrap-role-changed',
  'bootstrap-accessor-retired', 'bootstrap-current-invalid', 'bootstrap-session-ended',
  'boundary-render-error', 'boundary-caught', 'boundary-retry',
] as const;
export type ShellLifetimeEvent = typeof shellLifetimeEvents[number];
const lifetimeEvents = new Set<string>(shellLifetimeEvents);
type ErrorOrigin = 'assembly-error' | 'restore-error' | 'route-memory' | 'home-registration' | 'artwork-error' | 'app-render';
type ActionOrigin = 'home-card' | 'home-assembly';
type Operation = 'primary-play' | 'menu';
type Stage = 'enter' | 'guard-refused' | 'unoffered' | 'dispatch';
type MenuStage = 'bindings' | 'attached' | 'screen' | 'producer' | 'produced' | 'element';
const menuStages = new Set(['bindings', 'attached', 'screen', 'producer', 'produced', 'element']);
const operations = new Set(['primary-play', 'menu']),
  stages = new Set(['enter', 'guard-refused', 'unoffered', 'dispatch']);
export const HOME_DIAGNOSTIC_LIMIT = 256,
  HOME_DIAGNOSTIC_ORIGIN_LIMIT = 64;
export const HOME_DIAGNOSTIC_PREFIX = '[home-play-r25] ';
type Ticket = Readonly<{request: number; route: SafeRoute}>;
type SafeRecord = {
  operation: string;
  stage?: Stage;
  request?: number;
  route?: SafeRoute;
  status?: number;
  elapsedMs?: number;
  routeSource?: 'none' | 'local' | 'account';
  signedCurrent?: boolean;
  signedPrevious?: boolean;
  pairedCount?: number;
  lastGood?: boolean;
  code?: string;
  class?: string;
  frames?: readonly (readonly [number, number])[];
  component?: string;
  componentFrames?: readonly (readonly [number, number])[];
  runtimeRef?: number;
  ownerRef?: number;
  parentRef?: number;
  lifetimeOrder?: number;
  menuRef?: number;
  own?: boolean;
  enumerable?: boolean;
  callable?: boolean;
};
const statusOf = (value: unknown): number | undefined =>
  typeof value === 'number' &&
  Number.isInteger(value) &&
  value >= 100 &&
  value <= 599
    ? value
    : undefined;
function safeFrames(stack: unknown, skipMessage: boolean): [number, number][] {
  const frames: [number, number][] = [];
  if (typeof stack !== 'string') return frames;
  for (const line of stack.slice(0, 8000).split('\n').slice(skipMessage ? 1 : 0, 17)) {
    // Keep coordinates only, never a function/component name or source address.
    const match = /[:@](\d{1,7}):(\d{1,9})\)?$/.exec(line.trim());
    if (match) frames.push([Number(match[1]), Number(match[2])]);
    if (frames.length === 6) break;
  }
  return frames;
}
function safeError(
  error: unknown,
): Pick<SafeRecord, 'code' | 'class' | 'status' | 'frames'> {
  try {
    if (!error || typeof error !== 'object')
      return {code: 'other', class: 'unknown', frames: []};
    const value = error as {
      code?: unknown;
      name?: unknown;
      status?: unknown;
      stack?: unknown;
    };
    const code = value.code,
      name = value.name,
      stack = value.stack;
    const frames = safeFrames(stack, true);
    return {
      code: typeof code === 'string' && codes.has(code) ? code : 'other',
      class: typeof name === 'string' && classes.has(name) ? name : 'unknown',
      status: statusOf(value.status),
      frames,
    };
  } catch {
    return {code: 'other', class: 'unknown', frames: []};
  }
}

/** Passive, bounded receipts through the console path already installed by
 * index/error-capture (ErrorJournal + the original production native console).
 * A test-only factory argument controls the sink; there is no global/native hook. */
export function createHomeActionDiagnostics(
  sink: (line: string) => void = line => console.warn(line),
) {
  let sequence = 0,
    requestSequence = 0;
  let lifetimeOrder = 0, pendingLifetime = 0;
  const counts = new Map<string, number>(),
    tickets = new WeakSet<Ticket>();
  const references = new WeakMap<object, number>();
  let referenceSequence = 0;
  const reference = (value: object) => {
    let id = references.get(value);
    if (id === undefined) {id = ++referenceSequence; references.set(value, id);}
    return id;
  };
  const started = new WeakMap<Ticket, number>();
  const elapsed = (ticket: Ticket) => Math.max(0, Math.min(60_000, Date.now() - (started.get(ticket) ?? Date.now())));
  const write = (origin: string, record: SafeRecord) => {
    try {
      const count = counts.get(origin) ?? 0;
      if (
        !origins.has(origin) ||
        // A swallowed cancellation is always reported, however busy the session was; its own cap still holds.
        (origin !== 'boundary-cancelled' && sequence + pendingLifetime >= HOME_DIAGNOSTIC_LIMIT) ||
        count + (origin === 'shell-lifetime' ? pendingLifetime : 0) >= HOME_DIAGNOSTIC_ORIGIN_LIMIT
      )
        return;
      counts.set(origin, count + 1);
      sink(
        HOME_DIAGNOSTIC_PREFIX +
          JSON.stringify({v: 1, sequence: ++sequence, origin, ...record}),
      );
    } catch {
      /* Logging never changes the original operation, including when the sink throws. */
    }
  };
  return {
    /** Fixed event codes and private local reference ordinals only. Never inspect
     * an owner, selection, token, error message or installation's contents. */
    lifetime(event: ShellLifetimeEvent, owner: object, parent?: object): void {
      let reserved = false;
      try {
        const object = (value: unknown): value is object => value !== null && (typeof value === 'object' || typeof value === 'function');
        if (!lifetimeEvents.has(event) || !object(owner) || parent !== undefined && !object(parent)
          || sequence + pendingLifetime >= HOME_DIAGNOSTIC_LIMIT
          || (counts.get('shell-lifetime') ?? 0) + pendingLifetime >= HOME_DIAGNOSTIC_ORIGIN_LIMIT) return;
        const record: SafeRecord = {operation: event, lifetimeOrder: ++lifetimeOrder, ownerRef: reference(owner),
          ...(parent ? {parentRef: reference(parent)} : {}), frames: safeFrames(new Error().stack, true)};
        pendingLifetime++;reserved = true;
        // Console/ErrorJournal observers never run inside a render, guard,
        // authority callback or disposal. The capture ordinal preserves ordering.
        queueMicrotask(() => {
          if (!reserved) return;
          reserved = false;pendingLifetime--; // Consume this slot once, then honor all other reservations.
          write('shell-lifetime', record);
        });
      } catch {
        if (reserved) {reserved = false;pendingLifetime--;}
        /* A diagnostic sink cannot change original lifetime or recovery. */
      }
    },
    /** Only local object/function reference numbers and fixed descriptor booleans.
     * Do not invoke getters, inspect runtime contents or stringify private objects. */
    libraryMenu(stage: MenuStage, runtime: unknown): void {
      try {
        if (!menuStages.has(stage) || sequence >= HOME_DIAGNOSTIC_LIMIT
          || (counts.get('library-menu') ?? 0) >= HOME_DIAGNOSTIC_ORIGIN_LIMIT) return;
        const object = runtime !== null && typeof runtime === 'object' ? runtime : undefined;
        const descriptor = object ? Object.getOwnPropertyDescriptor(object, 'libraryMenu') : undefined;
        const method = descriptor && 'value' in descriptor ? descriptor.value : undefined;
        write('library-menu', {operation: stage,
          ...(object ? {runtimeRef: reference(object)} : {}),
          ...(typeof method === 'function' ? {menuRef: reference(method)} : {}),
          own: !!descriptor, enumerable: descriptor?.enumerable === true, callable: typeof method === 'function'});
      } catch { /* Diagnostics cannot change rendering, even for a hostile descriptor. */ }
    },
    routeMemory(value: {routeSource: 'none' | 'local' | 'account'; signedCurrent: boolean; signedPrevious: boolean; pairedCount: number; lastGood: boolean}): void {
      try {
        if (!['none', 'local', 'account'].includes(value.routeSource) || !Number.isSafeInteger(value.pairedCount)
          || value.pairedCount < 0 || value.pairedCount > 32 || typeof value.signedCurrent !== 'boolean'
          || typeof value.signedPrevious !== 'boolean' || typeof value.lastGood !== 'boolean') return;
        write('route-memory', {operation: 'saved-route-summary', routeSource: value.routeSource,
          signedCurrent: value.signedCurrent, signedPrevious: value.signedPrevious, pairedCount: value.pairedCount, lastGood: value.lastGood});
      } catch { /* passive */ }
    },
    action(
      origin: ActionOrigin,
      operation: Operation,
      stage: Stage = 'enter',
    ): void {
      try {
        if (operations.has(operation) && stages.has(stage))
          write(origin, {operation, stage});
      } catch {
        /* passive */
      }
    },
    probeFailure(failure: RouteProbeFailure, source?: Readonly<{env: number; kind: 'server' | 'selected'}>): void {
      try {
        const {code, name} = failure;
        write('route-probe', {operation: 'report-error', code: probeCodes.has(code) ? code : 'other', class: probeNames.has(name) ? name : 'unknown',
          ...(source && Number.isSafeInteger(source.env) && (source.kind === 'server' || source.kind === 'selected') ? {env: source.env, kind: source.kind} : {})});
      } catch { /* A diagnostic observer never changes the original probe outcome. */ }
    },
    error(origin: ErrorOrigin, error: unknown, componentStack?: unknown): void {
      try {
        const componentFrames = origin === 'app-render' ? safeFrames(componentStack, false) : [];
        write(origin, {operation: 'report-error', ...safeError(error),
          ...(componentFrames.length ? {componentFrames} : {})});
      } catch {
        /* passive */
      }
    },
    /** The app boundary swallowed an expected cancellation and remounted the tree (P2-74). It is never
     * silent: the error class, the top component's name (code identifiers only) and its coordinates. */
    cancellationRemount(error: unknown, componentStack?: unknown): void {
      try {
        const top = typeof componentStack === 'string' ? componentStack.trim().split('\n', 1)[0] : '';
        const name = /^(?:in|at)\s+([A-Za-z_$][\w$.]{0,63})(?:\s|$)/.exec(top.trim())?.[1];
        const {class: kind, frames} = safeError(error);
        write('boundary-cancelled', {operation: 'quiet-remount', class: kind, frames,
          ...(name ? {component: name} : {}), componentFrames: safeFrames(componentStack, false).slice(0, 1)});
      } catch {
        /* passive */
      }
    },
    request(method: string, input: string): Ticket | undefined {
      try {
        if (
          sequence >= HOME_DIAGNOSTIC_LIMIT ||
          (counts.get('native-http') ?? 0) >= HOME_DIAGNOSTIC_ORIGIN_LIMIT ||
          input.length > 8192
        )
          return;
        const path = new URL(input).pathname,
          rule = compiled.find(
            value =>
              value.method === method.toUpperCase() && value.pattern.test(path),
          );
        if (!rule) return;
        const ticket = Object.freeze({
          request: ++requestSequence,
          route: rule.route,
        });
        tickets.add(ticket);
        started.set(ticket, Date.now());
        write('native-http', {operation: 'dispatch', ...ticket});
        return ticket;
      } catch {
        return;
      }
    },
    response(ticket: Ticket | undefined, status: number): void {
      try {
        if (ticket && tickets.has(ticket))
          write('native-http', {
            operation: 'response',
            ...ticket,
            status: statusOf(status),
            elapsedMs: elapsed(ticket),
          });
      } catch {
        /* passive */
      }
    },
    rejected(ticket: Ticket | undefined, error: unknown): void {
      try {
        if (ticket && tickets.has(ticket))
          write('native-http', {
            operation: 'rejected',
            ...ticket,
            ...safeError(error),
            elapsedMs: elapsed(ticket),
          });
      } catch {
        /* passive */
      }
    },
  };
}
export const homeActionDiagnostics = createHomeActionDiagnostics();
