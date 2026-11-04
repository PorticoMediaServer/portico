import {routePath} from '../../contracts/src/route-paths.ts';
import type {PatchSignInPolicyRequest} from '../../contracts/src/types.ts';
import type {SettingsConfiguration} from './settings-configuration.ts';

/** Sign-in › Advanced (#713): nine rows over the identity sign-in policy. The configuration read publishes their
 * values with the policy's own revision (`signInPolicyRevision`); every change sends the complete nine-value object
 * with that revision to the owner-only PATCH /v1/admin/sign-in/policy, never through the settings change path. */
export type SignInPolicyValues = PatchSignInPolicyRequest['values'];
type Property = keyof SignInPolicyValues;
export const signInPolicyRows: Readonly<Record<string, Property>> = Object.freeze({
  'signin.stayLength': 'staySignedInDays', 'signin.pinRemember': 'pinRememberDays', 'signin.passwordAttempts': 'passwordAttempts',
  'signin.pinAttempts': 'pinAttempts', 'signin.passwordLength': 'passwordMinLength', 'signin.resetCodeLife': 'memberResetCodeMinutes',
  'signin.requireTwoStep': 'requireTwoStep', 'signin.rememberAccounts': 'rememberAccounts', 'signin.endedKeep': 'endedSessionsDays',
});
const numbers = new Set<Property>(['staySignedInDays', 'pinRememberDays', 'passwordMinLength', 'memberResetCodeMinutes', 'endedSessionsDays']);
const texts = new Set<Property>(['passwordAttempts', 'pinAttempts', 'requireTwoStep']);

export const isSignInPolicyRow = (rowId: string): boolean => Object.hasOwn(signInPolicyRows, rowId);

function typed(property: Property, value: unknown): SignInPolicyValues[Property] | undefined {
  if (numbers.has(property)) {const n = typeof value === 'string' && value.trim() !== '' ? Number(value) : value; return typeof n === 'number' && Number.isSafeInteger(n) ? n : undefined;}
  if (texts.has(property)) return typeof value === 'string' && value ? value : undefined;
  return typeof value === 'boolean' ? value : undefined;
}

/** The nine current values as the server published them, or undefined when any is missing or malformed. */
export function signInPolicyValues(configuration: SettingsConfiguration | undefined): SignInPolicyValues | undefined {
  if (!configuration) return undefined;
  const out: Partial<Record<Property, unknown>> = {};
  for (const [id, property] of Object.entries(signInPolicyRows)) {
    const field = configuration.fields.find(field => field.id === id);
    const value = field && typed(property, field.current);
    if (value === undefined) return undefined;
    out[property] = value;
  }
  return Object.freeze(out) as SignInPolicyValues;
}

/** The complete object with one row changed; select controls hand numbers over as strings. */
export function signInPolicyWith(values: SignInPolicyValues, rowId: string, value: unknown): SignInPolicyValues {
  const property = signInPolicyRows[rowId];
  if (!property) throw new Error('sign_in_policy_row_unknown');
  const next = typed(property, value);
  if (next === undefined) throw new Error('sign_in_policy_value_invalid');
  return Object.freeze({...values, [property]: next});
}

export type SignInPolicyAPI = Readonly<{request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>}>;
export function patchSignInPolicy(api: SignInPolicyAPI, expectedRevision: string, values: SignInPolicyValues, signal?: AbortSignal): Promise<unknown> {
  const body: PatchSignInPolicyRequest = {expectedRevision, values};
  return api.request(routePath('patch_sign_in_policy'), 'PATCH', body, signal);
}

/** Someone else changed the policy first: re-read and show what is current. */
export function signInPolicyConflict(error: unknown): boolean {
  return !!error && typeof error === 'object' && (error as {code?: unknown}).code === 'revision_mismatch';
}
