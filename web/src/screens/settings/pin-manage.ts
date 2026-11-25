import {managementPinRefused} from '@core/profile-management.ts';

/** True when an error is a server `403 profile_pin_manage` management-PIN refusal. */
export function isProfilePinManageError(error: unknown): boolean {
  if (!managementPinRefused(error)) return false;
  const value = error as {status?: unknown; statusCode?: unknown};
  const status = value.status ?? value.statusCode;
  return status === undefined || status === 403;
}

/** Detect a `403 profile_pin_manage` refusal and invoke the shared PIN-unlock / report flow.
 * Returns `true` when handled so callers never misclassify it as authority loss or a generic inline error. */
export function handleProfilePinManage(error: unknown, report?: (error: unknown) => void): boolean {
  if (!isProfilePinManageError(error)) return false;
  report?.(error);
  return true;
}
