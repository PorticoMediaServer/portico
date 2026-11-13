import {presentError} from '../../../../../packages/client-core/src/presentation/errors';

/** Phone and TV PIN flows use the shared code and wait mapping. */
export function profilePinFailureText(error: unknown): string | undefined {
  const {code} = (error ?? {}) as {code?: unknown};
  return code === 'profile_pin_invalid' || code === 'profile_pin_locked'
    ? presentError(error, 'profiles').body : undefined;
}
