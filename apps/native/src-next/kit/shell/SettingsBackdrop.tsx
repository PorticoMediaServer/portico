import React from 'react';
import {StandardBackdrop} from '../artwork';
import {useTheme} from '../theme';

/** Under a page opened from Settings (Watch Together, Live TV settings, Equalizer, the owner's Dashboard and
 * Libraries, through `Screen settings`). On a phone that page is inside the Settings sheet, and on a tablet inside the
 * shell's Settings page; both already stand on their own surface, so the page draws nothing (system rules 1 and 2: a
 * sheet is a plain surface and never draws a backdrop). On TV the page is a layer pushed over the one before it, and
 * covers it with the standard backdrop. */
export function SettingsBackdrop() {return useTheme().isTV ? <StandardBackdrop/> : null;}
