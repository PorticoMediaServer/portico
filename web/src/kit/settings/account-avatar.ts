import type {CSSProperties} from 'react';

/** The approved dashboard.js username hash and six canonical gradient pairs. */
export function accountAvatarStyle(username: string): CSSProperties {
  let hash = 2166136261;
  for (let index = 0; index < username.length; index++) {
    hash ^= username.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  const palette = (hash >>> 0) % 6 + 1;
  return {background: `linear-gradient(135deg, var(--color-account-avatar-${palette}-start), var(--color-account-avatar-${palette}-end))`};
}
