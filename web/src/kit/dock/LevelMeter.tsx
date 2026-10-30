import styles from './dock.module.css';

/** No clock or ambient animation: the approved playing indicator holds still. */
export function LevelMeter({variant = 'web', className}: {variant?: 'web' | 'tv' | 'tv-large'; className?: string}) {
  return <span aria-hidden="true" className={[styles.meter, className].filter(Boolean).join(' ')} data-variant={variant}><i /><i /><i />{variant === 'web' && <i />}</span>;
}
