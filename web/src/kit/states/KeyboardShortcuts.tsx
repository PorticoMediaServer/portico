import {Fragment, useRef, type RefObject} from 'react';
import {defaultI18n} from '@i18n';
import {Dialog} from '../overlays';
import {Icon, type IconName} from '../Icon';
import {keyboardShortcutSections, type ShortcutSection, type ShortcutSectionId} from './shortcuts';
import styles from './shortcuts.module.css';

export type KeyboardShortcutsProps = Readonly<{
  open: boolean;
  onOpenChange: (open: boolean, reason: 'close-button' | 'outside' | 'escape' | 'back' | 'action' | 'gesture') => void;
  skipBackSeconds: number;
  skipForwardSeconds: number;
  initialSection?: ShortcutSectionId;
  returnFocusRef?: RefObject<HTMLElement | null>;
}>;
const icons: Record<ShortcutSectionId, IconName> = {player: 'i-play', live: 'i-live', guide: 'i-guide', anywhere: 'i-kbd'};

function Section({section, headingRef}: {section: ShortcutSection; headingRef: RefObject<HTMLHeadingElement | null>}) {
  return <section className={styles.section} data-shortcut-section={section.id}>
    <h3 className={styles.heading} ref={headingRef} tabIndex={-1}><Icon name={icons[section.id]} size={18}/>{defaultI18n.t(section.labelKey)}</h3>
    <dl className={styles.list}>{section.rows.map(row => <Fragment key={row.id}>
      <dt>{row.keys.map((key, index) => <Fragment key={key}>
        {index > 0 && (row.separator ? <span className={styles.separator}>{defaultI18n.t(`next.states.key.${row.separator}`)}</span> : ' ')}
        <kbd>{defaultI18n.t(`next.states.key.${key}`)}</kbd>
      </Fragment>)}</dt><dd>{row.label}</dd>
    </Fragment>)}</dl>
  </section>;
}

export function KeyboardShortcuts({open, onOpenChange, skipBackSeconds, skipForwardSeconds, initialSection = 'player', returnFocusRef}: KeyboardShortcutsProps) {
  const player = useRef<HTMLHeadingElement>(null);
  const live = useRef<HTMLHeadingElement>(null);
  const guide = useRef<HTMLHeadingElement>(null);
  const anywhere = useRef<HTMLHeadingElement>(null);
  const refs = {player, live, guide, anywhere};
  const sections = keyboardShortcutSections(skipBackSeconds, skipForwardSeconds);
  return <Dialog open={open} onOpenChange={onOpenChange} title={<span className={styles.title}>{defaultI18n.t('next.states.shortcuts')}</span>}
    className={styles.dialog} width={1060} size="lg" initialFocusRef={refs[initialSection]} returnFocusRef={returnFocusRef}>
    <div className={styles.columns}>
      <div><Section section={sections[0]!} headingRef={player}/></div>
      <div>{sections.slice(1).map(section => <Section key={section.id} section={section} headingRef={refs[section.id]}/>)}</div>
    </div>
    <p className={styles.footer}><span>{defaultI18n.t('next.states.shortcut.settings')}</span><span>{defaultI18n.t('next.states.shortcut.omits')}</span></p>
  </Dialog>;
}
