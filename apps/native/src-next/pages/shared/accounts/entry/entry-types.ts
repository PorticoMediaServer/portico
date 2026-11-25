import type {IdentityTransaction, Registration} from '../../../../../../../packages/client-core/src/hosted-security';
import type {ManagedProfile} from '../../../../../../../packages/client-core/src/profile-management';
import type {ServerSignIn} from '../../../../../../../packages/client-core/src/sign-in';
import type {NativeAccountWorkspace} from '../../../../platform/account-workspace';
import type {NativeInvitationLink} from '../../../../platform/invitation-links';
import type {NativeInvitationEntry} from '../../../../platform/portico-invitation';
import type {NativeFactorWitness, NativePorticoSignIn} from '../../../../platform/portico-sign-in';
import type {NativeProfileRecovery} from '../../../../platform/profile-recovery';
import type {NativeServerSignIn} from '../../../../platform/server-sign-in';
import type {NativeSignInHint} from '../../../../platform/sign-in-ended';
import type {AccountDownloadsEntry} from '../../../../screens/accounts/downloads-entry-model';

export type NativeAccountEntryStage = 'welcome' | 'tv-move-handoff' | 'ended-sign-in' | 'owner-reset' | 'owner-reset-code' | 'owner-reset-done' | 'setup-confirm' | 'setup' | 'address' | 'identity-code' | 'local' | 'factor' | 'uses-portico' | 'portico' | 'forgot' | 'forgot-sent' | 'register' | 'verify' | 'provider' | 'contact' | 'servers' | 'account-sign-out' | 'profiles' | 'profile-pin' | 'profile-forgot-pin' | 'tv' | 'tv-credentials' | 'tv-required-setup' | 'member-move' | 'invitation-code' | 'invitation-local' | 'invitation-portico' | 'invitation-sign-out' | 'invitation-entry' | 'invitation-account' | 'account-workspace';

export type AccountIntent = Readonly<{kind: 'provider'; id: 'apple' | 'google'} | {kind: 'continue' | 'forgot' | 'create'}>;
export type MemberMove = Awaited<ReturnType<NativeServerSignIn['createMemberMove']>>;
export type MemberMoveSnapshot = ReturnType<MemberMove['getSnapshot']>;
export type ProfilePINOpening = Readonly<{profile: ManagedProfile; operation: NativeServerSignIn}>;
export type PINRecovery = {controller: NativeProfileRecovery; profile: ManagedProfile; operation: NativeServerSignIn; retired: boolean};
export type RequiredSetup = Readonly<{kind: 'required-setup'; requirement: 'password' | 'two-step'; serverName: string; serverOrigin: string}>;
export type EntryResult = {kind: 'profiles'; profiles: readonly ManagedProfile[]; canRemember?: boolean} | {kind: 'challenge'; expiresAt: string} | {kind: 'complete'} | RequiredSetup;

export type AccountEntryProps = {
  downloads?: AccountDownloadsEntry;
  incomingInvitation?: NativeInvitationLink;
  onBack?: () => void;
  onComplete: () => void;
  onSetupComplete?: (serverId: string) => void;
  onStageChange?: (stage: NativeAccountEntryStage) => void;
  initialStage?: 'welcome' | 'address';
  initialAddress?: string;
  endedSignIn?: NativeSignInHint;
  /** Launch with no saved server: a Portico Account kept on this device opens its server list, not welcome. */
  resumeAccount?: boolean;
};

export type AccountScope = Readonly<{accountId: string; familyId: string}>;
export type NearbyServerEntry = Readonly<{origin: string; server: ServerSignIn}>;

export type OwnerRecoveryOpening = {active: boolean; contact?: {open: () => Promise<void>; dispose: () => void}; finish?: () => void};
export type OwnerResetOpening = {source: NativeServerSignIn; controller: AbortController};
export type OwnerResetIntent = Readonly<{origin: string; serverId: string}>;

export type RegisterOpening = Readonly<{source: NativePorticoSignIn; owner: {live: boolean}}>;
export type VerifyOpening = Readonly<{source: NativePorticoSignIn; registration: Registration}>;
export type ProviderOpening = Readonly<{transaction: IdentityTransaction; source: NativePorticoSignIn; owner: {live: boolean}; life: AbortController}>;
export type FactorOpening = Readonly<{source: NativePorticoSignIn; witness: NativeFactorWitness; owner: {live: boolean}; life: AbortController}>;

export type InvitationOpening = {source: NativeServerSignIn; active: boolean} & (
  {kind: 'local'; controller: ReturnType<NativeServerSignIn['createLocalInvitation']>}
  | {kind: 'portico'; controller: Awaited<ReturnType<NativeServerSignIn['preparePorticoInvitation']>>}
);
export type InvitationSwitch = {entry: NativeInvitationEntry; active: boolean; scope?: AccountScope; username?: string};
export type InvitationIntent = NonNullable<ReturnType<typeof NativeServerSignIn.parseInvitationEntry>>
  & Partial<Pick<NativeInvitationEntry, 'fingerprint' | 'signIn' | 'expiresAt'>>
  & {publicPreview?: boolean};
export type InvitationEntryOpening = {active: boolean; returnAddress?: string; external?: NativeInvitationLink};

export type WorkspaceOpening = {active: boolean; username: string; scope: {accountId: string; familyId: string}; source?: NativeAccountWorkspace; createSource?: () => NativeAccountWorkspace};
export type DownloadsOpening = {source: AccountDownloadsEntry; active: boolean};
