import {defaultI18n, type MessageId} from '@i18n';
import type {SourceReadPreset} from '@core/remote-sources.ts';
import type {SourceChoiceComparison, SourceChoiceDefinition} from '@core/remote-source-choice.ts';
import {Button} from '../../../kit/controls';
import {Icon} from '../../../kit/Icon';
import styles from './source-choice-summary.module.css';

const t = defaultI18n.t;

/** The four drawn choice names, exactly the authored spec labels (remote-sources.html#4). */
const presetLabels: Readonly<Record<SourceReadPreset, MessageId>> = {
  list: 'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.0.label',
  once: 'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.1.label',
  everything: 'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.2.label',
  ask: 'next.settingsSpec.server.remote-sources.panels.source.tabs.1.groups.0.rows.0.control.options.3.label',
};

export type SourceChoiceSummaryProps = Readonly<{
  /** The caller's coherent exact pair: `changedFields` belongs to this very opening. */
  comparison: SourceChoiceComparison;
  /** The caller's exact recorded name and version, which is also the reset target. */
  definition: SourceChoiceDefinition;
  disabled: boolean;
  /** The caller's own phone decision (remote-sources.html#11c). Never detected here. */
  phone?: boolean;
  onChooseStart?: () => void;
  onResetAll?: () => void;
}>;

/** The authored Custom line (remote-sources.html#4d tiles 4 and 6, and the phone
 * row #11c). Presentation only: it reads the two records its caller already
 * matched and never looks up a baseline, a definition list or a command. A pair
 * with no changed rows draws nothing. The caller's own wrapper owns the surface
 * (.box on the source page, or the wizard tile's inline radius and background);
 * this row adds no decoration of its own. */
export function SourceChoiceSummary({comparison, definition, disabled, phone = false, onChooseStart, onResetAll}: SourceChoiceSummaryProps) {
  const changes = comparison.changedFields.length;
  if (!changes) return null;
  const choice = t(presetLabels[definition.name]);
  if (phone) return <div className={styles.phoneRow}>
    <span className={styles.phoneText}>
      <b>{t('next.remoteSources.custom')}</b>
      <small>{t('next.remoteSources.startedFrom', {choice})}. {t('next.remoteSources.changeCount', {count: changes})}.</small>
    </span>
    {onResetAll && <Button size="xs" variant="text" disabled={disabled} onClick={onResetAll}>{t('next.remoteSources.resetAll')}</Button>}
  </div>;
  return <div className={styles.presetRow}>
    <span className={styles.icon} aria-hidden="true"><Icon name="i-sliders" size={22}/></span>
    <div className={styles.text}>
      <b>{t('next.remoteSources.custom')} <span className={styles.tag}>{t('next.remoteSources.changeCount', {count: changes})}</span></b>
      <p>{t('next.remoteSources.startedFrom', {choice})}</p>
    </div>
    {(onChooseStart || onResetAll) && <div className={styles.actions}>
      {onChooseStart && <Button size="xs" variant="glass" disabled={disabled} onClick={onChooseStart}>{t('next.remoteSources.chooseDifferentStart')}</Button>}
      {onResetAll && <Button size="xs" variant="glass" disabled={disabled} onClick={onResetAll}>{t('next.remoteSources.resetAll')}</Button>}
    </div>}
  </div>;
}
