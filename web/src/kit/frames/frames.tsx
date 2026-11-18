import type {ReactNode} from 'react';
import {AuthError, AuthStage} from '../accounts';
import type {ArtworkSource} from '../artwork';
import {Button, Tabs, type ChoiceItem} from '../controls';
import type {IconName} from '../Icon';
import {Hero, type HeroCandidate, type HeroProps} from '../layout';
import {Dialog, type OverlayChange} from '../overlays';
import {usePhone} from '../overlays/shared';
import {Skeleton, StateLayout, type StateAction, type StateDetails, type StateRetry, type StateTone} from '../states';
import {PlayerControl} from '../player';
import {Icon} from '../Icon';
import {ArtBox} from './parts';
import styles from './frames.module.css';

/** The screen frames on the web (design/ui-v1 `system-frames.html`), with the native frames' names and props.
 * A screen picks one and fills its slots; the frame owns the header, gutters, rhythm and page-level states, the
 * shell owns the nav and tab bar around it, and the window (kit/backdrop.css) owns the backdrop. FRAMES.md is the brief. */

export type FrameBack = Readonly<{label: string; onPress: () => void}>;
function Back({back}: {back: FrameBack}) {
  return <Button variant="text" size="small" icon="i-left" className={styles.back} onClick={back.onPress}>{back.label}</Button>;
}

export type PageHeaderProps = Readonly<{
  title: string;
  /** A page that was pushed: Back on its own line, named after the page it returns to. */
  back?: FrameBack;
  /** Header actions (Filters, sort), beside the title. */
  trailing?: ReactNode;
}>;
/** Rule 3, the one header of a page that is not a title page and not in a sheet. The title wraps and is never cut. */
function PageHeader({title, back, trailing}: PageHeaderProps) {
  return <header className={styles.header} data-back={back ? true : undefined}>{back && <Back back={back} />}<div className={styles.headerRow}><h1 className={styles.pageTitle}>{title}</h1>{trailing}</div></header>;
}

export type PageStateProps = Readonly<{
  icon: IconName; tone?: StateTone; title: string;
  /** One sentence: the flexible region. */
  body?: string;
  primaryAction?: StateAction; secondaryAction?: StateAction;
  retry?: StateRetry;
  /** The owner-only Details block. */
  details?: StateDetails;
}>;
/** Frame 9: the whole page can't load, or is waiting the first time. Once per screen, in place of the page. */
export function PageStateFrame({primaryAction, secondaryAction, ...state}: PageStateProps) {
  return <StateLayout {...state} primary={primaryAction} secondary={secondaryAction} />;
}

export type BrowseFrameProps = Readonly<{
  /** The rows or the grid: the one flexible region. Each child is a kit `Shelf` (it draws its own heading) or a grid;
   * the frame spaces them. A row with no items is left out whole. */
  children?: ReactNode;
  /** The whole page is loading for the first time, or can't load: drawn in place of the rows. */
  state?: Readonly<{kind: 'loading'}> | PageStateProps;
}> & (
  /** A destination or a pushed list: its title is the header. */
  | Readonly<{header: PageHeaderProps; hero?: never}>
  /** Home: the hero leads, under the shell's nav. `null` while Home has nothing to feature. */
  | Readonly<{hero: ReactNode; header?: never}>);

/** Frame 1, Browse: Home, a library, Live TV, Saved, Search results. */
export function BrowseFrame({header, hero, state, children}: BrowseFrameProps) {
  const failure = state && 'title' in state ? state : undefined;
  return <div className={`${styles.frame} ${styles.browse}`} data-hero={hero ? true : undefined}>
    {header ? <PageHeader {...header} /> : hero ?? <div className={styles.browseTop} />}
    {failure ? <PageStateFrame {...failure} /> : <div className={styles.browseRows}>{state ? <RowsSkeleton /> : children}</div>}
  </div>;
}
/** Loading holds the loaded page's boxes (rule 12): two rows, each a heading and a line of cards at their real size. */
function RowsSkeleton() {
  const row = (width: string, ratio: string, sheen: boolean) => <div className={styles.skeletonRow}><Skeleton quiet line width="var(--card-poster-width)" />
    <div className={styles.skeletonCards}>{Array.from({length: 9}, (_, index) => <Skeleton quiet key={index} width={width} sheen={sheen && index === 0} style={{aspectRatio: ratio}} />)}</div></div>;
  return <div aria-hidden="true" className={styles.browseRows}>{row('var(--card-wide-width)', '16 / 9', true)}{row('var(--card-poster-width)', '2 / 3', false)}</div>;
}

/** What a screen may say about Home's hero: what to feature and how to draw its art and buttons. Never a size. */
export type HomeHeroProps<T extends HeroCandidate> = Pick<HeroProps<T>, 'candidates' | 'activeIndex' | 'onIndexChange' | 'active' | 'visible'
  | 'renderArtwork' | 'renderThumbnail' | 'renderActions' | 'renderMetadata' | 'renderReason' | 'titleLink' | 'onArtworkError' | 'onArtworkReady'>;
/** Rule 14, the moderate hero, for `BrowseFrame`'s `hero`: the kit's Hero (its cross-fade and drift unchanged) at the rule's fixed sizes. */
export function HomeHero<T extends HeroCandidate>(props: HomeHeroProps<T>) {
  return <Hero {...props} moderate platform={usePhone() ? 'phone-web' : 'desktop'} />;
}

export type SheetBarProps = Readonly<{
  title: string;
  /** Named after the page it returns to. */
  back?: FrameBack;
  /** The bar's one text action (Done, Save). */
  action?: Readonly<{label: string; onPress: () => void; disabled?: boolean}>;
}>;
/** Rule 3, the compact bar, 56 high: Back, the title centred at 17/700, one text action. */
function SheetBar({title, back, action}: SheetBarProps) {
  return <header className={styles.sheetBar}>{back ? <Back back={back} /> : <span />}<h1 className={styles.barTitle}>{title}</h1>
    {action ? <Button variant="text" size="small" disabled={action.disabled} onClick={action.onPress}>{action.label}</Button> : <span />}</header>;
}

export type SheetFrameProps = Readonly<{
  open: boolean; onOpenChange: OverlayChange; title: string;
  /** The rows or a short form: the flexible region. The sheet grows to its limit, then they scroll. */
  children: ReactNode;
  /** The actions, held at the bottom. */
  footer?: ReactNode;
}>;
/** Frame 5 and rule 2: menus, pickers, short tasks. A bottom sheet at phone width, a dialog wider. A plain raised
 * surface with its corners, grab and scrim; it takes no background or header of the content's own. */
export function SheetFrame({open, onOpenChange, title, footer, children}: SheetFrameProps) {
  return <Dialog open={open} onOpenChange={onOpenChange} title={title} footer={footer} showCloseButton={false}>{children}</Dialog>;
}

export type SettingsListFrameProps = Readonly<{
  /** The bar over the list at phone width (Settings, Done). */
  bar: SheetBarProps;
  /** The list: search, the Account groups, then the Server band. */
  list: ReactNode;
  /** The selected page, a `SettingsPageFrame`. At phone width it takes the list's place. */
  page?: ReactNode;
}>;
/** Frame 3, the index of Settings. From 600: the list in its own sticky column beside the page. At phone width: the
 * list under its compact bar, or the selected page in its place. */
export function SettingsListFrame({bar, list, page}: SettingsListFrameProps) {
  return <div className={`${styles.frame} ${styles.settingsFrame}`}>
    <div className={styles.settingsMaster} data-covered={page ? true : undefined}><div className={styles.settingsBar}><SheetBar {...bar} /></div>{list}</div>
    <div className={styles.settingsDetail}>{page}</div>
  </div>;
}

export type SettingsPageFrameProps = Readonly<{
  title: string;
  /** Back to the list, at phone width; named after the page it returns to. */
  back?: FrameBack;
  /** The compact bar's one text action, at phone width (Done, Save). */
  action?: SheetBarProps['action'];
  /** Beside the title from 600: Saving or Saved. */
  status?: ReactNode;
  /** One short paragraph under the header. */
  intro?: string;
  /** Sections of rows (`SectionCard`): the one flexible region. */
  children?: ReactNode;
  /** The whole page can't load. One section failing is a `StateRow` in that section's card instead. */
  state?: PageStateProps;
}>;
/** Frame 4, one settings or owner page, or a form. At phone width its header is the compact bar; from 600 the page
 * title in the column beside the list, with the intro at most 720 wide. */
export function SettingsPageFrame({title, back, action, status, intro, children, state}: SettingsPageFrameProps) {
  return <div className={`${styles.frame} ${styles.page}`}>
    <div className={styles.pageBar}><SheetBar title={title} back={back} action={action} /></div>
    <header className={styles.pageHead}><h1 className={styles.pageTitle}>{title}</h1><span role="status" className={styles.rowHelp}>{status}</span></header>
    {state ? <PageStateFrame {...state} /> : <>{intro !== undefined && <p className={styles.intro}>{intro}</p>}{children}</>}
  </div>;
}

export type SignInFrameProps = Readonly<{
  /** A page after the first has Back above its title. */
  back?: FrameBack;
  title: string;
  /** One line under the title. */
  lede?: string;
  /** Said above the fields; the fields and the primary button under them keep their order. */
  error?: string;
  /** The form: fields, then the primary button, then lesser choices. The one flexible region. */
  children: ReactNode;
}>;
/** Frame 6, the pages before you're in. The kit's `AuthStage` owns the wordmark, the column and its inset, and
 * Terms and Privacy at the bottom; this names the slots a sign-in page fills. */
export function SignInFrame({back, title, lede, error, children}: SignInFrameProps) {
  return <AuthStage>{back && <Back back={back} />}<h1>{title}</h1>{lede !== undefined && <p>{lede}</p>}<div className={styles.signInForm}><AuthError message={error} />{children}</div></AuthStage>;
}

/** The time read-outs; the frame places them for the form (under the scrubber, either side of it, or as one clock). */
export type PlayerReadouts = Readonly<{position: string; /** With a minus sign: the time left. */ remaining?: string; duration?: string; /** "Ends 4:48 PM". */ endsAt?: string; /** Said in place of the time left. */ live?: string}>;
export type PlayerChromeFrameProps = Readonly<{
  /** The video surface, or the frozen frame when playback failed. */
  picture: ReactNode;
  /** Whether the controls show; the player decides when they hide. */
  chromeVisible: boolean;
  /** Close (a `PlayerControl onPicture`), top left. */
  close: ReactNode;
  /** `PlayerTitle`, beside Close. Not drawn on an upright phone, where the watch page under the picture has the title. */
  title?: ReactNode;
  /** The buttons opposite Close (`PlayerControl onPicture`): Play on, picture in picture, settings. */
  tools?: ReactNode;
  /** The scrubber (`SeekLine`). */
  scrubber: ReactNode;
  times: PlayerReadouts;
  /** Previous, back, Play or Pause, forward, next (`PlayerControl`s). In the middle of the bottom bar from tablet
   * width; in the middle of the picture on a phone. */
  transport: ReactNode;
  /** The bar's left end (Episodes) and right end (the options: `PlayerPill`s, volume, full screen). */
  leading?: ReactNode; trailing?: ReactNode;
  /** `PlayerPrompt`: Skip intro, Up next. The prompt corner, bottom right above the bar; it stays when the chrome hides. */
  prompts?: ReactNode;
  /** `PlayerUpNext`, over the credits: it sits alone in the picture's bottom right corner and the controls hide while
   * it shows (desktop-player 4, phone-player 6). */
  card?: ReactNode;
  /** One of the player's own panels, open (`PlayerOptions`, `PlayerEpisodes`). `panelCovers` says what it lies over,
   * which hides: `bar` for the Episodes drawer (desktop-player 6), `all` on a phone on its side (phone-player 3). */
  panel?: ReactNode; panelCovers?: 'bar' | 'all';
  /** A phone: the finger is on the scrubber, so the transport in the middle of the picture makes way for the preview. */
  scrubbing?: boolean;
  /** An upright phone only: the watch page under the picture (the title page's own content). */
  body?: ReactNode;
}>;
/** Frame 7, the video player's chrome, in its three drawn forms. From tablet width (desktop-player, tablet-play): the
 * picture fills the window; top bar, then a bar of scrubber, read-outs and a row of left end, transport, right end.
 * A phone on its side (phone-player 1 and 2): the same, with the transport in the middle of the picture and the
 * read-outs either side of the scrubber. An upright phone (phone-player 4): the picture is a 16:9 strip with the
 * chrome inside it, the scrubber on its lower edge, and the watch page below. No backdrop under the picture; every
 * region is fixed; the parts that fill it are `kit/player`'s. */
export function PlayerChromeFrame({picture, chromeVisible, close, title, tools, scrubber, times, transport, leading, trailing, prompts, card, panel, panelCovers, scrubbing, body}: PlayerChromeFrameProps) {
  const covered = card ? 'all' : panel ? panelCovers : undefined, shown = chromeVisible && covered !== 'all';
  const right = times.live ? <b className={styles.playerLive}>{times.live}</b> : <b>{times.remaining}</b>;
  return <div className={`${styles.frame} ${styles.player}`} data-chrome={shown} data-covered={covered} data-scrubbing={scrubbing || undefined}>
    <div className={styles.playerStage}>
      <div className={styles.playerPicture}>{picture}</div>
      <div className={styles.playerShade} />
      <div className={styles.playerTop} inert={!shown}>{close}<div className={styles.playerTitle}>{title}</div><div className={styles.playerTools}>{tools}</div></div>
      <div className={styles.playerBottom}>
        <div className={styles.playerPrompts}>{!card && !panel && prompts}</div>
        <div className={styles.playerBar} inert={!shown || covered === 'bar'}>
          <div className={styles.playerSeek}>{scrubber}</div>
          <span className={styles.playerPosition}><b>{times.position}</b></span>
          <span className={styles.playerRemaining}><span className={styles.playerEnds}>{times.endsAt}</span>{right}</span>
          <span className={styles.playerClock}>{times.live ? <b className={styles.playerLive}>{times.live}</b> : <><b>{times.position}</b>{times.duration !== undefined && ` / ${times.duration}`}</>}</span>
          <div className={styles.playerLeading}>{leading}</div>
          <div className={styles.playerCentre}>{transport}</div>
          <div className={styles.playerTrailing}>{trailing}</div>
        </div>
      </div>
      {card}{panel}
    </div>
    {body !== undefined && <div className={styles.playerBody}>{body}</div>}
  </div>;
}

/** One control in a fixed slot. A control the item doesn't support keeps its slot with `disabled`, dimmed. */
export type AudioControl = Readonly<{icon: IconName; label: string; onPress: () => void; disabled?: boolean; active?: boolean; /** Back or forward: the seconds it skips, drawn in the arrow. */ skip?: number}>;
/** A bottom-row slot. Music's are a glyph over its label, or `inline` with the glyph beside it (the device). An
 * audiobook's are tiles: a short `value` ("1.25×", "28 min") or a glyph over the label, outlined in the accent when `active`. */
export type AudioOption = Omit<AudioControl, 'icon'> & Readonly<{icon?: IconName; value?: string; inline?: boolean}>;
export type AudioPlayerFrameProps = Readonly<{
  /** Close (the down arrow) and, opposite it, More. Between them, where this is playing from. */
  close: AudioControl; more: AudioControl; context?: string;
  /** What it is playing from, said strongly after `context`: under it at phone width, beside it from 1024. */
  source?: string;
  /** The cover. With none, the placeholder with `icon` (music or book) fills the same box. */
  artwork?: ArtworkSource | null; icon: IconName;
  title: string; byline?: string;
  /** The heart at the end of the title row. */
  favourite?: AudioControl;
  /** An audiobook: the chapter line over the scrubber ("Chapter 8" ... "of 19"). */
  chapter?: Readonly<{label: string; of: string}>;
  /** The player's scrubber; the frame gives it a fixed box. */
  scrubber: ReactNode;
  /** Five fixed slots; the middle one is Play or Pause. */
  transport: readonly [AudioControl, AudioControl, AudioControl, AudioControl, AudioControl];
  /** What plays next ("Next" / "Scared To Live"). Its line is kept, blank, when there is nothing to say. */
  next?: Readonly<{label: string; title: string}>;
  /** Three or four fixed slots. */
  options: readonly AudioOption[];
  /** From 1024: the queue, the one flexible region there. It scrolls. */
  upNext?: ReactNode;
  loading?: boolean;
}>;
/** Frame 8, now playing for music and audiobooks (rule 12). Every region is placed from the frame's edges, so a
 * missing cover, a long title or a missing control never moves the scrubber, the transport or the bottom row. */
export function AudioPlayerFrame({close, more, context, source, artwork, icon, title, byline, favourite, chapter, scrubber, transport, next, options, upNext, loading = false}: AudioPlayerFrameProps) {
  const tiles = options.some(item => item.value !== undefined);
  return <div className={`${styles.frame} ${styles.audio}`}>
    <div className={styles.audioTop}>
      <Button variant="icon" size="small" icon={close.icon} aria-label={close.label} onClick={close.onPress} />
      <p className={styles.audioContext}><span className={`${styles.rowHelp} ${styles.oneLine}`}>{loading ? '' : context}</span>{source !== undefined && !loading && <b className={styles.oneLine}>{source}</b>}</p>
      <Button variant="icon" size="small" icon={more.icon} aria-label={more.label} disabled={more.disabled || loading} onClick={more.onPress} />
    </div>
    <div className={styles.audioArtSpace}><div className={styles.audioArt}>{loading ? <Skeleton width="100%" height="100%" sheen /> : <ArtBox kind="square" source={artwork} icon={icon} alt={title} />}</div></div>
    <div className={styles.audioWords}><div>{loading ? <><Skeleton line width="64%" /><Skeleton line small width="40%" /></> : <>
      <h1 className={styles.clamp2}>{title}</h1><p className={`${styles.intro} ${styles.oneLine}`}>{byline}</p>
    </>}</div>{favourite && !loading && <Button variant="icon" size="small" icon={favourite.icon} aria-label={favourite.label} aria-pressed={favourite.active} onClick={favourite.onPress} />}</div>
    <div className={styles.audioScrubber}>
      {chapter && <p className={styles.audioChapter}><Icon name="i-list" size={16} /><span className={`${styles.rowLabel} ${styles.oneLine}`}>{loading ? '' : chapter.label}</span><span className={styles.rowHelp}>{loading ? '' : chapter.of}</span></p>}
      {scrubber}
    </div>
    <div className={styles.audioTransport}>{transport.map((item, index) => <PlayerControl key={item.label} icon={item.icon} label={item.label} primary={index === 2} skip={item.skip} active={item.active} disabled={item.disabled || loading} onPress={item.onPress} />)}</div>
    <p className={`${styles.rowHelp} ${styles.oneLine} ${styles.audioNext}`}>{next && !loading && <>{next.label}  <b>{next.title}</b></>}</p>
    <div className={styles.audioOptions} data-tiles={tiles || undefined}>{options.map(item => <Button key={item.label} variant="text" className={`${styles.audioOption} ${item.active ? styles.audioActive : ''}`} data-inline={item.inline && !tiles || undefined} aria-pressed={item.active} disabled={item.disabled || loading} onClick={item.onPress}>
      {item.value !== undefined ? <b className={styles.rowLabel}>{item.value}</b> : item.icon && <Icon name={item.icon} />}<small>{item.label}</small></Button>)}</div>
    {upNext !== undefined && <div className={styles.audioUpNext}>{upNext}</div>}
  </div>;
}
