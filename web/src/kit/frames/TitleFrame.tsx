import {useEffect, useRef, useState, type ReactNode} from 'react';
import {Button, ResumeButton, Tabs, type ChoiceItem} from '../controls';
import {Icon, type IconName} from '../Icon';
import {usePhone} from '../overlays/shared';
import {Skeleton} from '../states';
import {PageStateFrame, type FrameBack, type PageStateProps} from './frames';
import type {AboutValue} from './title-parts';
import styles from './title.module.css';

export type TitleAction = Readonly<{icon: IconName; label: string; onPress: () => void; disabled?: boolean;
  /** Shown with its label where there is room (from tablet width): Start over, My List, Shuffle. A round button on a phone. */
  labelled?: boolean;
  /** On (in My List, watched, a favourite): tinted in the accent (phone-detail d1 frame 3). */
  active?: boolean;
  /** Also on the collapsed bar, after the primary (desktop-detail f3: Play and +). */
  pinned?: boolean}>;
/** The one flexible button. With `progress` it is Resume. From tablet width the time left is beside its label, the
 * played part runs along its lower edge and Start over is the button after it (desktop-detail f2). On a phone, and on
 * a cover page from tablet width (desktop-listen book), the played part, the time left and Start over are one line
 * under the action row (phone-detail d1 frame 3). */
export type TitlePrimary = Omit<TitleAction, 'labelled' | 'pinned'> & Readonly<{progress?: number; remaining?: string; startOver?: Readonly<{label: string; onPress: () => void}>}>;
/** One part of the credits line: "Directed by" and the names; a name with `onPress` opens its page. */
export type TitleCredit = Readonly<{label: string; names: string | readonly AboutValue[]}>;
/** The line above the title (desktop-detail f1 "Dune collection · 2 films", f7 "Acting", desktop-listen "Album"). */
export type TitleKicker = Readonly<{label: string; icon?: IconName; detail?: string; onPress?: () => void}>;
/** A person's, an artist's, an album's or a book's own picture, beside the words in place of the art box. */
export type TitleCover = Readonly<{node: ReactNode; shape: 'poster' | 'square' | 'circle'}>;
export type TitleFrameProps = Readonly<{
  /** The picture behind the words, as the kit's `AmbientArt` with `treatment="title"`; it fades itself into the backdrop. */
  art?: ReactNode;
  /** In place of `art`: the cover beside the words (a poster or square at the left, a circle at the right). */
  cover?: TitleCover;
  /** A glass disc over the art up to tablet width. From 1024 the nav is the way back. */
  back: FrameBack;
  /** One glass disc opposite Back (Play on). */
  topAction?: TitleAction;
  kicker?: TitleKicker;
  title: string;
  /** The metadata line (`HeroFacts`). Its line is kept when there is nothing to show. */
  metadata?: ReactNode;
  /** The audio and subtitles for the next play (`TrackPicker`), under the synopsis on a phone and tablet. */
  picker?: ReactNode;
  /** The one flexible button: at phone width it takes the room the round buttons leave. A person has none. */
  primary?: TitlePrimary;
  /** The buttons after it, in order: labelled ones first, then round ones. One that the title doesn't support stays
   * in its slot with `disabled`. */
  actions?: readonly TitleAction[];
  /** One bold line over the synopsis, from 1024. */
  tagline?: string;
  synopsis?: string;
  /** A biography's Read more and where it is from ("Biography from TMDB"). */
  readMore?: Readonly<{label: string; onPress: () => void; note?: string}>;
  /** "Directed by" and "Starring", from 1024. */
  credits?: readonly TitleCredit[];
  /** The cast circles on the left page when the window is two pages (foldable f2). Elsewhere the cast is in Details. */
  cast?: ReactNode;
  tabs?: Readonly<{items: readonly ChoiceItem[]; value: string; onValueChange: (value: string) => void}>;
  /** The tab panel: the one flexible region. */
  children?: ReactNode;
  /** Beside the panel from 1024 (a collection's About, an album's Details); under it otherwise. */
  aside?: ReactNode;
  /** The bar that replaces the hero once it scrolls away. The frame sets it from the scroll; pass it to hold it. */
  collapsed?: boolean;
  state?: Readonly<{kind: 'loading'}> | PageStateProps;
}>;

/** Watches the tabs' top edge against the shell's bar: once they pass under it the collapsed bar shows. */
function useCollapsed(target: React.RefObject<HTMLElement | null>, forced?: boolean) {
  const [passed, setPassed] = useState(false);
  useEffect(() => {
    const element = target.current;
    if (!element || forced !== undefined || typeof IntersectionObserver === 'undefined') return;
    const top = parseFloat(getComputedStyle(element).getPropertyValue('--shell-top')) || 0;
    const observer = new IntersectionObserver(([entry]) => setPassed(!!entry && !entry.isIntersecting && entry.boundingClientRect.top < top + 1), {rootMargin: `-${top}px 0px 0px 0px`});
    observer.observe(element);
    return () => observer.disconnect();
  }, [target, forced]);
  return forced ?? passed;
}

const names = (value: TitleCredit['names']) => typeof value === 'string' ? value : value.map((name, index) => <span key={index}>{index > 0 && ', '}
  {name.onPress ? <button type="button" className={styles.link} onClick={name.onPress}>{name.text}</button> : name.text}</span>);

/** Frame 2, a title page: a film, show, season, episode, person, collection, album, artist or book. The regions are
 * `frameRegions.title` (packages/design): fixed are the art or cover, Back, the kicker, the title slot (the title sits
 * on its bottom edge and grows upward), the metadata line, the action row, the tabs and the collapsed bar; flexible are
 * the words and the tab panel. Forms: a phone and a tablet upright are one centred column; from 600 on its side the
 * window is two pages (the title, and the tabs with their panel); from 1024 the words sit over the art at the left. */
export function TitleFrame({art, cover, back, topAction, kicker, title, metadata, picker, primary, actions = [], tagline, synopsis, readMore, credits, cast, tabs, children, aside, collapsed, state}: TitleFrameProps) {
  const phone = usePhone(), failure = state && 'title' in state ? state : undefined, loading = !!state && !failure;
  const tabsTop = useRef<HTMLDivElement>(null), shown = useCollapsed(tabsTop, collapsed);
  const disc = (action: TitleAction) => <Button variant="glass" size="small" iconOnly icon={action.icon} aria-label={action.label} disabled={action.disabled} onClick={action.onPress} className={styles.disc} />;
  const backButton = disc({icon: 'i-left', label: back.label, onPress: back.onPress});
  if (failure) return <div className={styles.title}><div className={styles.top}>{backButton}</div><PageStateFrame {...failure} /></div>;
  const round = (action: TitleAction) => <Button key={`${action.label}:round`} variant="glass" phone={phone} iconOnly icon={action.icon} aria-label={action.label} title={action.label} aria-pressed={action.active} data-active={action.active || undefined} data-labelled={action.labelled || undefined} disabled={action.disabled || loading} onClick={action.onPress} className={styles.round} />;
  // A labelled action is round up to 1024 and labelled from there (CSS shows one of the two).
  const button = (action: TitleAction) => action.labelled ? [round(action), <Button key={action.label} variant="glass" icon={action.icon} aria-pressed={action.active} data-active={action.active || undefined} disabled={action.disabled || loading} onClick={action.onPress} className={styles.labelled}>{action.label}</Button>] : round(action);
  // While loading the action row keeps its buttons but says nothing it does not know yet (the time left).
  const resume = primary?.progress !== undefined && !loading;
  // Resume carries its played part from 1024 (desktop-detail f2); up to 1024, and on a cover page, the played part is the line under the row (CSS shows one).
  const plain = primary && <Button variant="primary" phone={phone} icon={primary.icon} disabled={primary.disabled || loading} onClick={primary.onPress} className={styles.primary} data-paired={resume && !cover || undefined}>{primary.label}</Button>;
  const lead = primary && <>{resume && !cover && <ResumeButton icon={primary.icon} progress={primary.progress!} remaining={primary.remaining} disabled={primary.disabled} onClick={primary.onPress} className={styles.resume}>{primary.label}</ResumeButton>}{plain}</>;
  const row = (primary || actions.length > 0) && <div className={styles.actions}>
    {lead}
    {primary?.startOver && !loading && <Button variant="glass" icon="i-restart" onClick={primary.startOver.onPress} className={styles.startOver}>{primary.startOver.label}</Button>}
    {actions.map(action => button(action))}
  </div>;
  const progress = resume && <div className={styles.progress}>
    <span role="progressbar" aria-valuemin={0} aria-valuemax={1} aria-valuenow={primary!.progress} aria-label={primary!.remaining}><i style={{width: `${primary!.progress! * 100}%`}} /></span>
    <b>{primary!.remaining}</b>
    {primary!.startOver && <Button variant="text" size="xs" icon="i-restart" onClick={primary!.startOver.onPress}>{primary!.startOver.label}</Button>}
  </div>;
  const words = loading ? <div className={styles.words} aria-hidden="true"><Skeleton line width="96%" /><Skeleton line width="70%" /></div>
    : (tagline !== undefined || synopsis !== undefined || credits?.length) ? <div className={styles.words}>
      {tagline !== undefined && <p className={styles.tagline}>{tagline}</p>}
      {synopsis !== undefined && <p className={styles.synopsis} data-read-more={readMore ? true : undefined}>{synopsis}</p>}
      {readMore && <p className={styles.readMore}><button type="button" onClick={readMore.onPress}>{readMore.label}<Icon name="i-chdown" size={16} /></button>{readMore.note !== undefined && <span>{readMore.note}</span>}</p>}
      {credits && credits.length > 0 && <p className={styles.credits}>{credits.map(credit => <span key={credit.label}>{credit.label} <b>{names(credit.names)}</b></span>)}</p>}
    </div> : null;
  const kickerLine = kicker && <p className={styles.kicker}>
    {kicker.onPress ? <button type="button" onClick={kicker.onPress}>{kicker.icon && <Icon name={kicker.icon} size={16} />}{kicker.label}</button> : <span>{kicker.icon && <Icon name={kicker.icon} size={16} />}{kicker.label}</span>}
    {kicker.detail !== undefined && <span className={styles.kickerDetail}>{kicker.detail}</span>}
  </p>;
  const pinned = actions.filter(action => action.pinned);
  return <div className={styles.title} data-cover={cover?.shape} data-collapsed={shown || undefined} data-panel={tabs ? undefined : 'bare'}>
    {/* The collapsed bar (desktop-detail f3; phone-detail d1 frame 2): sticky under the shell's bar, taking no room until it shows. */}
    <div className={styles.barSlot} aria-hidden={!shown} inert={!shown}>
      <div className={styles.bar}>
        <div className={styles.barRow}>
          <span className={styles.barBack}>{backButton}</span>
          <b className={styles.barTitle}>{title}</b>
          {tabs && <Tabs className={styles.barTabs} items={tabs.items} value={tabs.value} onValueChange={tabs.onValueChange} />}
          <span className={styles.barEnd}>
            {primary && <Button variant="primary" size="small" icon={primary.icon} disabled={primary.disabled || loading} onClick={primary.onPress}>{primary.label}</Button>}
            {pinned.map(action => <Button key={action.label} variant="glass" size="small" iconOnly icon={action.icon} aria-label={action.label} aria-pressed={action.active} disabled={action.disabled || loading} onClick={action.onPress} />)}
            {topAction && <span className={styles.barCast}>{disc(topAction)}</span>}
          </span>
        </div>
        {tabs && <Tabs className={styles.barTabsUnder} listClassName={styles.inset} phone items={tabs.items} value={tabs.value} onValueChange={tabs.onValueChange} />}
      </div>
    </div>
    <div className={styles.pages}>
      <div className={styles.page}>
        <header className={styles.head}>
          {cover ? <div className={styles.cover} data-shape={cover.shape}>{cover.node}</div> : <div className={styles.art}>{art}</div>}
          <div className={styles.top}>{backButton}{topAction && disc(topAction)}</div>
          <div className={styles.copy}>
            <div className={styles.ident}>
              {kickerLine}
              {loading && !title ? <Skeleton width="70%" height="var(--frame-hero-title-lockup)" /> : <h1 className={styles.lockup}>{title}</h1>}
              <div className={styles.meta}>{loading ? <Skeleton line small width="40%" /> : metadata}</div>
            </div>
            <div className={styles.body}>
              {row}
              {progress}
              {words}
              {picker && !loading && <div className={styles.picker}>{picker}</div>}
            </div>
          </div>
        </header>
        {cast && <div className={styles.cast}>{cast}</div>}
      </div>
      <div className={styles.page}>
        <div ref={tabsTop} className={styles.tabsTop} data-title-tabs-top="" />
        {tabs && <Tabs className={styles.tabs} listClassName={styles.inset} phone={phone} items={tabs.items} value={tabs.value} onValueChange={tabs.onValueChange} />}
        <div className={styles.content}>
          <div className={styles.panel}>{loading ? <div className={styles.skeletonRow} aria-hidden="true">{Array.from({length: 7}, (_, index) => <Skeleton key={index} className={styles.skeletonCard} />)}</div> : children}</div>
          {aside && !loading && <aside className={styles.aside}>{aside}</aside>}
        </div>
      </div>
    </div>
  </div>;
}
