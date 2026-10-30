export {ActivitySummaryContractError, parseActivitySummary,parseDashboardSessions} from './parser.ts';
export {ActivitySummaryService,DashboardSessionsService} from './service.ts';
export type {OwnerActivitySummary,DashboardSessionsPage,DashboardSessionsSnapshot, ActivitySummarySession, ActivitySummaryScan, ActivitySummaryTask, ActivitySummaryScope, ActivitySummarySource, ActivitySummarySourceStore, ActivitySummaryErrorCode, ActivitySummarySnapshot, ActivitySummaryTimers} from './types.ts';

import type {MessageId,MessageValues} from '../../../../i18n/src/index.ts';
import type {OwnerActivitySummary} from './types.ts';
export type ActivityCopy=Readonly<{title:MessageId;detail:MessageId;values:MessageValues}>;
/** Only qualified source facts select a major-issue message. Ordinary health
 * and unmatched/scan counts have no route into this mapping. */
export function activityIssueCopy(issue:OwnerActivitySummary['majorIssues'][number]):ActivityCopy|undefined {
 const keys:Record<string,readonly[MessageId,MessageId]>={
  library_unreachable:['next.shell.issue.libraryTitle','next.shell.issue.libraryDetail'],
  remote_unreachable:['next.shell.issue.remoteTitle','next.shell.issue.remoteDetail'],
  remote_program_unavailable:['next.shell.issue.remoteProgramTitle','next.shell.issue.remoteProgramDetail'],
  storage_nearly_full:['next.shell.issue.storageTitle','next.shell.issue.storageDetail'],
  backup_failed:['next.shell.issue.backupTitle','next.shell.issue.backupDetail'],
  secure_renewal_failed:['next.shell.issue.renewalTitle','next.shell.issue.renewalDetail'],
 };
 const key=keys[issue.code];return key?{title:key[0],detail:key[1],values:{subject:issue.subject}}:undefined;
}
/** Alert parameters stay separate from localized sentences and raw diagnostics. */
export function activityAlertCopy(alert:Readonly<{code:string;params:Readonly<Partial<Record<'title'|'channel'|'reason'|'time',string>>>}>):ActivityCopy|undefined {
 if(alert.code==='upload-budget-exhausted')return {title:'next.shell.alert.uploadTitle',detail:'next.shell.alert.uploadDetail',values:{}};
 if(alert.code.startsWith('job-failed-'))return {title:'next.shell.alert.jobTitle',detail:alert.params.title?'next.shell.alert.jobNamedDetail':'next.shell.alert.jobDetail',values:alert.params.title?{title:alert.params.title}:{}};
 return undefined;
}
