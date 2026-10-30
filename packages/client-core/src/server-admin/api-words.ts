import {routePath} from "../../../contracts/src/route-paths.ts";
/**
 * API keys, personal access tokens and webhooks in words, once for every client: what a key may
 * do, when it stops working, why it stopped, which events a webhook can be sent and how a
 * delivery went. No transport here (`api-access.ts`) and no rendering.
 */
import {apiScopes, type ApiCredential, type ApiScope, type Webhook, type WebhookDelivery} from '../api-access.ts';
import type {MessageId, MessageValues} from '../../../i18n/src/index.ts';

type Translate = (id: MessageId, values?: MessageValues) => string;
type Has = (id: string) => boolean;
export type WordTone = 'healthy' | 'warning' | 'danger' | 'neutral';

// ── What a key or token may do ──────────────────────────────────────────────

/**
 * The scopes a creation sends for what was ticked. "Everything" stands alone (it already includes
 * the rest); otherwise the ticked ones in the server's order, each once.
 */
export function chosenScopes(ticked: ReadonlySet<ApiScope>): readonly ApiScope[] {
  if (ticked.has('admin')) return ['admin'];
  return apiScopes.filter(scope => ticked.has(scope));
}

/** Whether a scope shows as ticked: its own tick, or "Everything". */
export const scopeTicked = (ticked: ReadonlySet<ApiScope>, scope: ApiScope): boolean => ticked.has(scope) || ticked.has('admin');

/** What a key or token may do, as a list reads it: "Read, Manage libraries" or "Everything". */
export function scopesText(t: Translate, scopes: readonly ApiScope[]): string {
  if (scopes.includes('admin')) return t('api.scope.admin');
  return apiScopes.filter(scope => scopes.includes(scope)).map(scope => t(`api.scope.${scope}`)).join(', ');
}

// ── When it stops working ───────────────────────────────────────────────────

/** The choices of "Stops working": never, or a number of days from now. */
export const EXPIRY_DAYS = [0, 30, 90, 365] as const;
export type ExpiryDays = (typeof EXPIRY_DAYS)[number];

export function expiryLabel(t: Translate, days: ExpiryDays): string {
  return days === 0 ? t('api.credential.expiryNever') : days === 365 ? t('api.credential.expiryYear') : t('api.credential.expiryDays', {count: days});
}

/** The instant a choice of days ends at, or nothing for "never". */
export function expiryInstant(days: ExpiryDays, now: number): string | undefined {
  return days > 0 ? new Date(now + days * 86_400_000).toISOString() : undefined;
}

// ── A key or token in a list ────────────────────────────────────────────────

/** Still usable, or waiting for the owner to approve it: the ones a list shows first and offers to revoke. */
export const credentialWorks = (credential: ApiCredential): boolean => credential.state === 'active' || credential.state === 'awaiting_approval';

/** "Read · ends in a1b2 · last used 3 hours ago · expires 4 Jan": the line under a key's name. */
export function credentialFacts(t: Translate, credential: ApiCredential, format: Readonly<{relative: (iso: string) => string; date: (iso: string) => string}>): string {
  return [
    scopesText(t, credential.scopes),
    credential.hint ? t('api.credential.endsIn', {hint: credential.hint}) : '',
    credential.lastUsedAt ? t('api.credential.lastUsed', {when: format.relative(credential.lastUsedAt)}) : t('api.credential.neverUsed'),
    credential.expiresAt ? t('api.credential.expires', {when: format.date(credential.expiresAt)}) : t('api.credential.noExpiry'),
  ].filter(Boolean).join(' · ');
}

/** Whose token it is, for the owner's list: the account, and the profile when it has another name. */
export function credentialMember(credential: ApiCredential): string {
  const {username, profile} = credential.member;
  return profile && profile !== username ? (username ? `${username} · ${profile}` : profile) : username;
}

/** The state of a key that is not simply working, and why it stopped where the server says. Nothing for a working one. */
export function credentialState(t: Translate, has: Has, credential: ApiCredential): {tone: WordTone; text: string} | undefined {
  if (credential.state === 'active') return undefined;
  if (credential.state === 'awaiting_approval') return {tone: 'warning', text: t('api.credential.state.awaiting_approval')};
  const reason = credential.endedReason ? `api.credential.ended.${credential.endedReason}` : '';
  const state = t(`api.credential.state.${credential.state}`);
  return {tone: 'neutral', text: reason && has(reason) ? `${state}. ${t(reason as MessageId)}` : state};
}

// ── Webhook events ──────────────────────────────────────────────────────────

/** The checklist's groups, in the order shown. An event's group is the word before its first dot. */
const EVENT_GROUPS: readonly Readonly<{id: string; label: MessageId; prefixes: readonly string[]}>[] = [
  {id: 'titles', label: 'api.webhooks.group.titles', prefixes: ['title']},
  {id: 'scans', label: 'api.webhooks.group.scans', prefixes: ['scan']},
  {id: 'playback', label: 'api.webhooks.group.playback', prefixes: ['playback']},
  {id: 'recordings', label: 'api.webhooks.group.recordings', prefixes: ['recording']},
  {id: 'people', label: 'api.webhooks.group.people', prefixes: ['member']},
  {id: 'server', label: 'api.webhooks.group.server', prefixes: ['backup', 'storage', 'update', 'server']},
];
const OTHER_EVENTS: MessageId = 'api.webhooks.group.other';

export type WebhookEventGroup = Readonly<{id: string; label: MessageId; events: readonly string[]}>;

/**
 * The events the server offers, grouped for the checklist. An event of a kind this app has no
 * group for is still offered, under "Other", so a newer server's event can always be chosen.
 */
export function webhookEventGroups(events: readonly string[]): readonly WebhookEventGroup[] {
  const offered = [...new Set(events)];
  const groups: WebhookEventGroup[] = [];
  const placed = new Set<string>();
  for (const group of EVENT_GROUPS) {
    const mine = offered.filter(event => group.prefixes.includes(event.split('.')[0] ?? ''));
    for (const event of mine) placed.add(event);
    if (mine.length) groups.push({id: group.id, label: group.label, events: mine});
  }
  const rest = offered.filter(event => !placed.has(event));
  if (rest.length) groups.push({id: 'other', label: OTHER_EVENTS, events: rest});
  return groups;
}

/** An event in words ("Someone starts playing"); one this app has no words for is shown by its own name. */
export function webhookEventLabel(t: Translate, has: Has, event: string): string {
  const id = `api.webhooks.event.${event}`;
  return has(id) ? t(id as MessageId) : event;
}

/** What needs the owner's eye on a webhook, most serious first; nothing when all is well. */
export function webhookTrouble(t: Translate, webhook: Webhook): {tone: WordTone; text: string} | undefined {
  // The server's authoritative retirement fact comes first: ordinary delivery counts cannot say
  // that the destination or signing secret needs the owner's review before it may be enabled.
  if (webhook.requiresOwnerReview === true) return {tone: 'warning', text: t('api.webhooks.ownerReviewRequired')};
  if (webhook.failing > 0) return {tone: 'danger', text: t('api.webhooks.failing', {count: webhook.failing})};
  if (webhook.dropped > 0) return {tone: 'warning', text: t('api.webhooks.dropped', {count: webhook.dropped})};
  if (webhook.waiting > 0) return {tone: 'neutral', text: t('api.webhooks.waiting', {count: webhook.waiting})};
  return undefined;
}

// ── The delivery log ────────────────────────────────────────────────────────

export const deliveryTone = (delivery: WebhookDelivery): WordTone => (delivery.state === 'delivered' ? 'healthy' : delivery.state === 'failed' ? 'danger' : 'warning');

/** Why a delivery has not arrived, in words: the server's reason, or the other service's answer. Nothing for one that arrived or has not been tried. */
export function deliveryProblem(t: Translate, has: Has, delivery: WebhookDelivery): string | undefined {
  if (delivery.state === 'delivered') return undefined;
  if (delivery.error) {
    const id = `api.webhooks.error.${delivery.error}`;
    return t(has(id) ? (id as MessageId) : 'api.webhooks.error.request_failed');
  }
  if (delivery.status >= 300) return t('api.webhooks.error.status', {status: delivery.status});
  return undefined;
}

/** "3 attempts · trying again at 14:05": how far a delivery has got. */
export function deliveryProgress(t: Translate, delivery: WebhookDelivery, time: (iso: string) => string): string {
  return [
    delivery.attempts > 0 ? t('api.webhooks.attempts', {count: delivery.attempts}) : '',
    delivery.state === 'pending' && delivery.nextAttemptAt && delivery.attempts > 0 ? t('api.webhooks.nextAttempt', {when: time(delivery.nextAttemptAt)}) : '',
  ].filter(Boolean).join(' · ');
}

// ── Addresses ───────────────────────────────────────────────────────────────

/** An address the server can be asked to send a webhook to: http or https, with a host, and no name or password in it. The server decides the rest. */
export function webhookAddressOk(address: string): boolean {
  try {
    const url = new URL(address.trim());
    return (url.protocol === 'http:' || url.protocol === 'https:') && !!url.hostname && !url.username && !url.password;
  } catch { return false; }
}

/** Where an assistant connects: the server's address with the path every Portico server uses. */
export function assistantAddress(serverBase: string): string {
  return new URL(routePath("mcp_stream"), serverBase).toString();
}
