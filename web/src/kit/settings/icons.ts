import {nextIcons, type IconName} from '../../../../packages/design/src/next-icons';

/** settings.html iconName: strip one prefix, look it up, otherwise use sliders. */
export function settingsIconName(value: unknown): IconName {
  const name = String(value || '').replace(/^i-/, '');
  const id = `i-${name}`;
  return Object.hasOwn(nextIcons, id) ? id as IconName : 'i-sliders';
}
