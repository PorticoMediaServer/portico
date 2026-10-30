import {enUS, type MessageId} from './en-US.ts';

/**
 * Regional English, derived from the en-US source by spelling rules.
 *
 * A hand-kept list of the messages that differ goes stale the day a new string
 * says "favorite": the trial en-CA catalogue listed seven messages while the
 * source used the word in forty. A rule cannot miss one. Each regional
 * catalogue is every en-US message whose text a rule changes; everything else
 * falls back to en-US as before (A11Y-14.1: exact locale → en-US).
 *
 * Only spelling is regional. Vocabulary ("Movies", "Trash") is the product's
 * own and stays the same everywhere.
 *
 * A rule is a US stem and its replacement, matched at the start of a word and
 * followed by whatever ending the word has: `favorit` turns "Favorites",
 * "favorited" and "favorite" alike. A stem ending in `$` must end the word.
 */
type Rules = Readonly<Record<string, string>>;

/** Canada and the Commonwealth agree on these. */
const commonwealth: Rules = {
  favorit: 'favourit',
  'favor$': 'favour', 'favors$': 'favours', 'favored$': 'favoured',
  'color$': 'colour', 'colors$': 'colours', 'colored$': 'coloured', 'colorful$': 'colourful',
  behavior: 'behaviour',
  'honor$': 'honour', 'honors$': 'honours', 'honored$': 'honoured',
  neighbor: 'neighbour',
  'canceled$': 'cancelled', 'canceling$': 'cancelling',
  'labeled$': 'labelled', 'labeling$': 'labelling',
  'leveling$': 'levelling',
  'center$': 'centre', 'centers$': 'centres', 'centered$': 'centred',
  theater: 'theatre',
  'gray$': 'grey',
  'catalog$': 'catalogue', 'catalogs$': 'catalogues',
  // The noun; the catalogue never uses the verb ("licensed" is unchanged).
  'license$': 'licence', 'licenses$': 'licences',
};

/** Canada keeps "-ize" and "program"; the rest of the Commonwealth does not. */
const british: Rules = {
  ...commonwealth,
  customiz: 'customis', organiz: 'organis', recogniz: 'recognis', optimiz: 'optimis', normaliz: 'normalis',
  minimiz: 'minimis', maximiz: 'maximis', finaliz: 'finalis', randomiz: 'randomis', synchroniz: 'synchronis',
  authoriz: 'authoris', personaliz: 'personalis', prioritiz: 'prioritis', categoriz: 'categoris',
  initializ: 'initialis', summariz: 'summaris', visualiz: 'visualis', equaliz: 'equalis', stabiliz: 'stabilis',
  analyz: 'analys',
  // A television programme; software is not called a program anywhere in the catalogue.
  'program$': 'programme', 'programs$': 'programmes',
};

function matcher(rules: Rules): (text: string) => string {
  const stems = Object.keys(rules).sort((a, b) => b.length - a.length);
  const pattern = new RegExp('\\b(?:' + stems.map(s => (s.endsWith('$') ? s.slice(0, -1) + '\\b' : s)).join('|') + ')', 'gi');
  const byStem = new Map<string, string>(stems.map(s => [s.replace('$', ''), rules[s]!]));
  return text => text.replace(pattern, (found, offset: number) => {
    // `{color}` and `{color, select, …}` are argument names, not words.
    if (text[offset - 1] === '{') return found;
    const to = byStem.get(found.toLowerCase());
    if (!to) return found;
    if (found === found.toUpperCase() && found.length > 1) return to.toUpperCase();
    return found[0] === found[0]!.toUpperCase() ? to[0]!.toUpperCase() + to.slice(1) : to;
  });
}

/** The names of the languages themselves are never respelled. */
const fixed = (id: string) => id.startsWith('locale.');

function derive(rules: Rules): Partial<Record<MessageId, string>> {
  const respell = matcher(rules);
  const out: Partial<Record<MessageId, string>> = {};
  for (const [id, source] of Object.entries(enUS) as [MessageId, string][]) {
    if (fixed(id)) continue;
    const text = respell(source);
    if (text !== source) out[id] = text;
  }
  return out;
}

/** Derived on first use: a device that reads US English never pays for the others. */
function lazy(rules: Rules): () => Partial<Record<MessageId, string>> {
  let made: Partial<Record<MessageId, string>> | undefined;
  return () => (made ??= derive(rules));
}

export const canadianEnglish = lazy(commonwealth);
export const britishEnglish = lazy(british);
