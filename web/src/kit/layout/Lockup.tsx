import type {CSSProperties, HTMLAttributes, ReactNode} from 'react';
import {typeScale, type TypeStyle} from '@design';
import styles from './layout.module.css';

export type LayoutPlatform = 'desktop' | 'phone-app' | 'phone-web' | 'tv';
export function layoutType(type: TypeStyle): CSSProperties {
  return {fontSize: type.size, fontWeight: type.weight, lineHeight: type.lineHeight === 'normal' ? 'normal' : `${type.lineHeight}px`, letterSpacing: `${type.tracking}em`};
}
export function platformType(platform: LayoutPlatform) { return typeScale[platform === 'tv' ? 'tenFoot' : platform === 'desktop' ? 'desktop' : 'handheld']; }

export type LockupProps = Omit<HTMLAttributes<HTMLHeadingElement>, 'children' | 'title'> & {
  title: ReactNode; /** Default: from the title's length (`longTitle`) when it is text. */ long?: boolean; lines?: number; platform?: LayoutPlatform; as?: 'h1' | 'h2' | 'h3';
};
/** A title set large is long past 20 characters and takes the long size (kit.css `.lockup.long`). */
export function longTitle(title: string): boolean { return [...title.trim()].length > 20; }
export function Lockup({title, long, lines, platform = 'desktop', as: Tag = 'h1', className, style, ...props}: LockupProps) {
  const type = platformType(platform);
  // A caller may say; otherwise a text title decides by its own length, so every page sizes it alike.
  long ??= typeof title === 'string' && longTitle(title);
  const clamp: CSSProperties = lines ? {display: '-webkit-box', WebkitBoxOrient: 'vertical', WebkitLineClamp: lines, overflow: 'hidden'} : {};
  return <Tag {...props} className={[styles.lockup, className].filter(Boolean).join(' ')} data-long={long || undefined}
    style={{...layoutType(long ? type.lockupLong : type.lockup), ...clamp, ...style}}>{title}</Tag>;
}
