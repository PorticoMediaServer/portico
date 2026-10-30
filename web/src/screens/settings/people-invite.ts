import type {ServerInvitation} from '@core/direct-account.ts';

/** The exact create this People workspace will send for one invitation. One
 * attempt owns one body and one operation id, so a retry after an uncertain
 * answer repeats the same command instead of minting a second capability. */
export type InviteAccess = Readonly<{maxStreamsMode: 'inherit' | 'unlimited' | 'limit'; maxStreams: number; allowRemote: boolean; accessTokens: boolean; maxContentRating: string}>;
export type InviteBody = Readonly<{operationId: string; role: string; allowedLibraries: readonly string[]; expiresInHours: number; notes?: string; porticoUsername?: string; access: InviteAccess}>;
export type InviteAttempt = Readonly<{operationId: string; body: InviteBody}>;

/** "Streams at once" as drawn: the server default, no limit, or a count. */
export type InviteStreams = 'default' | 'unlimited' | '1' | '2' | '3' | '4' | '6';
export const streamChoices: readonly InviteStreams[] = Object.freeze(['default', 'unlimited', '1', '2', '3', '4', '6']);
/** "Highest rating" as drawn; empty is No limit. */
export const ratingChoices = Object.freeze(['', 'G', 'PG', 'PG-13', 'R'] as const);

/** The drawn Invite form's choices, before they become one immutable command. */
export type InviteChoices = Readonly<{role: 'member' | 'admin'; allLibraries: boolean; libraryIds: readonly string[]; expiresInHours: number; notes?: string; porticoUsername?: string;
  streams: InviteStreams; awayAllowed: boolean; accessTokens: boolean; maxRating: string}>;
export const expiryChoices = Object.freeze([24, 72, 168, 720] as const);
export const defaultInviteChoices: InviteChoices = Object.freeze({role: 'member', allLibraries: true, libraryIds: [], expiresInHours: 168,
  streams: 'default', awayAllowed: true, accessTokens: true, maxRating: ''});

/** The grant the server stores and applies when the person joins. */
export function inviteAccess(choices: Pick<InviteChoices, 'streams' | 'awayAllowed' | 'accessTokens' | 'maxRating'>): InviteAccess {
  const count = Number(choices.streams);
  return Object.freeze({maxStreamsMode: choices.streams === 'default' ? 'inherit' : choices.streams === 'unlimited' ? 'unlimited' : 'limit',
    maxStreams: Number.isInteger(count) && count > 0 ? count : 0, allowRemote: choices.awayAllowed, accessTokens: choices.accessTokens, maxContentRating: choices.maxRating});
}

/** The form's choices for an attempt that is being reconciled, read back from its exact body. */
export function inviteChoicesOf(body: InviteBody): InviteChoices {
  const a = body.access;
  return Object.freeze({role: body.role === 'admin' ? 'admin' : 'member', allLibraries: body.allowedLibraries.length === 0, libraryIds: body.allowedLibraries,
    expiresInHours: body.expiresInHours, ...(body.notes === undefined ? {} : {notes: body.notes}), ...(body.porticoUsername === undefined ? {} : {porticoUsername: body.porticoUsername}),
    streams: a.maxStreamsMode === 'inherit' ? 'default' : a.maxStreamsMode === 'unlimited' ? 'unlimited' : String(a.maxStreams) as InviteStreams,
    awayAllowed: a.allowRemote, accessTokens: a.accessTokens, maxRating: a.maxContentRating});
}

/** What the server gets: admin is the existing wire word for Administrator, and
 * private notes stay a create-only field (never a card or a link). */
export function inviteBody(choices: InviteChoices) {
  if (!choices.allLibraries && !choices.libraryIds.length) throw new Error('invite_library_required');
  return Object.freeze({role: choices.role === 'admin' ? 'admin' : 'member',
    allowedLibraries: Object.freeze([...(choices.allLibraries ? [] : choices.libraryIds)]),
    expiresInHours: choices.expiresInHours,
    access: inviteAccess(choices),
    ...(choices.notes?.trim() ? {notes: choices.notes.trim()} : {}),
    ...(choices.porticoUsername?.trim() ? {porticoUsername: choices.porticoUsername.trim()} : {})});
}

/** Definite refusals did not create anything, so the attempt may be replaced. */
const definite = (status: number | undefined) => status !== undefined && status >= 400 && status < 500 && status !== 408 && status !== 429;

/**
 * Single-flight create for the People Invite flow. `begin` is synchronous and
 * returns undefined while an attempt is in flight; an uncertain outcome keeps
 * the attempt (same body, same operation id) for the next explicit retry.
 */
export class InviteAttempts {
  private attempt: InviteAttempt | undefined;
  private running = false;
  private retired = false;

  constructor(private readonly createOperationId: () => string) {}

  /** The current attempt, or a new one. Undefined means "already running". */
  begin(input: Omit<InviteBody, 'operationId'>): InviteAttempt | undefined {
    if (this.running || this.retired) return undefined;
    // Capture the body and the cryptographic id before committing the gate: a
    // refusing id generator must leave no stuck attempt and no fallback id.
    if (!this.attempt) {
      const operationId = this.createOperationId();
      this.attempt = Object.freeze({operationId, body: Object.freeze({...input, allowedLibraries: Object.freeze([...input.allowedLibraries]), operationId})});
    }
    this.running = true;
    return this.attempt;
  }

  /** A created invitation ends the attempt; the next Invite is a new command. */
  created(): void { this.attempt = undefined; this.running = false; }

  /** An uncertain or retryable failure keeps the attempt for the retry. */
  failed(error: unknown): void {
    this.running = false;
    const status = (error as {status?: unknown})?.status;
    if (typeof status === 'number' && definite(status)) this.attempt = undefined;
  }

  /** Retired workspaces never send, reuse or publish anything again. */
  retire(): void { this.retired = true; this.running = false; this.attempt = undefined; }

  get busy(): boolean { return this.running; }

  get pending(): InviteAttempt | undefined { return this.retired ? undefined : this.attempt; }
}

export type InviteCreated = ServerInvitation;
