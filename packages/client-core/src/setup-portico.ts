/** First-run setup with a Portico Account on the rebuilt server (`server-next/API.md`, `POST /v1/setup/portico` and
 * `/finish`). The server asks Portico Services for an approval address; the person approves there; the browser comes
 * back to this server's setup page with the approval in the address fragment (`#approval=…`, never sent to any
 * server by the browser), or a phone asks the server how the approval stands. The server checks the approval
 * itself: who approved, that it is for this server and for setup, that it has not run out or been used. */

/** The same strict base64url read as `envelopePayload` in `portico-account.ts` (kept here so setup does not load the
 * Portico Account client). */
function base64urlJson(text: string): unknown {
  const base64 = text.replace(/-/g, '+').replace(/_/g, '/');
  return JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(base64 + '='.repeat((4 - base64.length % 4) % 4)), c => c.charCodeAt(0))));
}

/** The page Portico Services sends the browser back to: this server's setup page, marked as a return so a person
 * who comes back without approving is told so. Nothing in it is secret. */
export function setupReturnUrl(pageOrigin: string): string {
  return `${pageOrigin}/setup?portico=return`;
}

/** What a setup page found in its own address when it opened: whether it is a return from Portico Services, and the
 * approval it carries, if any. */
export type SetupPageReturn = Readonly<{returned: boolean; approval?: string}>;
export function readSetupPageReturn(search: string, hash: string): SetupPageReturn | undefined {
  const returned = new URLSearchParams(search).get('portico') === 'return';
  const approval = new URLSearchParams(hash.replace(/^#/, '')).get('approval') ?? undefined;
  if (!returned && !approval) return undefined;
  return Object.freeze({returned, ...(approval && /^[A-Za-z0-9_-]{1,8192}$/.test(approval) ? {approval} : {})});
}

/** Which Portico Account approved, read from the returned approval to label the stored sign-in (grouping and
 * Portico sign-out), never as proof: the server has already checked it. An approval that is not a setup approval
 * for `serverId` gives nothing, so a mixed-up address can never label another server. */
export function setupApprovalAccount(approval: string, serverId: string): string | undefined {
  try {
    const envelope = base64urlJson(approval) as {payload?: unknown} | null;
    if (!envelope || typeof envelope !== 'object' || typeof envelope.payload !== 'string') return undefined;
    const payload = base64urlJson(envelope.payload) as {kind?: unknown; serverId?: unknown; purpose?: unknown; accountId?: unknown};
    if (payload.kind !== 'portico.server-approval' || payload.purpose !== 'setup' || payload.serverId !== serverId) return undefined;
    return typeof payload.accountId === 'string' && payload.accountId ? payload.accountId : undefined;
  } catch {
    return undefined;
  }
}
