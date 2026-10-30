import {defaultI18n} from '@i18n';
import type {SourceReadValues} from '@core/remote-sources.ts';
import type {SourceChoiceField} from '@core/remote-source-choice.ts';
import {SettingNumber} from '../../../kit/settings';
import styles from './source-choice-connection-rows.module.css';

const t = defaultI18n.t;

type ConnectionField = 'connections' | 'requestsPerSecond' | 'bytesPerSecond';
type ConnectionValues = Pick<SourceReadValues, ConnectionField>;

export type SourceChoiceConnectionMarkers = Readonly<{
  /** The rows this opening applies; the caller supplies the actual closed IDs. */
  applicable: readonly SourceChoiceField[];
  /** The rows whose current value differs from the chosen definition. */
  changed: readonly SourceChoiceField[];
  /** The caller's already-formatted baseline words for one row, or undefined. */
  value: (field: SourceChoiceField) => string | undefined;
  onReset?: (field: SourceChoiceField) => void;
}>;

export type SourceChoiceConnectionRowsProps = Readonly<{
  values: ConnectionValues;
  disabled: boolean;
  /** The actual displayed source name for the canonical help sentence, when the
   * original source really has one. No fallback name is invented here. */
  sourceName?: string;
  /** The caller's real phone decision (remote-sources.html#11b); never detected here. */
  phone?: boolean;
  onChange: (patch: Partial<ConnectionValues>) => void;
  markers?: SourceChoiceConnectionMarkers;
}>;

/** The three connection limits as the Connection-limits box draws them
 * (remote-sources.html#4b/#4c): one full-width row each, the existing drawn label
 * on the left and the unchanged SettingNumber on the right, with the authored
 * changed wash, bar and ring. Marker presentation only: the caller owns
 * applicability, the changed set, each formatted baseline string and the exact
 * callbacks, so this reads no comparison, command, read, state or clock. A row
 * outside the marker, or with no markers at all, draws the plain row. */
export function SourceChoiceConnectionRows({values, disabled, sourceName, phone = false, onChange, markers}: SourceChoiceConnectionRowsProps) {
  const on = (field: SourceChoiceField) => !!markers && markers.applicable.includes(field) && markers.changed.includes(field);
  const word = (field: SourceChoiceField) => (on(field) ? markers!.value(field) : undefined);
  const reset = (field: SourceChoiceField) => (markers?.onReset ? () => markers.onReset!(field) : undefined);
  const conn = word('connections'), rps = word('requestsPerSecond'), speed = word('bytesPerSecond');
  // The desktop rows carry the canonical long help sentence with the source's
  // own name; the phone rows carry the shorter published sentence. This
  // presenter supplies no local copy and no fallback name.
  const help = phone ? {
    connections: t('next.remoteSources.connectionsHelpPhone'),
    requests: t('next.remoteSources.requestsPerSecondHelpPhone'),
    speed: t('next.remoteSources.downloadSpeedHelpPhone'),
  } : sourceName ? {
    connections: t('next.remoteSources.connectionsHelp', {name: sourceName}),
    requests: t('next.remoteSources.requestsPerSecondHelp', {name: sourceName}),
    speed: t('next.remoteSources.downloadSpeedHelp', {name: sourceName}),
  } : undefined;
  return <div className={styles.rows} data-phone={phone || undefined}>
    <div className={styles.cell} data-changed={on('connections') || undefined}>
      <span className={styles.labelBlock}><span className={styles.label}>{t('next.remoteSources.connections')}</span>
        {help && <span className={styles.help}>{help.connections}</span>}</span>
      <div className={styles.control}>
        {conn !== undefined && <span className={styles.marker}>
          <span>{t('next.remoteSources.presetValue', {value: conn})}</span>
          {markers?.onReset && <button type="button" className={styles.reset} disabled={disabled} onClick={reset('connections')}>{t('action.reset')}</button>}
        </span>}
        <SettingNumber label={t('next.remoteSources.connections')} value={values.connections} min={1} max={32}
          unit={t('next.remoteSources.atOnce')} disabled={disabled}
          onCommit={value => {if (value !== null) onChange({connections: value});}}/>
      </div>
    </div>
    <div className={styles.cell} data-changed={on('requestsPerSecond') || undefined}>
      <span className={styles.labelBlock}><span className={styles.label}>{t('next.remoteSources.requestsPerSecond')}</span>
        {help && <span className={styles.help}>{help.requests}</span>}</span>
      <div className={styles.control}>
        {rps !== undefined && <span className={styles.marker}>
          <span>{t('next.remoteSources.presetValue', {value: rps})}</span>
          {markers?.onReset && <button type="button" className={styles.reset} disabled={disabled} onClick={reset('requestsPerSecond')}>{t('action.reset')}</button>}
        </span>}
        <SettingNumber label={t('next.remoteSources.requestsPerSecond')} value={values.requestsPerSecond} min={1} max={50}
          unit={t('next.remoteSources.aSecond')} disabled={disabled}
          onCommit={value => {if (value !== null) onChange({requestsPerSecond: value});}}/>
      </div>
    </div>
    <div className={styles.cell} data-changed={on('bytesPerSecond') || undefined}>
      <span className={styles.labelBlock}><span className={styles.label}>{t('next.remoteSources.downloadSpeed')}</span>
        {help && <span className={styles.help}>{help.speed}</span>}</span>
      <div className={styles.control}>
        {speed !== undefined && <span className={styles.marker}>
          <span>{t('next.remoteSources.presetValue', {value: speed})}</span>
          {markers?.onReset && <button type="button" className={styles.reset} disabled={disabled} onClick={reset('bytesPerSecond')}>{t('action.reset')}</button>}
        </span>}
        <SettingNumber label={t('next.remoteSources.downloadSpeed')} value={values.bytesPerSecond ? values.bytesPerSecond / 1_000_000 : null}
          min={1} max={1000} optional unit={t('next.remoteSources.megabytesPerSecond')} disabled={disabled}
          onCommit={value => onChange({bytesPerSecond: value === null ? 0 : Math.round(value) * 1_000_000})}/>
      </div>
    </div>
  </div>;
}
