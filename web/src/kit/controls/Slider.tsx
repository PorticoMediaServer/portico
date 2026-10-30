import {forwardRef, type ComponentPropsWithoutRef, type CSSProperties, type ElementRef} from 'react';
import {Slider as SliderPrimitive} from 'radix-ui';
import {rangeBounds, rangeValue} from './model';
import styles from './controls.module.css';

export type SliderProps = Omit<ComponentPropsWithoutRef<typeof SliderPrimitive.Root>, 'value' | 'defaultValue' | 'onValueChange' | 'onValueCommit' | 'orientation'> & {
  value?: number;
  defaultValue?: number;
  onValueChange?: (value: number) => void;
  onValueCommit?: (value: number) => void;
  variant?: 'player' | 'dock';
};

/** Web volume only; native platforms keep the operating system's volume controls. */
export const Slider = forwardRef<ElementRef<typeof SliderPrimitive.Root>, SliderProps>(function Slider({value, defaultValue = 100, min = 0, max = 100, step = 1, onValueChange, onValueCommit, variant = 'player', className, style, disabled, 'aria-label': label, 'aria-labelledby': labelledBy, 'aria-valuetext': valueText, ...props}, ref) {
  const {low, high, increment} = rangeBounds(min, max, step);
  return <SliderPrimitive.Root {...props} ref={ref} value={value === undefined ? undefined : [rangeValue(value, min, max, step)]}
    defaultValue={[rangeValue(defaultValue, min, max, step)]} min={low} max={high === low ? low + increment : high} step={increment} disabled={disabled || high === low}
    onValueChange={values => onValueChange?.(rangeValue(values[0], min, max, step))} onValueCommit={values => onValueCommit?.(rangeValue(values[0], min, max, step))}
    className={[styles.slider, className].filter(Boolean).join(' ')} data-variant={variant} style={style as CSSProperties}>
    <SliderPrimitive.Track className={styles.sliderTrack}><SliderPrimitive.Range className={styles.sliderFill} /></SliderPrimitive.Track>
    <SliderPrimitive.Thumb className={styles.sliderThumb} aria-label={label} aria-labelledby={labelledBy} aria-valuetext={valueText} aria-valuemin={low} aria-valuemax={high} />
  </SliderPrimitive.Root>;
});
