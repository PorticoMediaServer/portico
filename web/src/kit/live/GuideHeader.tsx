import {Chip} from '../controls';
import {Icon, type IconName} from '../Icon';
import {usePhone} from '../overlays/shared';
import styles from './live.module.css';

/** The guide's quiet pickers (phone-guide g1, final3-phone-07): a glyph, a strong label, its value and a chevron, drawn as text,
 * not as a button: "Today  Wed, Oct 21 ⌄" and "By number ⌄". The control is the whole line. */
export function GuidePicker({icon, label, value, onPress}: Readonly<{icon?: IconName; label: string; value?: string; onPress: () => void}>) {
  return <button type="button" className={styles.picker} onClick={onPress}>
    {icon && <Icon name={icon} size={18}/>}<b>{label}</b>{value !== undefined && <span>{value}</span>}<Icon name="i-chdown" size={16}/>
  </button>;
}

export type GuideGroup = Readonly<{id: string; label: string; count?: number; icon?: IconName}>;
/** The channel groups as chips: All, Favorites, News … At phone width their names only (phone-guide g1); wider, each says its
 * count, Favorites with its star (desktop-live live-guide). */
export function GuideGroups({groups, value, onValueChange}: Readonly<{groups: readonly GuideGroup[]; value: string; onValueChange: (id: string) => void}>) {
  const phone = usePhone();
  return <>{groups.map(group => <Chip key={group.id} variant="search" selected={group.id === value} count={phone ? undefined : group.count} icon={phone ? undefined : group.icon} onClick={() => onValueChange(group.id)}>{group.label}</Chip>)}</>;
}
