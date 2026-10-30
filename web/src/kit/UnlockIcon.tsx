import {createElement} from 'react';
import {icons} from '@design';
import styles from './Icon.module.css';

/** The canonical registry has this glyph; the mockup sprite has no i-unlock. */
export function UnlockIcon({size = 24}: {size?: number}) {
  const icon = icons.unlock;
  return <svg className={styles.icon} width={size} height={size} viewBox="0 0 24 24" fill="none"
    stroke="currentColor" strokeWidth={icon.strokeWidth} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">
    {icon.elements.map(({type, ...props}, index) => createElement(type, {...props, key: index}))}
  </svg>;
}
