import React from 'react';
import {StandardBackdrop} from '../artwork';

/** The one top every Settings page has, member and owner, phone, tablet and TV: the standard backdrop (the teal wash) under
 * its header and body. Settings' own sheet draws it, and so does every page opened from Settings that is drawn outside
 * that sheet (Notifications, Watch Together, Switch server, Live TV settings, Equalizer, the owner's Dashboard and
 * Libraries, through `Screen settings`), so no page builds its own top and none
 * shows a flat navy one beside the others (owner, 13 Nov; W4 F14). */
export function SettingsBackdrop() {return <StandardBackdrop/>;}
