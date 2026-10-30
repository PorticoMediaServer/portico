import {defineWebScreen} from '../../route-registry';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {useTogetherBindings} from './owner';
import {TogetherScreen} from './Together';

export function TogetherEntry() {
  const captured = useProtectedWebScreenContext(), binding = useTogetherBindings(captured?.context);
  return binding ? <TogetherScreen binding={binding} /> : null;
}
export default defineWebScreen({id: 'together', chrome: 'shell', component: TogetherEntry});
