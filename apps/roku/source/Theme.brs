' Shared look: colours, type and sizes from packages/design, for a 1920x1080 screen.
' Every component reads these; a screen never carries its own colour or size.

function colours() as object
    return {
        ink: "0x071018FF", raised: "0x10212DFF", raisedHigh: "0x162C3AFF", raisedBright: "0x1D3848FF",
        overlay: "0x132634FF", line: "0x28404FFF", lineStrong: "0x3B5668FF", track: "0x35505FFF",
        text: "0xEDF5FAFF", textSecondary: "0xBDCCD6FF", textTertiary: "0x96A9B5FF",
        accent: "0x409FD4FF", accentStrong: "0x5CB3E2FF", focus: "0xD5EFFFFF", actionInk: "0x03121CFF",
        white: "0xFFFFFFFF", danger: "0xF38B88FF",
        ' the plates under buttons, chips and keys: the one in focus, one at rest, and a chip that is switched on
        plateOn: "0xD8E6EEFF", plate: "0x1D3848E6", chipOn: "0x2F86BBFF"
    }
end function

' The screen frames of design/ui-v1/system-frames.html, as numbers for a 1920 x 1080 television.
' A screen picks one and places its parts from it; it never types a gutter, a header line or a
' row's starting line of its own. Fixed regions are placed from these edges, never stacked after
' whatever is above them, so nothing moves when something is missing (rule 12).
function frames() as object
    return {
        gutter: 200, rightEdge: 1824, railClosed: 112,
        ' Home, a library, Live TV, Saved, Search results: a page title at `titleY` (44/800), Home's
        ' moderate hero with its words from `heroTop`, the first row's heading at `rowsY`, rows `rowGap` apart.
        browse: {titleY: 64, heroTop: 110, rowsY: 520, rowGap: 44, headingGap: 22, bottom: 56},
        ' A film, show, person, collection, album or book: words and the action row from `copyTop`,
        ' the tabs line fixed at `tabsY`, the first row fixed at `rowY`; art fills the right and fades left.
        title: {copyTop: 80, copyWidth: 960, tabsY: 628, rowY: 712, artX: 600},
        ' Settings: the list column, then the chosen page with its title and intro and sections of rows.
        settings: {top: 64, listWidth: 480, listRow: 56, pageX: 760, pageWidth: 1064, titleBlock: 150, sectionsY: 250, row: 84, sectionGap: 44},
        ' The side panel every menu and picker opens in. Back closes it.
        sheet: {x: 1200, width: 720, rowsX: 1260, rowsY: 140, rowsWidth: 600, rowsHeight: 700},
        ' Before anyone is signed in: words and steps on the left, a QR code or picture on the right.
        signIn: {x: 200, y: 120, width: 760, height: 700, sideX: 1100, sideY: 220, side: 560},
        ' The video player: the picture is the page; the controls sit in the bottom 300, prompts lower right.
        player: {overlayY: 780, promptX: 1400, promptY: 600, promptWidth: 440, promptHeight: 120},
        ' Now playing for music and audiobooks: every control placed from here; only Up next changes.
        audio: {fromY: 120, artX: 200, artY: 230, art: 600, titleX: 880, titleY: 230, titleWidth: 500, titleHeight: 330, controlsY: 590, controlsHeight: 274, nextX: 1460, nextY: 230, nextWidth: 360, nextHeight: 640},
        ' The whole page could not load, or is empty: icon, title and one sentence, then buttons.
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
        lockup: [800, 120, 0], lockupLong: [800, 84, 0], display: [800, 96, 0], lede: [400, 30, 42], title: [800, 44, 0], heading: [600, 32, 0],
        rowHeading: [700, 32, 0], rowSubtitle: [400, 20, 0], nav: [700, 24, 0],
        cardTitle: [600, 22, 0], cardSubtitle: [400, 20, 0], wideTitle: [700, 26, 0],
        meta: [400, 24, 0], metaStrong: [700, 24, 0], eyebrow: [600, 24, 0], synopsis: [400, 24, 35],
        button: [700, 26, 0], tag: [600, 20, 0], badge: [800, 18, 0], body: [400, 24, 33], caption: [400, 20, 27],
        keycap: [700, 18, 0], skipSeconds: [800, 14, 0], chip: [700, 24, 0], micro: [500, 17, 0], episodeTitle: [700, 24, 30], seasonName: [700, 26, 0], personInitials: [700, 44, 0], section: [700, 26, 0], factLabel: [400, 22, 0], about: [400, 24, 35], listLabel: [600, 25, 0], help: [500, 19, 0], groupLabel: [700, 20, 0], tab: [700, 26, 0], padKey: [700, 44, 0], code: [800, 92, 0], welcome: [800, 88, 0], pageTitle: [800, 64, 0], faceInitial: [700, 84, 0], bodyStrong: [700, 24, 33], ledeStrong: [700, 30, 42], coverTitle: [800, 26, 29]
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
    if shape = "episode" then card = [384, 340]
    if shape = "gridPoster" then card = [200, 396]     ' a poster 200 x 300 with its title (two lines at most) and year under it    ' a 16:9 still with the episode's name and number under it
    if shape = "person" then card = [190, 250]     ' a round face 150 across with a name and a part under it
    return {card: card, headingGap: 60, block: 60 + card[1] + 52}
end function
