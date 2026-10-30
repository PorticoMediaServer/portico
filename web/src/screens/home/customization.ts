import {HomeLayoutEditor, type HomeLayoutEditorBindings} from '@core/home-layout-editor.ts';
import type {PersonalSavedService} from '@core/personal-saved.ts';

/** The web dialog's own saved-views service, added to the shared Home layout editor bindings. */
export type HomeCustomizationBindings = HomeLayoutEditorBindings & Readonly<{
  /** A fresh dialog-owned service, captured to this signed-in transport and SavedScope. */
  createSavedViews: () => PersonalSavedService;
}>;
/** Home's own entry and Settings' Appearance row draw the one shared editor. */
export const HomeCustomization = HomeLayoutEditor;
export type HomeCustomization = HomeLayoutEditor;
