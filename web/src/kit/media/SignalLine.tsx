import {forwardRef, type ButtonHTMLAttributes} from 'react';
import {formatSignalText, musicSignalAccessibleName} from '@core/music-signal.ts';
import {Icon} from '../Icon';
import {WaveGlyph} from './WaveGlyph';
import type {MediaLayout, SignalPathModel, SignalTranslate} from './types';
import styles from './media.module.css';

export type SignalLineProps = Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children' | 'translate'> & Readonly<{model: SignalPathModel; translate: SignalTranslate; layout?: MediaLayout}>;
export const SignalLine = forwardRef<HTMLButtonElement, SignalLineProps>(function SignalLine({model, translate, layout = 'desktop', className, ...props}, ref) {
  const source = layout === 'desktop' ? model.source : model.shortSource ?? model.source;
  const sourceText = typeof source === 'string' ? source : source ? formatSignalText(source, translate) : undefined;
  const state = model.state ? formatSignalText(model.state, translate) : undefined;
  const foldedSource = model.foldSourceIntoState ? model.state?.values?.source : undefined;
  const emphasis = typeof foldedSource === 'string' ? foldedSource : typeof foldedSource === 'object' ? formatSignalText(foldedSource, translate) : undefined;
  const at = state && emphasis ? state.indexOf(emphasis) : -1;
  const showSource = sourceText && !model.foldSourceIntoState;
  if (!showSource && !state && !model.stateLoss) return null;
  return <button {...props} type="button" ref={ref} className={[styles.signal, className].filter(Boolean).join(' ')} data-layout={layout} data-healthy={model.healthy || undefined} aria-label={musicSignalAccessibleName(model, translate)}>
    <WaveGlyph size={layout === 'tv' ? 26 : layout === 'phone' ? 14 : 16} />
    {showSource && <b>{sourceText}</b>}
    {showSource && state && <i className={styles.signalGap} />}
    {state && <span className={styles.signalState}>{at < 0 || !emphasis ? state : <>{state.slice(0, at)}<b>{emphasis}</b>{state.slice(at + emphasis.length)}</>}</span>}
    {model.stateLoss && <><i className={styles.signalGap} /><span className={styles.signalState}>{formatSignalText(model.stateLoss, translate)}</span></>}
    {layout !== 'phone' && <Icon name="i-right" size={layout === 'tv' ? 20 : 14} className={styles.chevron} />}
  </button>;
});
