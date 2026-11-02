import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

// Walk W-15 (2 Nov): on Android TV an item with no artwork drew an empty dark frame (search movie and episode
// cards, the artist page's album cards, the audiobook cover). mock.html "No artwork": a picture that was never
// served is the generated cover of the title. Cards take it by default; a surface's own fallback still wins.
const react = {
  createElement: (type: any, props: any, ...children: any[]) => ({type, props: {...props, ...(children.length ? {children: children.length === 1 ? children[0] : children} : {})}}),
  useState: (value: unknown) => [value, () => {}], createContext: () => ({}), useContext: () => 0,
};
const reactModule = {...react, default: react};
const tree = (node: any): any[] => Array.isArray(node) ? node.flatMap(tree) : node?.props ? [node, ...tree(node.props.children)] : [];
const tint = (title: string) => ({first: `first:${title}`, second: 'second'});

async function modules() {
  const art = await screenModule(new URL('../src-next/kit/artwork/MissingArt.tsx', import.meta.url), {
    react: reactModule, '@portico/design': {generatedCoverTint: tint}, './Generated': {GeneratedArt: 'GeneratedArt', GeneratedCover: 'GeneratedCover'},
  });
  const theme = {isTV: true, platform: 'tv', textScale: 1, layoutMode: 'tv', spec: {card: {hover: {episodeScale: 1.05}, wide: {insetX: {tv: 18}, insetBottom: {tv: 16}}, progress: {marginTop: {tv: 8}}}, episode: {width: {tv: 384}, titleMargin: {tv: 12}}}, type: {episodeTitle: {}, wideTitle: {}}, colors: {}};
  const cards = await screenModule(new URL('../src-next/kit/cards/MediaCards.tsx', import.meta.url), {
    react: reactModule, 'react-native': {StyleSheet: {absoluteFill: {}}, Text: 'Text', View: 'View'}, 'react-native-svg': {default: 'Svg', Text: 'SvgText'},
    '../../../../../packages/i18n/src/index': {defaultI18n: {t: (key: string) => key}}, '../theme': {useTheme: () => theme},
    '../artwork/index': {MediaArt: 'MediaArt', CircleArt: 'CircleArt', PersonCircle: 'PersonCircle', GeneratedCoverClearance: {Provider: 'Provider'}, missingArt: art.missingArt},
    '../controls/Focusable': {Focusable: 'Focusable'}, '../selection/index': {SelectionCircle: 'SelectionCircle'},
    './primitives': {ArtFrame: 'ArtFrame', Badge: 'Badge', Caption: 'Caption', CardAction: 'CardAction', CardFrame: 'CardFrame', Parts: 'Parts', Progress: 'Progress', Scrim: 'Scrim', titleStyle: () => ({})},
  });
  return {art, cards};
}
/** The MediaArt a card draws, once its frame has run its render prop. */
function mediaArt(card: any) {
  while (typeof card.type === 'function') card = card.type(card.props);
  const frame = card.props.children({focused: false, hovered: false, holding: false});
  const art = tree(frame).find(node => node.type === 'MediaArt');
  assert.ok(art, 'the card draws its media art');
  return art.props;
}

test('W-15: missing art is the typographic cover for portrait and square, the tinted wash for 16:9', async () => {
  const {art} = await modules();
  const poster = art.missingArt({title: 'Multiple Audio', shape: 'poster', width: 214, height: 321});
  assert.equal(poster.type, 'GeneratedCover');
  assert.deepEqual([poster.props.title, poster.props.glyph, poster.props.width, poster.props.height], ['Multiple Audio', 'M', 214, 321]);
  assert.deepEqual(poster.props.tint, tint('Multiple Audio'));
  const square = art.missingArt({title: 'Example Book', author: 'A. Writer', shape: 'square', width: 420, height: 420});
  assert.equal(square.type, 'GeneratedCover');
  assert.equal(square.props.author, 'A. Writer');
  for (const shape of ['still', 'wide']) {
    const wash = art.missingArt({title: 'Absolute episode 1', shape, width: 384, height: 216});
    assert.equal(wash.type, 'GeneratedArt', shape);
    assert.deepEqual([wash.props.glyph, wash.props.palette, wash.props.width, wash.props.height], ['A', tint('Absolute episode 1'), 384, 216]);
  }
  assert.equal(art.missingArt({title: '  ', shape: 'poster', width: 214, height: 321}), undefined, 'a waiting slot has no title and draws no cover');
});

test('W-15: a poster, square, wide or episode card with no picture draws the generated cover by default', async () => {
  const {cards} = await modules();
  assert.equal(mediaArt(cards.PosterCard({title: 'Multiple Audio', width: 214})).fallback.type, 'GeneratedCover');
  assert.equal(mediaArt(cards.PosterCard({title: 'Multiple Audio', width: 214, artwork: null})).fallback.type, 'GeneratedCover');
  const album = mediaArt(cards.SquareCard({title: 'Example Album', width: 240}));
  assert.equal(album.fallback.type, 'GeneratedCover');
  assert.deepEqual([album.fallback.props.width, album.fallback.props.height], [240, 240]);
  assert.equal(mediaArt(cards.WideCard({title: 'Continue', width: 320})).fallback.type, 'GeneratedArt');
  const episode = mediaArt(cards.EpisodeTile({title: 'Absolute episode 1', label: [], layout: 'tile', width: 384}));
  assert.equal(episode.fallback.type, 'GeneratedArt');
  assert.deepEqual([episode.fallback.props.width, episode.fallback.props.height], [384, 216]);
});

test('W-15: a card with a picture, its own fallback, or no title keeps what it had', async () => {
  const {cards} = await modules();
  const picture = {uri: 'https://server/art.jpg', cacheKey: 'art'};
  assert.equal(mediaArt(cards.PosterCard({title: 'Has art', width: 214, artwork: picture})).fallback, undefined, 'art present: nothing drawn under it');
  assert.equal(mediaArt(cards.PosterCard({title: 'Own', width: 214, artworkFallback: 'OWN'})).fallback, 'OWN', 'a surface chooses its own');
  assert.equal(mediaArt(cards.PosterCard({title: 'Plain', width: 214, artworkFallback: null})).fallback, null, 'null keeps the frame plain');
  assert.equal(mediaArt(cards.PosterCard({title: '', width: 214})).fallback, undefined, 'a waiting card draws no cover');
});
