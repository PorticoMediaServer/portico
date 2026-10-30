// Foliate paginator adaptations; MIT, see LICENSE and SOURCES.md.
// Equivalent to upstream's lookbehind, without requiring Safari16.4 syntax.
export const unprefixEpubCss = css => css.replace(/([{\s;])-epub-/gi, '$1')

export const lastLinearIndex = sections => {
    for (let index = sections.length - 1; index >= 0; index--)
        if (sections[index].linear !== 'no') return index
    return -1
}
