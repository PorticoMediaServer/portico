import type {ReactNode} from 'react';
import {Artwork, PersonCircle, nameInitials, type ArtworkSource} from '../artwork';
import styles from './cover-hero.module.css';

/** A link-like name in the byline. `person` draws the name with its initials in a small circle
 * before it, as an author is drawn (desktop-read 3, phone-read 3). */
export function CoverHeroLink({onOpen, href, person, picture, children}: Readonly<{onOpen?: () => void;
  /** A real address, so the link can be opened in a new tab; a plain click still opens it here. */
  href?: string;
  person?: string;
  /** A small round picture before the name instead of initials (an album's own cover beside its artist). */
  picture?: ArtworkSource;
  children: ReactNode}>) {
  const content = <>{picture ? <span className={styles.face} aria-hidden="true"><Artwork source={picture} alt="" fit="cover" width="100%" height="100%"/></span>
    : person && <PersonCircle className={styles.face} size={28} initialsSize={11} initials={nameInitials(person)} alt=""/>}{children}</>;
  if (href) return <a className={styles.link} href={href} onClick={event => {
    if (!onOpen || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    event.preventDefault(); onOpen();
  }}>{content}</a>;
  return onOpen ? <button type="button" className={styles.link} onClick={onOpen}>{content}</button> : <span className={styles.link}>{content}</span>;
}
