import {useEffect, useState, type ReactNode} from 'react';
import {LibraryHeader} from '../browse';
import {Button} from '../controls';
import {BottomSheet} from '../overlays';
import {usePhone} from '../overlays/shared';
import {PushedNavBar} from '../shell';
import {PageStateFrame, type FrameBack, type PageStateProps} from './frames';
import styles from './newframes.module.css';

/** Round 8: the kinds of screen that had no frame (Search, Live TV, a page with a second pane, the reader), with the
 * native frames' names and regions (packages/design `frameRegions`). FRAMES.md is the brief. */

export type SearchNone = Readonly<{title: string; body?: string; action?: ReactNode}>;
export type SearchFrameProps = Readonly<{
  /** Phone width, before anything is typed (phone-app 7). Wider, the shell's Search is the page's name. */
  title?: string;
  /** The search field: the kit `TextField` (`variant="large-search"` from 600, `"search"` at phone width). */
  field: ReactNode;
  /** Phone width, while there is text: Cancel beside the field (phone-app 8). */
  cancel?: ReactNode;
  /** The scope chips with their counts, on one line that scrolls sideways at phone width and wraps wider. */
  scopes?: ReactNode;
  /** One line over the results when part of a search ran late (desktop-search s-top). */
  notice?: ReactNode;
  /** No matches: a large line and what to try (desktop-search s-none). */
  none?: SearchNone;
  /** The one flexible region: Recent searches and browsing; or the Top result and the results by kind; or Browse instead. */
  children?: ReactNode;
}>;

/** Search (desktop-search, phone-app 7 and 8, final-mobileweb-2). Fixed: the title at phone width, the field with Cancel,
 * the scope chips. Flexible: what is under them. */
export function SearchFrame({title, field, cancel, scopes, notice, none, children}: SearchFrameProps) {
  const phone = usePhone();
  return <div className={`${styles.frame} ${styles.search}`}>
    <div className={styles.searchHead}>
      {title !== undefined && phone && <h1 className={styles.pageTitle}>{title}</h1>}
      <div className={styles.searchField}><div>{field}</div>{cancel}</div>
      {scopes && <div className={styles.chips} role="group">{scopes}</div>}
    </div>
    <div className={styles.rows}>
      {notice && <div className={styles.edge}>{notice}</div>}
      {none && <div className={`${styles.edge} ${styles.none}`}><h2 className={styles.pageTitle}>{none.title}</h2>{none.body && <p className={styles.intro}>{none.body}</p>}{none.action}</div>}
      {children}
    </div>
  </div>;
}

export type FramePane = Readonly<{title: string; eyebrow?: ReactNode; subtitle?: string; close: Readonly<{label: string; onPress: () => void}>; children: ReactNode; footer?: ReactNode}>;
export type SplitFrameProps = Readonly<{children: ReactNode; pane?: FramePane}>;

/** A page and a second pane on it (desktop-live live-sheet and live-rules, tablet 11b, phone-guide g3): the programme's
 * details beside the guide, a rule beside the rules, Downloads' Rules beside the list. From 600 the pane is a raised
 * column on the right that stays under the nav while the page scrolls, and the page takes the rest; at phone width it is
 * a bottom sheet over the page. */
export function SplitFrame({children, pane}: SplitFrameProps) {
  const phone = usePhone();
  if (phone) return <>{children}{pane && <BottomSheet open onOpenChange={open => {if (!open) pane.close.onPress();}} title={<PaneHead pane={pane} sheet/>} accessibilityLabel={pane.title}
    showCloseButton={false} footer={pane.footer}><div className={styles.paneBody}>{pane.children}</div></BottomSheet>}</>;
  return <div className={styles.split} data-pane={pane ? true : undefined}>
    <div className={styles.splitPage}>{children}</div>
    {pane && <aside className={styles.pane} aria-label={pane.title}>
      <PaneHead pane={pane}/>
      <div className={styles.paneBody}>{pane.children}</div>
      {pane.footer && <div className={styles.paneFoot}>{pane.footer}</div>}
    </aside>}
  </div>;
}
function PaneHead({pane, sheet = false}: {pane: FramePane; sheet?: boolean}) {
  return <div className={styles.paneHead}>
    <div>{pane.eyebrow}<h2 className={sheet ? styles.rowHeading : styles.paneTitle}>{pane.title}</h2>{pane.subtitle && <p className={styles.paneSubtitle}>{pane.subtitle}</p>}</div>
    {!sheet && <Button variant="glass" size="small" icon="i-x" iconOnly aria-label={pane.close.label} onClick={pane.close.onPress}/>}
  </div>;
}

export type LiveFrameProps = Readonly<{
  title: string;
  /** Phone width: Recordings as its own page, with Back to Live TV and the title in the bar (phone-app 12). */
  back?: FrameBack;
  switcher?: ReactNode;
  /** From 1024: the source's name, its channels and how far the guide reaches, at the title row's end. */
  facts?: ReactNode;
  /** The title row's own buttons: a phone's List | Guide and Recordings. */
  actions?: ReactNode;
  /** Guide | Recordings from 600 (kit `Tabs`); a phone's Recordings page has its views as tabs. */
  tabs?: ReactNode;
  /** The channel groups, or Recordings' views (with the storage line at their end). */
  chips?: ReactNode;
  /** From 1024: On now, before the guide. */
  lead?: ReactNode;
  /** The day, the 2 h steps and Now, the sort, Grid | List. */
  toolbar?: ReactNode;
  /** The one flexible region: the grid, the channel list, Upcoming, Recorded, Series rules. */
  children?: ReactNode;
  details?: FramePane;
  /** The keyboard line under the guide. */
  hints?: ReactNode;
  state?: PageStateProps;
}>;

/** Live TV (desktop-live, phone-guide, phone-app 10 to 12, final3-mobileweb-05): the guide, the channel list and
 * Recordings on one frame. Fixed: the library-style header (title, source picker, facts; the tabs under it), the chips and
 * the toolbar. Flexible: the guide or the list. The details pane is `SplitFrame`'s. The header is the kit `LibraryHeader`,
 * as the live Recordings page (`kit/dvr` RecordingsLayout) draws it; Stage 2 moves those pages onto this frame. */
/** A short, wide window: a phone on its side (600 wide or more and under 600 tall, the guide's own rule). LiveFrame draws its
 * one-row form there; the screen hands it that form's content (the view switch and Recordings, the chips, the sort). */
export function useShortWide() {
  const query = '(min-width: 600px) and (max-height: 599px) and (orientation: landscape)';
  const [short, setShort] = useState(() => typeof window !== 'undefined' && window.matchMedia(query).matches);
  useEffect(() => {
    const media = window.matchMedia(query), update = () => setShort(media.matches);
    media.addEventListener('change', update); update();
    return () => media.removeEventListener('change', update);
  }, []);
  return short;
}

export function LiveFrame({title, back, switcher, facts, actions, tabs, chips, lead, toolbar, children, details, hints, state}: LiveFrameProps) {
  const phone = usePhone(), short = useShortWide() && !back;
  // A phone on its side keeps the guide's height: the title, the view switch, the chips and the toolbar share one row, and the
  // tabs, facts and On now give way (the Recordings button among the actions stays).
  if (short) return <SplitFrame pane={details}><div className={`${styles.frame} ${styles.live}`}>
    <header className={styles.liveRow}><h1 className={styles.rowTitle}>{title}</h1>{actions}{chips && <div className={styles.chips} role="group">{chips}</div>}{toolbar}</header>
    {state ? <PageStateFrame {...state}/> : <div className={styles.rows}>{children}{hints && <div className={`${styles.edge} ${styles.hints}`}>{hints}</div>}</div>}
  </div></SplitFrame>;
  // The tab strip's rule runs the page's width; its tabs start at the gutter (as TitleFrame's do).
  const strip = tabs && <div className={styles.tabs}>{tabs}</div>;
  const header = back && phone ? <><div className={styles.pushed}><PushedNavBar safeArea={false} backLabel={back.label} title={title} onBack={back.onPress}/></div>{strip}</>
    : phone ? <header className={styles.liveHead}><h1 className={styles.pageTitle}>{title}</h1>{actions}</header>
    : <LibraryHeader name={title} switcher={switcher} menu={(facts || actions) && <div className={styles.liveEnd}>{facts}{actions}</div>}>{strip}</LibraryHeader>;
  return <SplitFrame pane={details}><div className={`${styles.frame} ${styles.live}`}>
    {header}
    {(chips || phone && switcher) && <div className={styles.chips} role="group">{phone && !back && switcher}{chips}</div>}
    {state ? <PageStateFrame {...state}/> : <div className={styles.rows}>
      {lead}
      {toolbar && <div className={`${styles.edge} ${styles.toolbar}`}>{toolbar}</div>}
      {children}
      {hints && <div className={`${styles.edge} ${styles.hints}`}>{hints}</div>}
    </div>}
  </div></SplitFrame>;
}

/** The reader's frame is the kit's `ReaderChrome` (kit/reader): its bar (Back, title and author), its tools (contents,
 * search, bookmark, Aa, full screen, More), the page, the page arrows from 1024, the progress bar (chapter, pages, the
 * chapter-marked scrubber, time left), the panel (contents, bookmarks, highlights, search: docked beside the page from
 * 600, a sheet at phone width) and the display settings (`ReaderDisplayPopover` from 1024, `ReaderDisplaySheet`). Every
 * reader draws it, and the native apps draw it too, inside the reading guest's web view (`screens/read/native/entry.tsx`).
 * The name is the frame's; the part stays where the reader's own styles are. */
export {ReaderChrome as ReaderFrame, type ReaderChromeProps as ReaderFrameProps} from '../reader/ReaderChrome';
