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
 * `none`: the overlay commits as you choose (a menu, a picker). */
export type OverlayFoot = 'halves' | 'full' | 'end' | 'none';
export type OverlayForm = Readonly<{kind: OverlayFormKind; modal: boolean; bar: OverlayBar; foot: OverlayFoot}>;

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
  /** Narrowing a list: the library's Filters. */
  | 'filters'
  /** Settings: the index and a page. */
  | 'settings'
  /** The profile and account menu under the avatar. */
  | 'account'
  /** The Notifications list under the bell. */
  | 'notifications';

const bar = (leading: OverlayBar['leading'], title: OverlayBar['title'], trailing: OverlayBar['trailing'], subtitle = false): OverlayBar => ({leading, title, trailing, subtitle});
const form = (kind: OverlayFormKind, modal: boolean, overlayBar: OverlayBar, foot: OverlayFoot): OverlayForm => ({kind, modal, bar: overlayBar, foot});
/** Nothing in the bar: a popover has at most a quiet heading of its own content. */
const quiet = bar('none', 'none', 'none');

/** Task x form. The TV column belongs to the TV builder (R9 TV column, family G): on TV a menu or picker is a right-hand
 * side sheet with an eyebrow, a title, full-width rows and a Back/Close key-hint footer. Until that builder fills it, a
 * TV cell is `null` and the kit keeps its existing TV form. */
export const overlayForms: Readonly<Record<OverlayTask, Readonly<Record<FrameForm, OverlayForm | null>>>> = {
  actions: {
    phone: form('sheet', true, bar('none', 'start', 'done', true), 'none'),
    medium: form('popover', true, bar('none', 'start', 'none', true), 'none'),
    /** A pointer menu (desktop-detail f2) leaves the page undimmed. */
    expanded: form('popover', false, bar('none', 'start', 'none', true), 'none'),
    tv: null,
  },
  choice: {
    phone: form('popover', true, quiet, 'none'),
    medium: form('popover', true, quiet, 'none'),
    expanded: form('popover', false, quiet, 'none'),
    tv: null,
  },
  confirm: {
    phone: form('sheet', true, bar('none', 'start', 'none'), 'halves'),
    medium: form('dialog', true, bar('none', 'start', 'none'), 'end'),
    expanded: form('dialog', true, bar('none', 'start', 'none'), 'end'),
    tv: null,
  },
  task: {
    /** live-owner 13a-c: "Cancel · centred title"; the action (or Back and the action) at the foot. */
    phone: form('sheet', true, bar('cancel', 'centre', 'none'), 'full'),
    medium: form('side', true, bar('none', 'start', 'close'), 'halves'),
    expanded: form('dialog', true, bar('none', 'start', 'close'), 'end'),
    tv: null,
  },
  details: {
    phone: form('sheet', true, bar('none', 'start', 'done', true), 'none'),
    medium: form('side', false, bar('none', 'start', 'close', true), 'none'),
    expanded: form('side', false, bar('none', 'start', 'close', true), 'none'),
    tv: null,
  },
  filters: {
    phone: form('sheet', true, bar('none', 'start', 'done'), 'halves'),
    medium: form('side', false, bar('none', 'start', 'close'), 'halves'),
    expanded: form('side', false, bar('none', 'start', 'close'), 'halves'),
    tv: null,
  },
  settings: {
    /** The index: "Settings · Done". A page pushed inside it: "‹ Settings · centred title · its action". */
    phone: form('fullSheet', true, bar('back', 'centre', 'done'), 'none'),
    medium: form('page', false, quiet, 'none'),
    expanded: form('page', false, quiet, 'none'),
    tv: null,
  },
  account: {
    phone: form('sheet', true, bar('none', 'start', 'done'), 'none'),
    medium: form('popover', true, quiet, 'none'),
    expanded: form('popover', true, quiet, 'none'),
    tv: null,
  },
  notifications: {
    /** On a phone Notifications is a page pushed inside the Settings sheet (phone-app 21): "‹ Settings · Notifications · Mark all read". */
    phone: form('fullSheet', true, bar('back', 'centre', 'action'), 'none'),
    medium: form('popover', true, bar('none', 'start', 'action'), 'none'),
    expanded: form('popover', true, bar('none', 'start', 'action'), 'none'),
    tv: null,
  },
};

/** The form a task takes on a form, or `null` on TV until its column is filled. */
export function overlayForm(task: OverlayTask, frameForm: FrameForm): OverlayForm | null {
  return overlayForms[task][frameForm];
}
