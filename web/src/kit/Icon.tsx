import type {SVGProps} from 'react';
import {nextIcons, nextIconSprite, type IconName} from '../../../packages/design/src/next-icons';
import styles from './Icon.module.css';

export type {IconName};
export type IconProps = Omit<SVGProps<SVGSVGElement>, 'children' | 'name' | 'viewBox' | 'role' | 'aria-hidden'> & {
  name: IconName;
  size?: number;
};

/** Mount once per HTML document; portals in the same document share this sprite. */
export function IconSprite() {
  return <svg className={styles.sprite} width={0} height={0} aria-hidden="true" focusable="false">
    <defs dangerouslySetInnerHTML={{__html: nextIconSprite}} />
  </svg>;
}

/** Labels come from the caller's i18n catalogue; otherwise the glyph is decorative. */
export function Icon({name, size = 24, width = size, height = size, className, ...props}: IconProps) {
  const labelled = Boolean(props['aria-label'] || props['aria-labelledby']);
  return <svg {...props} className={[styles.icon, className].filter(Boolean).join(' ')} width={width} height={height}
    viewBox={nextIcons[name].viewBox} role={labelled ? 'img' : undefined} aria-hidden={labelled ? undefined : true} focusable="false">
    <use href={`#${name}`} />
  </svg>;
}
