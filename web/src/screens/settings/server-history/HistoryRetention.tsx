import type {ReactNode} from 'react';
import type {SettingsHolder, SettingsSection} from '@core/presentation/settings-spec.ts';
/** The canonical retention row keeps its existing runtime binding, explicit
 * Save/Discard, visibility and Advanced disclosure. The history table owns
 * its independent bounded reads and receives no retention state. */
export function HistoryRetention({section, renderRows}: {section: SettingsSection; renderRows: (holder: SettingsHolder) => ReactNode}) {
  if (!section.advanced) return null;
  const groups = (section.advanced.groups ?? []).map(group => ({...group, rows: group.rows.filter(row => row.id === 'history.keepHistory')})).filter(group => group.rows.length);
  return renderRows({advanced: {...section.advanced, groups}});
}
