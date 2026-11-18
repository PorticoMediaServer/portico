/**
 * R9, overlays by form: the one table that says which kind of overlay each task takes on each form, what its bar holds
 * and how its foot buttons are laid out. The kit overlays in both apps read it (web `kit/overlays`, `kit/frames`
 * SheetFrame; native the same); a screen names the TASK, never the kind.
 *
 * Drawn from: phone-detail 9-12 (menus and File info as sheets with "title subtitle · Done"), live-owner 13a-c (a task
 * sheet: "Cancel · centred title", Back + primary or one full-width action at the foot), select 22 (a confirmation:
 * equal halves, the destructive one a solid red fill), phone-app 4b and 18 (a short choice drops from its trigger over a
 * dimmed page), phone-app 18-21 (a settings page pushed inside the Settings sheet: "‹ Settings · centred title · action"),
 * tablet f4 and tablet-play f2 (side panels: title, a round × at the top right, real buttons at the foot), tablet f9 and
 * desktop-detail f2 (a card's menu is a popover beside the card), desktop-detail f9b and desktop-listen now-states
 * (dialogs: actions at the end, primary last), desktop-detail f10 (File info as a side panel), desktop-search menu and
 * notif (the account menu and Notifications drop from the top bar).
 *
 * Rules every row keeps: an anchored overlay opens below (or beside) its trigger and leaves the trigger visible; a modal
 * one dims the page; foot buttons are equal halves or full width on a phone and never hug the left edge.
 */
import type {FrameForm} from './frames.ts';

/** `sheet` from the bottom; `fullSheet` from 62 below the top; `dialog` centred; `side` a panel on the right edge;
 * `popover` anchored to its trigger; `page` not an overlay at all (the frame draws it in the page). */
export type OverlayFormKind = 'sheet' | 'fullSheet' | 'dialog' | 'side' | 'popover' | 'page';
/** What the bar's three slots hold. `title` sits at the start unless `centre` is set; `subtitle` is the item a menu or
 * File info is about ("Dune: Part Two"), grey, beside or under the title. */
export type OverlayBar = Readonly<{
  /** The start slot: Cancel (a task you can abandon), Back named after the page it returns to, or nothing. */
  leading: 'cancel' | 'back' | 'none';
  /** The title centred between leading and trailing (a pushed page or a task sheet), or at the start. */
  title: 'centre' | 'start' | 'none';
  /** The end slot: Done (accent), the task's own action (Save, Add), the round close ×, or nothing. */
  trailing: 'done' | 'action' | 'close' | 'none';
  subtitle: boolean;
}>;
/** `halves`: two buttons of equal width across the foot (Cancel | the action; Back | Add source). `full`: one button the
 * foot's width (Show 19 movies). `end`: at their own width at the end of the foot, primary last (a pointer dialog).
 * `none`: the overlay commits as you choose (a menu, a picker). `hints`: TV, where Back closes and the foot says so with
 * key hints ("OK Choose", "Back Close") in place of buttons. */
export type OverlayFoot = 'halves' | 'full' | 'end' | 'none' | 'hints';
/** `width`: a TV side sheet's width, drawn per task (detail-tv-5d File info 900, 8b Play a different version 760, menus and
 * pickers 640). */
export type OverlayForm = Readonly<{kind: OverlayFormKind; modal: boolean; bar: OverlayBar; foot: OverlayFoot; width?: number}>;

/** The tasks. A screen names one of these; anything that is none of them is a page. */
export type OverlayTask =
  /** An item's More or long-press menu (Mark as watched, File info, Delete…). */
  | 'actions'
  /** Pick one value from a short list: a select's open list, the library switcher, sort order. */
  | 'choice'
  /** A question before an action: Delete this recording?, Sign out? */
  | 'confirm'
  /** A short form: Add source, Save as playlist, Invite someone. */
  | 'task'
  /** Read-only facts about an item: File info, Playback info. */
  | 'details'
  /** Play a different version: one two-line row per version that plays it at once. */
  | 'versions'
  /** Narrowing a list: the library's Filters. */
  | 'filters'
  /** Settings: the index and a page. */
  | 'settings'
  /** The profile and account menu under the avatar. */
  | 'account'
  /** The Notifications list under the bell. */
  | 'notifications';

const bar = (leading: OverlayBar['leading'], title: OverlayBar['title'], trailing: OverlayBar['trailing'], subtitle = false): OverlayBar => ({leading, title, trailing, subtitle});
const form = (kind: OverlayFormKind, modal: boolean, overlayBar: OverlayBar, foot: OverlayFoot, width?: number): OverlayForm => ({kind, modal, bar: overlayBar, foot, width});
/** The TV side sheet's width for a menu, a picker or a short task (detail-tv 5c, tv-more 23). */
const tvSide = 640;
/** Nothing in the bar: a popover has at most a quiet heading of its own content. */
const quiet = bar('none', 'none', 'none');

/** The TV side sheet's bar: an eyebrow (what it is about) over the title, at the start. TV never draws Cancel or Done in a bar. */
const tvBar = bar('none', 'start', 'none', true);

/** Task x form. The TV column (detail-tv 5c, 5d, tv-focus 4, 9, 21, 38, 43, tv-more 23): a menu, a picker or a short
 * task is the right-hand side sheet (640) over the dimmed page, with an eyebrow, a title, full-width rows and key hints at
 * its foot; Filters is the side panel with Clear all and Show N; a confirmation is a centred dialog, Cancel first; File
 * info is a full-height side sheet with Done; the profile menu is the one anchored TV menu, a flyout right of the rail;
 * Settings is a page. No mock-up draws the Notifications list on TV (it is a row in the profile menu): the side sheet. */
export const overlayForms: Readonly<Record<OverlayTask, Readonly<Record<FrameForm, OverlayForm>>>> = {
  actions: {
    phone: form('sheet', true, bar('none', 'start', 'done', true), 'none'),
    medium: form('popover', true, bar('none', 'start', 'none', true), 'none'),
    /** A pointer menu (desktop-detail f2) leaves the page undimmed. */
    expanded: form('popover', false, bar('none', 'start', 'none', true), 'none'),
    tv: form('side', true, tvBar, 'hints', tvSide),
  },
  choice: {
    phone: form('popover', true, quiet, 'none'),
    medium: form('popover', true, quiet, 'none'),
    expanded: form('popover', false, quiet, 'none'),
    tv: form('side', true, tvBar, 'hints', tvSide),
  },
  confirm: {
    phone: form('sheet', true, bar('none', 'start', 'none'), 'halves'),
    medium: form('dialog', true, bar('none', 'start', 'none'), 'end'),
    expanded: form('dialog', true, bar('none', 'start', 'none'), 'end'),
    tv: form('dialog', true, bar('none', 'start', 'none'), 'end'),
  },
  task: {
    /** live-owner 13a-c: "Cancel · centred title"; the action (or Back and the action) at the foot. */
    phone: form('sheet', true, bar('cancel', 'centre', 'none'), 'full'),
    medium: form('side', true, bar('none', 'start', 'close'), 'halves'),
    expanded: form('dialog', true, bar('none', 'start', 'close'), 'end'),
    tv: form('side', true, bar('none', 'start', 'none'), 'hints', tvSide),
  },
  details: {
    phone: form('sheet', true, bar('none', 'start', 'done', true), 'none'),
    medium: form('side', false, bar('none', 'start', 'close', true), 'none'),
    expanded: form('side', false, bar('none', 'start', 'close', true), 'none'),
    tv: form('side', true, tvBar, 'full', 900),
  },
  /** detail-phone 9b (a sheet: the title, the film under it, Done), desktop-detail 9b (a centred dialog over the dimmed
   * page: the title, the film under it, a round x, no foot), detail-tv-8b (the side sheet, OK Play and Back Close). */
  versions: {
    phone: form('sheet', true, bar('none', 'start', 'done', true), 'none'),
    medium: form('dialog', true, bar('none', 'start', 'close', true), 'none'),
    expanded: form('dialog', true, bar('none', 'start', 'close', true), 'none'),
    tv: form('side', true, tvBar, 'hints', 760),
  },
  filters: {
    phone: form('sheet', true, bar('none', 'start', 'done'), 'halves'),
    medium: form('side', false, bar('none', 'start', 'close'), 'halves'),
    expanded: form('side', false, bar('none', 'start', 'close'), 'halves'),
    tv: form('side', true, bar('none', 'start', 'none'), 'halves', tvSide),
  },
  settings: {
    /** The index: "Settings · Done". A page pushed inside it: "‹ Settings · centred title · its action". */
    phone: form('fullSheet', true, bar('back', 'centre', 'done'), 'none'),
    medium: form('page', false, quiet, 'none'),
    expanded: form('page', false, quiet, 'none'),
    tv: form('page', false, quiet, 'none'),
  },
  account: {
    phone: form('sheet', true, bar('none', 'start', 'done'), 'none'),
    medium: form('popover', true, quiet, 'none'),
    expanded: form('popover', true, quiet, 'none'),
    tv: form('popover', false, quiet, 'none'),
  },
  notifications: {
    /** On a phone Notifications is a page pushed inside the Settings sheet (phone-app 21): "‹ Settings · Notifications · Mark all read". */
    phone: form('fullSheet', true, bar('back', 'centre', 'action'), 'none'),
    medium: form('popover', true, bar('none', 'start', 'action'), 'none'),
    expanded: form('popover', true, bar('none', 'start', 'action'), 'none'),
    /** No TV mock-up draws the list; it opens from the rail's profile menu (tv-more 10), so it is a side sheet like every list a TV menu opens. */
    tv: form('side', true, tvBar, 'hints', tvSide),
  },
};

/** The form a task takes on a form. */
export function overlayForm(task: OverlayTask, frameForm: FrameForm): OverlayForm {
  return overlayForms[task][frameForm];
}

/**
 * R12, feedback by form: where a toast stands and its shape. Drawn from desktop-browse (with Undo: a card centred 36 above
 * the bottom edge, padding 14/14/14/22, gap 22, the action an accent text button 36 high), desktop-player (the same card
 * 24 in from the start, above the controls), desktop-listen (a confirmation with no action: a pill 40 high at the top
 * centre, radius 20, padding 0 16) and phone-player (the card the window's width less 8 each side, above the bar or the
 * player's controls, padding 10/8/10/14, the action a tinted button 36 high). TV keeps its own toast (`feedback.toast`,
 * top trailing): `null`.
 */
export type ToastCard = Readonly<{
  radius: number;
  /** Top, end, bottom, start. */
  padding: readonly [number, number, number, number];
  gap: number;
  /** The card spans the window less this on each side (a phone); `null`: it takes its content's width. */
  inset: number | null;
  /** The gap between the card and the bar, the player's controls or the window's edge under it. */
  bottom: number;
  /** Inside the player, wider forms: the card stands this far in from the start, not centred. */
  playerInset: number | null;
  /** `tinted`: the glass button; `text`: the accent text button. */
  action: 'tinted' | 'text';
  actionHeight: number;
}>;
export type ToastPill = Readonly<{height: number; radius: number; paddingX: number; top: number}>;
const phoneCard: ToastCard = {radius: 14, padding: [10, 8, 10, 14], gap: 12, inset: 8, bottom: 12, playerInset: null, action: 'tinted', actionHeight: 36};
const wideCard: ToastCard = {radius: 14, padding: [14, 14, 14, 22], gap: 22, inset: null, bottom: 36, playerInset: 24, action: 'text', actionHeight: 36};
export const toastPill: ToastPill = {height: 40, radius: 20, paddingX: 16, top: 22};
export const toastCards: Readonly<Record<FrameForm, ToastCard | null>> = {phone: phoneCard, medium: wideCard, expanded: wideCard, tv: null};

/** An anchored popover from a task (R9 `actions`, `choice`, `account` on medium and wide forms) takes its content's width
 * between `min` (web, native) and `max`, so a choice is never cut (desktop-detail f4's Episode display is drawn ~540 at 1440,
 * its content 16 in from the edge); on a phone, at most the window less the gutters. */
export const popoverWidth = {min: {web: 340, native: 320}, max: 540} as const;
