import type {MessageId} from '../../../i18n/src/index.ts';
import type {SettingsValue} from './settings-spec.ts';

/**
 * Settings auto-save. A few remote-access edits can cut off everyone connected from away from home,
 * possibly the owner making the change, so they ask once before saving (O-151). Keyed by row id, so the
 * reworked Secure connections / LAN networks rows keep it. `values` limits the question to the choices
 * that can cut people off; without it, any change asks.
 */
const remote: MessageId = 'settings.confirm.remoteAccess';
const confirmations: Readonly<Record<string, Readonly<{message: MessageId; values?: readonly SettingsValue[]}>>> = Object.freeze({
  'remote.who': {message: remote, values: ['owner', 'none']},
  'remote.mapping': {message: remote, values: ['off']},
  'remote.secure': {message: remote, values: [false]},
  // Required drops every plain-HTTP connection, including the owner's own if they are on one.
  'remote.secureConnections': {message: remote, values: ['required']},
  'remote.publicPort': {message: remote},
  'remote.interface': {message: remote},
  'remote.homeNets': {message: remote},
  'remote.trustedProxies': {message: remote},
});

/** The question to ask before saving this change, or undefined to save at once. */
export function settingsChangeConfirmation(rowId: string, next: SettingsValue, current: SettingsValue | undefined): MessageId | undefined {
  const rule = confirmations[rowId];
  if (!rule || JSON.stringify(next) === JSON.stringify(current ?? null)) return undefined;
  return !rule.values || rule.values.some(value => value === next) ? rule.message : undefined;
}
