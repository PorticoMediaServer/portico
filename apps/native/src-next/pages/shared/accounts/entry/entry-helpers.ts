import type {HostedServer} from '../../../../../../../packages/client-core/src';
import type {IdentityOptions} from '../../../../../../../packages/client-core/src/hosted-security';
import type {ManagedProfile} from '../../../../../../../packages/client-core/src/profile-management';
import type {SetupCodeSnapshot} from '../../../../../../../packages/client-core/src/setup-code';
import type {NativeAccountEntryStage} from '../../../../pages/shared/accounts/entry/entry-types';

/** TV reached a server nobody has set up. It is set up from a phone or a browser, not here. */
export const SETUP_ELSEWHERE = 'setup_elsewhere';
export const setupElsewhereRefusal = (error: unknown): boolean => (error as {code?: unknown} | null)?.code === SETUP_ELSEWHERE;

/** A profile's face on the pickers and the PIN page: its first letter on the profile's own colour. */
export const profileFace = (value: ManagedProfile) => ({initial: [...value.name][0] ?? '', color: value.art});

export function isValidDirectoryResponse(out: {items?: unknown} | null | undefined): out is {items: HostedServer[]} {
  if (!out || !Array.isArray(out.items)) return false;
  const items = out.items as HostedServer[];
  if (items.some(value => !value || typeof value.id !== 'string' || !value.id
    || typeof value.name !== 'string' || !value.name || typeof value.baseUrl !== 'string' || typeof value.publicKey !== 'string'
    || !Number.isSafeInteger(value.policyRevision) || value.policyRevision < 0)) return false;
  return new Set(items.map(value => value.id)).size === items.length;
}

const TRANSIENT_DIRECTORY_CATEGORIES = new Set(['offline', 'unreachable', 'timeout', 'busy', 'unavailable', 'invalid-response', 'rate-limited']);
export const isTransientDirectoryFailure = (category: string): boolean => TRANSIENT_DIRECTORY_CATEGORIES.has(category);

export function isProviderOffered(
  value: IdentityOptions['providers'][number],
  authModule: {canOpenBrowser?: boolean; bundleId?: string; canSignInApple?: boolean} | undefined,
): boolean {
  return value.enabled && (Boolean(authModule?.canOpenBrowser)
    || (value.id === 'apple' && value.native
      && Boolean(authModule?.bundleId && value.nativeClientIds.includes(authModule.bundleId))
      && Boolean(authModule?.canSignInApple)));
}

export function isPasswordInvalid(stage: NativeAccountEntryStage, stageError: unknown): boolean {
  return stage === 'local' && (stageError as {code?: string} | undefined)?.code === 'local_credentials_invalid';
}

export function computeTVNoCodeState(
  isTV: boolean,
  stage: NativeAccountEntryStage,
  tvKind: 'local' | 'portico',
  tvState: SetupCodeSnapshot<unknown>,
  approvalPhase: string | undefined,
  stageError: unknown,
): {selfRetrying: boolean; noCodeNow: boolean} {
  const selfRetrying = tvState.phase === 'creating' && !!tvState.message;
  const noCodeNow = isTV && stage === 'tv' && tvKind === 'portico' && !tvState.userCode
    && (!approvalPhase || approvalPhase === 'ready')
    && (selfRetrying || tvState.phase === 'error' || stageError !== undefined);
  return {selfRetrying, noCodeNow};
}
