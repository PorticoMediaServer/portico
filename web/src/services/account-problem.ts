import type {MessageId} from '@i18n';
import {ServicesError} from './services-api';

/** Where a refusal is shown changes a few sentences: a code that is "not found" in a lookup is
 * a typing mistake, while the same code on a link means the approval ran out. */
export type ProblemPlace = 'signIn' | 'signUp' | 'confirm' | 'reset' | 'twoStep' | 'approve' | 'approveCode' | 'account';

const fieldMessages: Readonly<Record<string, MessageId>> = {
  email: 'next.account.problem.email', newEmail: 'next.account.problem.email', username: 'next.account.problem.username',
  displayName: 'next.account.problem.name', password: 'next.account.problem.password', newPassword: 'next.account.problem.password',
};

/** Which field a refusal belongs to, when it names one, so the sentence sits under that field. */
export function problemField(error: unknown): string | undefined {
  if (!(error instanceof ServicesError)) return undefined;
  if (error.code === 'username_taken') return 'username';
  if (error.code === 'invalid') return Object.keys(error.fields).find(field => field in fieldMessages);
  return undefined;
}

/** The sentence a person is shown for any refusal these pages can meet. Codes are the Services
 * contract's; one that is not listed gets the general "try again". */
export function problemMessage(error: unknown, place: ProblemPlace): MessageId {
  if (!(error instanceof ServicesError)) return 'next.account.problem.internal';
  switch (error.code) {
    case 'unreachable': return 'next.account.problem.unreachable';
    case 'busy': return 'next.account.problem.busy';
    case 'slow_down': return place === 'approve' ? 'next.account.problem.approveLimit' : 'next.account.problem.slowDown';
    case 'wait': return 'next.account.problem.wait';
    case 'signed_out': return 'next.account.problem.signedOut';
    case 'credentials': return 'next.account.problem.credentials';
    case 'username_taken': return 'next.account.problem.usernameTaken';
    case 'invalid': {
      const field = problemField(error);
      return field ? fieldMessages[field]! : 'next.account.problem.internal';
    }
    case 'email_unavailable': return 'next.account.problem.emailUnavailable';
    case 'code_wrong': return 'next.account.problem.codeWrong';
    case 'two_step_wrong': return 'next.account.problem.twoStepWrong';
    case 'challenge_gone': return 'next.account.problem.challengeGone';
    case 'approval_gone': return place === 'approveCode' ? 'next.account.problem.codeNotFound' : 'next.account.problem.approvalGone';
    case 'already_answered': return 'next.account.problem.alreadyAnswered';
    case 'server_owned': return 'next.account.problem.serverOwned';
    case 'too_many_servers': return 'next.account.problem.tooManyServers';
    case 'server_not_linked': return 'next.account.problem.serverNotLinked';
    case 'confirm_email': return 'next.account.problem.confirmEmail';
    case 'password_wrong': return 'next.account.problem.passwordWrong';
    case 'sign_in_again': return 'next.account.problem.signInAgain';
    case 'no_password': return 'next.account.problem.noPassword';
    case 'email_limit': return 'next.account.problem.emailLimit';
    case 'email_in_use': return 'next.account.problem.emailInUse';
    case 'owns_server': return 'next.account.delete.ownsServer';
    case 'last_way_in': return 'next.account.problem.lastWayIn';
    case 'two_step_on': return 'next.account.problem.twoStepOn';
    case 'two_step_not_started': return 'next.account.problem.twoStepNotStarted';
    case 'provider_in_use': return 'next.account.provider.inUse';
    case 'provider_unavailable': return 'next.account.provider.unavailable';
    case 'not_owner': return 'next.account.servers.notOwner';
    case 'owner_cannot_leave': return 'next.account.servers.ownerCannotLeave';
    default: return 'next.account.problem.internal';
  }
}
