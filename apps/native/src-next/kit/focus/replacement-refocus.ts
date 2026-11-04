import React from 'react';
import {replacementRefocusHook} from './replacement-refocus-hook';

export type {ReplacementRefocus} from './replacement-refocus-hook';
/** The replacement refocus rule (replacement-refocus-hook.ts) over the app's React. The factory lives in its own module, with
 * no React of its own, so a test harness can build it over a fixture's React without loading this one first. */
export const useReplacementRefocus = replacementRefocusHook(React);
