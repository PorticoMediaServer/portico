' Shared look: colours, type and sizes from packages/design, for a 1920x1080 screen.
' Every component reads these; a screen never carries its own colour or size.

function colours() as object
    return {
        ink: "0x071018FF", raised: "0x10212DFF", raisedHigh: "0x162C3AFF", raisedBright: "0x1D3848FF",
        overlay: "0x132634FF", line: "0x28404FFF", lineStrong: "0x3B5668FF", track: "0x35505FFF",
        text: "0xEDF5FAFF", textSecondary: "0xBDCCD6FF", textTertiary: "0x96A9B5FF",
        accent: "0x409FD4FF", accentStrong: "0x5CB3E2FF", focus: "0xD5EFFFFF", actionInk: "0x03121CFF",
        white: "0xFFFFFFFF", danger: "0xF38B88FF",
        ' over a picture: a control's glass plate at rest and in focus, the dark plate under a focus
        ' label, the seek bar's track, the lighter stretch a waiting scrub would skip, and the tick
        ' that marks where the picture still is
        glass: "0xEDF5FA24", glassOn: "0xEDF5FA47", labelPlate: "0x071018E6",
        barTrack: "0xEDF5FA38", barAhead: "0x5CB3E26B", barTick: "0xEDF5FAE6",
        ' the faint plate behind a side sheet's open heading and its choices
        openGroup: "0xEDF5FA14",
        ' the plates under buttons, chips and keys: the one in focus, one at rest, a chip that is
        ' switched on, a key at rest; a progress track on a pale plate
        plateOn: "0xD8E6EEFF", plate: "0x1D3848E6", chipOn: "0x2F86BBFF", key: "0x10212DE6", trackOn: "0x03121C40",
        ' the icon of a page that could not load, and the tile of a section that could not
        warning: "0xE8BC6AFF", dangerTint: "0xF38B8833",
        ' laid over a card's picture: the plate under its length and tick, its progress track, a generated cover's rule
        onArt: "0x071018D9", trackOnArt: "0xEDF5FA40", coverRule: "0xEDF5FA8C",
        ' the dark card under a prompt over the picture (Up next, the Undo note), and the dimming on its picture
        promptPlate: "0x09131BD6", stillDim: "0x707070FF",
        ' the guide's channel badges, one picked from the channel's name
        channelTints: ["0x8A3B3BFF", "0x8A5A2BFF", "0x7A6A2AFF", "0x3E6B3AFF", "0x2D6B6BFF", "0x34558AFF", "0x5B3F8AFF", "0x7A3A6BFF"]
    }
end function

' The screen frames of design/ui-v1/system-frames.html, as numbers for a 1920 x 1080 television.
' A screen places its parts from its frame and types no edge of its own; fixed regions are placed
' from these lines, never stacked after whatever is above them (rule 12). Each frame is added
' here with the first screen that uses it.
function frames() as object
    return {
        ' The side panel every menu and picker opens in, its words from `top` and its rows from `rowsX`. Back closes it.
        sheet: {x: 1200, width: 720, top: 64, rowsX: 1260, rowsWidth: 600},
        ' Live TV's guide: the time header, the first row and the row pitch, the channel column, and the
        ' grid from `gridX` at `perMinute` px a minute; `shown` rows fit on the screen.
        guide: {headerY: 244, rowsY: 296, pitch: 100, cell: 90, channelWidth: 300, gridX: 520, perMinute: 8, shown: 7},
        ' The video player: the picture is the page. The top row hangs from `top`; the seek bar,
        ' the time row and the button row sit on their own lines; everything runs from `left` to `right`.
        ' Over it: the waiting spinner's line, how far below a page notice Playback failed sits, the foot of the prompts at the lower right, and the Finished page's lines.
        player: {left: 96, right: 1824, top: 80, barY: 828, timeY: 862, buttonsY: 912, waitY: 470, noticeDrop: 40, promptBottom: 720, finished: {top: 84, titleY: 120, metaY: 326, buttonsY: 372}},
        ' Every page beside the rail: its content starts at `gutter` and ends at `rightEdge`.
        gutter: 200, rightEdge: 1824,
        ' A library or Search (frame "Browse"): the page title's line; Home's moderate hero with its
        ' words from `heroTop`, and the heading of the row in focus on `rowsY`.
        browse: {titleY: 64, heroTop: 110, rowsY: 520},
        ' A library (tv.html frame 4): the toolbar under the page title, the count, then the grid's first cards.
        grid: {toolsY: 144, countY: 236, cardsY: 318},
        ' Search (tv.html frame 5): the box on the page title's line, the keys under it, the results
        ' column from `resultsX`; the row in focus is brought up level with the keys.
        search: {boxWidth: 500, boxHeight: 80, keysY: 176, resultsX: 780},
        ' A title page (frame "Title page"): its words and action row start at `copyTop`, `copyWidth`
        ' wide; the section strip is fixed at `tabsY` and the first row at `rowY`.
        title: {copyTop: 80, copyWidth: 960, tabsY: 628, rowY: 712,
            ' below the hero the page folds up (tv-detail frame 3): the strip at `foldedTabsY`, the
            ' section's row heading at `foldedHeadingY`
            foldedTabsY: 164, foldedHeadingY: 263,
            ' Details: the facts' column (Released, Directed by...) beside About
            factsX: 1148,
            ' a show's Episodes, folded (tv-detail frames 5 and 6): the season list and the episode
            ' stills from `episodesY`, the episode slot at `episodesX`; under them the band about what has
            ' focus: its heading ending on `bandHeadBottom`, the meta line on `bandMetaMid`, three lines of
            ' synopsis from `bandTextY`, the Resume line on `bandResumeMid`; the credits column at
            ' `creditsX` from `creditsY`, its * hint on `bandHintMid`; then More like this peeking, its
            ' heading at `peekY`
            episodesX: 600, episodesY: 248, bandHeadBottom: 648, bandMetaMid: 680, bandTextY: 708,
            bandResumeMid: 848, creditsX: 1264, creditsY: 616, bandHintMid: 878, peekY: 944},
        ' A person or a collection (tv-detail frames 9 and 10): the words in fixed slots from the title
        ' frame's copyTop (a person's beside a face `face` across); between a collection's description
        ' and `rowsY` lies the place of its action row; the first row's heading is fixed at `rowsY`, and
        ' once focus goes down the row in focus sits at `scrolledY`.
        group: {face: 280, nameY: 112, factsY: 252, aboutY: 298, rowsY: 470, scrolledY: 164},
        ' The whole page could not load (frame "Page states"): icon, title and one sentence from `y`,
        ' `width` wide, then buttons on `buttonsY`.
        state: {y: 300, width: 1000, buttonsY: 760}
    }
end function

' Page geometry shared by every screen. `cardPad` is the headroom above and below a card inside its
' list cell: a list cuts off whatever leaves the cell, and the card in focus grows.
function metrics() as object
    return {safeLeft: 200, safeRight: 56, railWidth: 150, railOpenWidth: 440, pageShift: 270, cardGap: 28, cardPad: 16, focusScale: 1.08, focusSpread: 30}
end function

' The ten-foot type scale (packages/design typeScale.tenFoot): [weight, size, line height or 0].
function typeRoles() as object
    return {
        lockup: [800, 100, 0], lockupLong: [800, 56, 0], display: [800, 96, 0], lede: [400, 30, 42], title: [800, 44, 0], heading: [600, 32, 0],
        rowHeading: [700, 32, 0], rowSubtitle: [400, 20, 0], nav: [700, 24, 0],
        cardTitle: [600, 22, 0], cardSubtitle: [400, 20, 0], wideTitle: [700, 26, 0],
        meta: [400, 24, 0], metaStrong: [700, 24, 0], eyebrow: [600, 24, 0], synopsis: [400, 24, 35],
        button: [700, 26, 0], tag: [600, 20, 0], badge: [800, 18, 0], body: [400, 24, 33], caption: [400, 20, 27],
        keycap: [700, 18, 0], skipSeconds: [800, 14, 0], micro: [500, 17, 0], episodeTitle: [700, 24, 30], seasonName: [700, 26, 0], personInitials: [700, 44, 0], section: [700, 26, 0], factLabel: [400, 22, 0], about: [400, 24, 35], listLabel: [600, 25, 0], help: [500, 19, 0], groupLabel: [700, 20, 0], padKey: [700, 44, 0], code: [800, 92, 0], welcome: [800, 88, 0], pageTitle: [800, 64, 0], faceInitial: [700, 84, 0], bodyStrong: [700, 24, 33], ledeStrong: [700, 30, 42], coverTitle: [800, 26, 29],
        tab: [700, 26, 0], keyLetter: [600, 32, 0], personFace: [800, 96, 0]
    }
end function

function fontFor(role as string) as object
    r = typeRoles()[role]
    f = createObject("roSGNode", "Font")
    f.uri = "pkg:/fonts/manrope-" + r[0].toStr() + ".ttf"
    f.size = r[1]
    return f
end function

' Give a Label its role's font (and line spacing where the role sets a line height).
sub setType(label as object, role as string, colour = "" as string)
    r = typeRoles()[role]
    label.font = fontFor(role)
    if r[2] > 0 then label.lineSpacing = r[2] - r[1] * 1.25
    if colour <> "" then label.color = colour
end sub

' A shelf's geometry for a card shape: {card: [w, h], headingGap, block}.
' `block` is the height from one shelf's heading to the next.
function shelfMetrics(shape as string) as object
    card = [214, 320]
    if shape = "landscape" then card = [400, 225]
    if shape = "square" then card = [214, 214]
    if shape = "episode" then card = [384, 340]     ' a 16:9 still with the episode's name and number under it
    if shape = "gridPoster" then card = [200, 372]     ' a poster 200 x 300 with its title and year under it, one line each
    if shape = "person" then card = [190, 250]     ' a round face 150 across with a name and a part under it
    return {card: card, headingGap: 60, block: 60 + card[1] + 52}
end function
