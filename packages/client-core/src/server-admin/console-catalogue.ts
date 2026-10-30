/**
 * The console's words for tasks, alerts, scans and feedback, from the catalogue: every client
 * names a job, a job's state and an alert through these, in the viewer's language, and for a code
 * this client has no word for, a general word instead of the code itself.
 */
import type {MessageId, MessageValues} from '../../../i18n/src/index.ts';
import {sentence} from './console-words.ts';

type Translate = (id: MessageId, values?: MessageValues) => string;
type Has = (id: string) => boolean;

const known = (t: Translate, has: Has, prefix: string, code: string | undefined, fallback: MessageId): string => {
  const id = `${prefix}.${code ?? ''}`;
  return code && has(id) ? t(id as MessageId) : t(fallback);
};

/** A task's kind ("Library scan"). A kind the server added since reads as its own name in words, which is better than "Background task" for telling two apart. */
export const jobName = (t: Translate, has: Has, kind: string): string => (has(`server.job.${kind}`) ? t(`server.job.${kind}` as MessageId) : kind ? sentence(kind) : t('server.job.other'));
export const jobStateName = (t: Translate, has: Has, state: string): string => known(t, has, 'server.jobState', state, 'server.jobState.queued');
export const alertName = (t: Translate, has: Has, code: string): string => known(t, has, 'server.alert', code, 'server.alert.other');
export const severityName = (t: Translate, has: Has, severity: string | undefined): string => known(t, has, 'server.severity', severity, 'server.severity.notice');
export const scanStateName = (t: Translate, has: Has, status: string): string => known(t, has, 'server.scan.state', status, 'server.scan.state.queued');
export const scanDepthName = (t: Translate, has: Has, tier: string): string => known(t, has, 'server.scan.depth', tier, 'server.scan.depth.custom');
export const scanDepthHelp = (t: Translate, has: Has, tier: string): string => known(t, has, 'server.scan.depthHelp', tier, 'server.scan.depthHelp.custom');
/** One step of a scan. A step this client has no word for reads as its own name in words. */
export const scanStepName = (t: Translate, has: Has, step: string): string => (has(`server.scan.step.${step}`) ? t(`server.scan.step.${step}` as MessageId) : sentence(step));
export const feedbackStatusName = (t: Translate, has: Has, status: string): string => known(t, has, 'server.feedback.status', status, 'server.feedback.status.open');
/** CD-38: a category this client has no word for shows as it is, never blank. */
export const feedbackCategoryName = (t: Translate, has: Has, category: string): string => (has(`server.feedback.category.${category}`) ? t(`server.feedback.category.${category}` as MessageId) : category || t('server.feedback.category.other'));
