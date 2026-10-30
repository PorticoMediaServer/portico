import type {RuntimeSettings} from '../../../../../../../packages/client-core/src/console';
import type {LogsDraft, MaintenanceDocument, MaintenanceSettings} from '../../../../../../../packages/client-core/src/server-admin/server-forms';
import {defaultI18n, type MessageId} from '../../../../../../../packages/i18n/src';

/** These expert producers publish values but no defaults. Show their actual values rather than
 * claiming they are at defaults; the spec-backed controls are compared separately. */
export function storageExtraSummary(runtime: RuntimeSettings | undefined, maintenance: MaintenanceSettings | undefined, document: MaintenanceDocument | undefined, logs: LogsDraft | undefined, t: typeof defaultI18n.t): readonly string[] {
  const parts: string[] = [];
  const days = (label: string, value: number) => parts.push(`${label}: ${value === 0 ? t('next.settings.never') : `${defaultI18n.number(value)} ${t('settings.unit.days')}`}`);
  if (runtime) days(t('web.logs.retentionTasks'), runtime.jobDays);
  if (maintenance && document) for (const category of document.storageCategories) {
    if (!['conversions', 'prepared', 'downloads', 'subtitles', 'trickplay'].includes(category.retentionKey)) days(category.name, maintenance.retention[category.retentionKey] ?? 0);
  }
  if (logs) for (const entry of logs.retention) days(t('settings.server.retention.logs', {category: t(`server.logCategory.${entry.category}` as MessageId)}), entry.days);
  return parts;
}
