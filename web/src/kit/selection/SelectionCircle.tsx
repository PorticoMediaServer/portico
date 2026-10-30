import {forwardRef, type ButtonHTMLAttributes, type CSSProperties, type Ref, type HTMLAttributes} from 'react';
import {Icon} from '../Icon';
import {circleIntent, type SelectionIntent} from './interaction';
import styles from './selection.module.css';

export type SelectionCircleState = Readonly<{
  selected: boolean;
  selecting?: boolean;
  disabled?: boolean;
  accessibilityLabel: string;
  onSelectedChange?: (next: boolean, intent: SelectionIntent) => void;
  size?: number;
  variant?: 'card' | 'row';
}>;

export type SelectionCircleProps = SelectionCircleState & (
  | ({interactive?: true} & Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children' | 'aria-label' | 'aria-pressed' | 'onChange'>)
  | ({interactive: false} & Pick<HTMLAttributes<HTMLSpanElement>, 'id' | 'className' | 'style'>)
);

/** The owner positions this circle. Artwork rings, title colour and card activation stay with the card. */
export const SelectionCircle = forwardRef<HTMLButtonElement | HTMLSpanElement, SelectionCircleProps>(function SelectionCircle(props, ref) {
  const {selected, selecting = false, disabled = false, accessibilityLabel, onSelectedChange, size, variant = 'card', interactive = true, className, style, ...rest} = props;
  const circleStyle = {...style, ...(size === undefined ? {} : {'--selection-circle-size': `${size}px`})} as CSSProperties;
  const classes = [styles.circle, className].filter(Boolean).join(' ');
  const glyph = <Icon name="i-check" size={16} className={styles.circleCheck} />;
  if (!interactive) return <span ref={ref as Ref<HTMLSpanElement>} id={props.id} className={classes} style={circleStyle}
    aria-hidden="true" data-selected={selected} data-selecting={selecting} data-disabled={disabled} data-variant={variant}>{glyph}</span>;
  const button = rest as ButtonHTMLAttributes<HTMLButtonElement>;
  return <button {...button} ref={ref as Ref<HTMLButtonElement>} type={button.type ?? 'button'} className={classes} style={circleStyle}
    aria-label={accessibilityLabel} aria-pressed={selected} disabled={disabled} data-selected={selected} data-selecting={selecting} data-variant={variant}
    onClick={event => {
      button.onClick?.(event);
      if (event.defaultPrevented || disabled) return;
      event.stopPropagation();
      onSelectedChange?.(event.shiftKey ? true : !selected, circleIntent(selecting, event.shiftKey));
    }}>{glyph}</button>;
});
