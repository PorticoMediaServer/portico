import {useEffect, useId, useMemo, useRef, useState, useSyncExternalStore, type KeyboardEvent, type PointerEvent} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import {Icon} from '../Icon';
import {WaveGlyph} from './WaveGlyph';
import {eqFrequencies, equalizerResponse, parseEqualizer, type EqualizerSettings} from '../../platform/playback/audio/equalizer';
import type {EqualizerPort} from './equalizer-port';
import styles from './equalizer.module.css';

export type MediaTranslate = (key: MessageId, values?: Record<string, string | number>) => string;
export type EqualizerProps = Readonly<{
  value: EqualizerSettings;
  onChange(value: EqualizerSettings): void | Promise<void>;
  onReset(): void | Promise<void>;
  reportError(error: unknown): void;
  layout?: 'settings' | 'panel' | 'phone' | 'tv';
  /** Known renderer/output rate only. Unknown rate gives no invented response curve. */
  sampleRate?: number;
  /** Only hosts which can actually offer bit-perfect output show the drawn help line. */
  bitPerfectOutputOffered?: boolean;
  onBack?: () => void;
  onOpenSettings?: () => void;
  onRail?: () => void;
  t?: MediaTranslate;
  formatNumber?: typeof defaultI18n.number;
}>;
const labels = ['31', '62', '125', '250', '500', '1k', '2k', '4k', '8k', '16k'];
const clamp = (value: number) => Math.max(-12, Math.min(12, Math.round(value * 2) / 2));
const position = (value: number) => (12 - value) / 24 * 100;

/** The same controlled EQ at each drawn size; persistence belongs to the supplied app port. */
export function Equalizer({value, onChange, onReset, reportError, layout = 'settings', sampleRate, bitPerfectOutputOffered = false, onBack, onOpenSettings, onRail, t = defaultI18n.t, formatNumber = defaultI18n.number}: EqualizerProps) {
  const id = useId(), [dragging, setDragging] = useState<number | null>(null);
  const knobs = useRef<(HTMLButtonElement | null)[]>([]), toggle = useRef<HTMLButtonElement>(null), reset = useRef<HTMLButtonElement>(null);
  const lastBand = useRef(0), leftControl = useRef<'switch' | 'reset'>('switch'), draft = useRef(value);
  const [display, setDisplay] = useState(value);
  useEffect(() => { if (layout === 'tv') toggle.current?.focus(); }, [layout]);
  useEffect(() => { if (dragging === null) { draft.current = value; setDisplay(value); } }, [value, dragging]);
  const number = (n: number) => `${n > 0 ? '+' : n < 0 ? '−' : ''}${formatNumber(Math.abs(n), {minimumFractionDigits: 1, maximumFractionDigits: 1})}`;
  const db = (n: number) => t('next.media.eq.db', {value: number(n)});
  const change = (next: EqualizerSettings) => {
    draft.current = parseEqualizer(next); setDisplay(draft.current);
    try { void Promise.resolve(onChange(draft.current)).catch(reportError); } catch (error) { reportError(error); }
  };
  const set = (index: number, gain: number) => {
    if (!draft.current.enabled) return;
    const current = draft.current, bounded = clamp(gain);
    if (index === 0) change({...current, preampDb: bounded});
    else change({...current, bandsDb: current.bandsDb.map((v, i) => i === index - 1 ? bounded : v)});
  };
  const key = (event: KeyboardEvent<HTMLButtonElement>, index: number, gain: number) => {
    const amount = event.shiftKey ? 3 : 0.5;
    if (layout === 'tv' && (event.key === 'ArrowLeft' || event.key === 'ArrowRight')) {
      event.preventDefault();
      if (event.key === 'ArrowLeft' && index === 0) (leftControl.current === 'reset' && !reset.current?.disabled ? reset.current : toggle.current)?.focus();
      else knobs.current[Math.max(0, Math.min(10, index + (event.key === 'ArrowLeft' ? -1 : 1)))]?.focus();
      return;
    }
    const next = event.key === 'Home' ? -12 : event.key === 'End' ? 12 : ['ArrowUp', 'ArrowRight'].includes(event.key) ? gain + amount : ['ArrowDown', 'ArrowLeft'].includes(event.key) ? gain - amount : undefined;
    if (next !== undefined) { event.preventDefault(); set(index, next); }
  };
  const pointer = (event: PointerEvent<HTMLButtonElement>, index: number, horizontal: boolean) => {
    const bounds = event.currentTarget.parentElement!.getBoundingClientRect();
    set(index, horizontal ? (event.clientX - bounds.left) / bounds.width * 24 - 12 : 12 - (event.clientY - bounds.top) / bounds.height * 24);
  };
  const response = useMemo(() => {
    if (!sampleRate || sampleRate <= 62) return undefined;
    const upper = Math.min(16000, sampleRate / 2 - 1), hz = Array.from({length: 241}, (_, i) => 31 * (upper / 31) ** (i / 240));
    // Ruling17: the curve is the true combined band-filter response. Preamp has its own
    // slider and is excluded here; the engine's net response still includes its gain.
    return equalizerResponse({...display, preampDb: 0}, sampleRate, hz).map((gain, i) => `${i === 0 ? 'M' : 'L'}${i / 240 * 1000},${position(gain) * 10}`).join(' ');
  }, [display, sampleRate]);
  const band = (index: number, gain: number, label: string, horizontal = false) => <div className={[styles.band, index === 0 && styles.preamp].filter(Boolean).join(' ')} data-dragging={dragging === index || undefined} key={index}>
    <span className={styles.value}>{horizontal ? label : number(gain)}</span>
    <span className={styles.track} data-horizontal={horizontal || undefined}>
      <i className={styles.fill} style={horizontal ? {left: `${Math.min(50, 100 - position(gain))}%`, width: `${Math.abs(gain) / 24 * 100}%`} : {top: `${Math.min(50, position(gain))}%`, height: `${Math.abs(gain) / 24 * 100}%`}} />
      <button type="button" ref={element => { knobs.current[index] = element; }} className={styles.knob} disabled={!display.enabled} role="slider" aria-label={index === 0 ? label : t(index < 6 ? 'next.media.eq.hz' : 'next.media.eq.khz', {frequency: index < 6 ? eqFrequencies[index - 1]! : eqFrequencies[index - 1]! / 1000})}
        aria-valuemin={-12} aria-valuemax={12} aria-valuenow={gain} aria-valuetext={db(gain)} aria-orientation={horizontal ? 'horizontal' : 'vertical'}
        style={horizontal ? {left: `${100 - position(gain)}%`} : {top: `${position(gain)}%`}}
        onKeyDown={event => key(event, index, gain)} onDoubleClick={() => set(index, 0)} onFocus={() => { lastBand.current = index; }}
        onPointerDown={event => { if (event.button !== 0 || !display.enabled) return; event.preventDefault(); event.currentTarget.focus(); event.currentTarget.setPointerCapture(event.pointerId); setDragging(index); pointer(event, index, horizontal); }}
        onPointerMove={event => { if (event.currentTarget.hasPointerCapture(event.pointerId)) pointer(event, index, horizontal); }}
        onPointerUp={event => { if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId); setDragging(null); }}
        onPointerCancel={() => setDragging(null)} onLostPointerCapture={() => setDragging(null)} />
      {dragging === index && <span className={styles.bubble} style={horizontal ? {left: `${100 - position(gain)}%`} : {top: `${position(gain)}%`}}>{number(gain)}</span>}
      {layout === 'tv' && <><span className={`${styles.nudge} ${styles.up}`} style={{top: `${position(gain)}%`}} aria-hidden="true"><Icon name="i-up" size={22} /></span><span className={`${styles.nudge} ${styles.down}`} style={{top: `${position(gain)}%`}} aria-hidden="true"><Icon name="i-down" size={22} /></span></>}
    </span>
    <span className={styles.frequency}>{horizontal ? db(gain) : label}</span>
  </div>;
  const leftKeys = (event: KeyboardEvent<HTMLButtonElement>, from: 'switch' | 'reset') => {
    if (layout !== 'tv') return;
    if (event.key === 'ArrowRight' && display.enabled) { event.preventDefault(); knobs.current[lastBand.current]?.focus(); }
    else if (event.key === 'ArrowDown' && from === 'switch' && !reset.current?.disabled) { event.preventDefault(); reset.current?.focus(); }
    else if (event.key === 'ArrowUp' && from === 'reset') { event.preventDefault(); toggle.current?.focus(); }
    else if (event.key === 'ArrowLeft' && onRail) { event.preventDefault(); onRail(); }
  };
  const resetButton = <button type="button" className={styles.reset} ref={reset} disabled={!display.enabled || display.preampDb === 0 && display.bandsDb.every(v => v === 0)}
    onFocus={() => { leftControl.current = 'reset'; }} onKeyDown={event => leftKeys(event, 'reset')}
    onClick={() => { try { void Promise.resolve(onReset()).catch(reportError); } catch (error) { reportError(error); } }}>{t('next.media.eq.reset')}</button>;
  return <section className={styles.equalizer} data-layout={layout} data-enabled={display.enabled} aria-label={t('next.media.eq.title')}>
    <div className={styles.heading}>
      {onBack && <button type="button" className={styles.back} data-window-no-drag="" onClick={onBack} aria-label={t('next.media.eq.backQueue')}><Icon name="i-left" size={22} /></button>}
      <div className={styles.label}><b>{t('next.media.eq.title')}</b>{layout !== 'panel' && <span className={styles.scope}>{t('preferences.thisDevice')}</span>}
        {bitPerfectOutputOffered && layout !== 'panel' && layout !== 'tv' && <small>{t('next.media.eq.help')}</small>}</div>
      <button type="button" className={styles.switch} ref={toggle} data-window-no-drag="" role="switch" aria-checked={display.enabled} aria-label={t('next.media.eq.title')}
        onFocus={() => { leftControl.current = 'switch'; }} onKeyDown={event => leftKeys(event, 'switch')} onClick={() => change({...draft.current, enabled: !draft.current.enabled})} />
    </div>
    {layout === 'tv' && resetButton}
    {layout !== 'panel' && layout !== 'tv' && <h3 className={styles.sectionTitle}>{t('next.media.eq.bands')}</h3>}
    <div className={styles.graph} role="group" aria-label={t('next.media.eq.bandsLabel')}>
      {layout === 'phone' && <div className={styles.horizontal}>{band(0, display.preampDb, t('next.media.eq.preamp'), true)}</div>}
      <div className={styles.grid}>
        {layout !== 'phone' && <><div className={styles.scale}>{[12, 6, 0, -6, -12].map(gain => <span key={gain} style={{top: `${position(gain)}%`}}>{gain === 0 ? t('next.media.eq.db', {value: 0}) : `${gain > 0 ? '+' : '−'}${Math.abs(gain)}`}</span>)}</div>{band(0, display.preampDb, t('next.media.eq.preamp'))}<div className={styles.divider} /></>}
        <div className={styles.bands}>
          <i className={styles.zero} />
          {response && <svg className={styles.curve} viewBox="0 0 1000 1000" preserveAspectRatio="none" aria-hidden="true"><defs><linearGradient id={id} x1="0" y1="0" x2="0" y2="1"><stop offset="0" stopColor="currentColor" stopOpacity=".22" /><stop offset="1" stopColor="currentColor" stopOpacity=".04" /></linearGradient></defs><path d={`${response} L1000,500 L0,500 Z`} fill={`url(#${id})`} /><path d={response} fill="none" stroke="currentColor" strokeOpacity=".75" strokeWidth="2" vectorEffect="non-scaling-stroke" /></svg>}
          {display.bandsDb.map((gain, i) => band(i + 1, gain, layout === 'settings' ? t(i < 5 ? 'next.media.eq.hz' : 'next.media.eq.khz', {frequency: i < 5 ? eqFrequencies[i]! : eqFrequencies[i]! / 1000}) : labels[i]!))}
        </div>
      </div>
    </div>
    {layout === 'panel' && bitPerfectOutputOffered && <p className={styles.note}><WaveGlyph size={18} />{t('next.media.eq.help')}</p>}
    {layout !== 'tv' && <footer className={styles.footer}>{resetButton}{layout === 'panel' && onOpenSettings && <button type="button" className={styles.reset} onClick={onOpenSettings}>{t('next.media.eq.settings')}</button>}</footer>}
  </section>;
}

/** Mounts only after the app's real saved record is read; it never creates a session EQ. */
export function DeviceEqualizer({port, ...props}: Omit<EqualizerProps, 'value' | 'onChange' | 'onReset'> & {port: EqualizerPort}) {
  useEffect(() => port.start(), [port]);
  const value = useSyncExternalStore(port.subscribe, port.getSnapshot, () => null);
  return value && <Equalizer {...props} value={value} onChange={port.change} onReset={port.reset} />;
}
