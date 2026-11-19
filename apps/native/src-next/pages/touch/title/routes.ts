import type {ComponentType} from 'react';
import type {ContentEntryKind} from '../../../../../../packages/client-core/src/library-content';
import type {NativeTitleScreenProps} from '../../../screens/watch/TitleScreen';
import {TitlePage} from './TitlePage';

/** The rebuilt touch title pages (phones and tablets), by kind (the touch lane's own table; TV keeps pages/tv/title/routes.ts).
 * A kind not here stays on the old title screen until its page lands. */
export const touchTitlePages: Partial<Record<ContentEntryKind, ComponentType<NativeTitleScreenProps>>> = {movie: TitlePage, show: TitlePage};
