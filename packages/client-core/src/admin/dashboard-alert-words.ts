import type {MessageId, MessageValues} from '../../../i18n/src/index.ts';
import type {DashboardStatus} from './dashboard.ts';

type DashboardAlert = DashboardStatus['alerts'][number];
type Translate = (id: MessageId, values?: MessageValues) => string;

/** The Dashboard's alerts in words, as the web draws them (console rule 10): a known code by its own sentence, any other
 *  by the server's message, or "unknown" when it sends none. */
const ALERT_COPY: Readonly<Record<string, MessageId>> = {
  update_available: 'next.dashboard.updateAvailable', upload_budget_exhausted: 'next.dashboard.uploadAlert', state_permissions: 'next.dashboard.permissionsAlert',
  data_folder_readable: 'next.dashboard.permissionsAlert', storage_nearly_full: 'next.dashboard.storageAlert', backup_failed: 'next.dashboard.backupAlert',
  library_unreachable: 'next.dashboard.sourceAlert', remote_unreachable: 'next.dashboard.sourceAlert', secure_renewal_failed: 'next.dashboard.certificateAlert',
};

export type DashboardAlertWords = Readonly<{title: string; detail?: string; known: boolean;
  /** Fix permissions is offered: the server says it can fix this one (`action.kind` "fix-permissions"). With "none" it
   *  says why it cannot, in `detail`, and nothing is offered. */
  fixPermissions: boolean}>;

export function dashboardAlertWords(alert: DashboardAlert, t: Translate): DashboardAlertWords {
  const copy = ALERT_COPY[alert.code];
  const title = copy ? t(copy, {version: alert.params.latest ?? '', count: alert.count ?? 0}) : alert.params.message || t('next.dashboard.unknownAlert');
  const detail = copy ? alert.params.message || (alert.code === 'data_folder_readable' ? undefined : alert.params.reason) : undefined;
  const fixPermissions = alert.code === 'data_folder_readable' && alert.action.kind === 'fix-permissions' && !!alert.params.owner && !!alert.params.path;
  return Object.freeze({title, ...(detail ? {detail} : {}), known: !!copy, fixPermissions});
}
