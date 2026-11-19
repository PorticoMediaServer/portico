import type {ComponentType} from 'react';
import type {ContentEntryKind} from '../../../../../../packages/client-core/src/library-content';
import type {NativeTitleScreenProps} from '../../../screens/watch/TitleScreen';
import {FilmPage} from './FilmPage';

/** The rebuilt TV title pages, by kind (the TV lane's own table; the touch lane keeps pages/touch/title/routes.ts). A kind not
 * here stays on the old title screen until its page lands. */
export const tvTitlePages: Partial<Record<ContentEntryKind, ComponentType<NativeTitleScreenProps>>> = {movie: FilmPage};
