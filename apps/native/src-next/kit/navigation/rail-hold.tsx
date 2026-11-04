import React from 'react';
import {railHoldHooks} from './rail-hold-hooks';

/** The rail hold (rail-hold-hooks.ts) over the app's React. */
export const {RailHoldProvider, useRailHold, useRailHeld} = railHoldHooks(React as unknown as Parameters<typeof railHoldHooks>[0]);
