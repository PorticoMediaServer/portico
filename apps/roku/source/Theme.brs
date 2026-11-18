' Shared look: colours, type and sizes from packages/design, for a 1920x1080 screen.
' Every component reads these; a screen never carries its own colour or size.

' Entries are kept one per line in alphabetical order so that changes to different screens do not
' collide; a new entry goes in its alphabetical place.
function colours() as object
    return {
        accent: "0x409FD4FF"                ' accent
        accentStrong: "0x5CB3E2FF"          ' accent
        actionInk: "0x03121CFF"             ' ink on an accent plate
        barAhead: "0x5CB3E26B"              ' over a picture: the lighter stretch a waiting scrub would skip
        barTick: "0xEDF5FAE6"               ' over a picture: the tick that marks where the picture still is
        barTrack: "0xEDF5FA38"              ' over a picture: the seek bar's track
        ' the guide's channel badges, one picked from the channel's name
        channelTints: ["0x8A3B3BFF", "0x8A5A2BFF", "0x7A6A2AFF", "0x3E6B3AFF", "0x2D6B6BFF", "0x34558AFF", "0x5B3F8AFF", "0x7A3A6BFF"]
        chipOn: "0x2F86BBFF"                ' the plate under a chip that is switched on
        coverRule: "0xEDF5FA8C"             ' laid over a card's picture: a generated cover's rule
        danger: "0xF38B88FF"                ' danger
        dangerTint: "0xF38B8833"            ' the tile of a section that could not load
        ' a profile's face, by the colour name the server gives it (packages/design profileArtColors)
        faces: {blue: "0x28587AFF", violet: "0x685294FF", mint: "0x28665DFF", coral: "0x985243FF", gold: "0x796120FF", slate: "0x4A596AFF", rose: "0x854D73FF", sky: "0x376F8CFF"}
        focus: "0xD5EFFFFF"                 ' focus
        glass: "0xEDF5FA24"                 ' over a picture: a control's glass plate at rest
        glassOn: "0xEDF5FA47"               ' over a picture: a control's glass plate in focus
        handoffCard: "0x0C1A25F2"           ' the card a hand-off to a phone is printed on (its QR code, address and code)
        handoffEdge: "0x1C3341FF"           ' the edge of that card
        ink: "0x071018FF"                   ' surface
        key: "0x10212DE6"                   ' the plate under a key at rest
        labelPlate: "0x071018E6"            ' over a picture: the dark plate under a focus label
        line: "0x28404FFF"                  ' line
        lineStrong: "0x3B5668FF"            ' line
        onArt: "0x071018D9"                 ' laid over a card's picture: the plate under its length and tick
        openGroup: "0xEDF5FA14"             ' the faint plate behind a side sheet's open heading and its choices
        overlay: "0x132634FF"               ' surface
        plate: "0x1D3848E6"                 ' the plate under a button, chip or key at rest
        plateOn: "0xD8E6EEFF"               ' the plate under a button, chip or key in focus
        promptPlate: "0x09131BD6"           ' the dark card under a prompt over the picture (Up next, the Undo note)
        railPanel: "0x0B1822FF"             ' the open rail's panel
        raised: "0x10212DFF"                ' surface
        raisedBright: "0x1D3848FF"          ' surface
        raisedHigh: "0x162C3AFF"            ' surface
        ' the 1 px line between a list's rows
        separator: "0x3B566873"
        stillDim: "0x707070FF"              ' the dimming on a prompt's picture
        text: "0xEDF5FAFF"                  ' text
        textSecondary: "0xBDCCD6FF"         ' text
        textTertiary: "0x96A9B5FF"          ' text
        track: "0x35505FFF"                 ' a progress track
        trackOn: "0x03121C40"               ' a progress track on a pale plate
        trackOnArt: "0xEDF5FA40"            ' laid over a card's picture: its progress track
        warning: "0xE8BC6AFF"               ' the icon of a page that could not load
        white: "0xFFFFFFFF"                 ' white
    }
end function

' The screen frames of design/ui-v1/system-frames.html, as numbers for a 1920 x 1080 television.
' A screen places its parts from its frame and types no edge of its own; fixed regions are placed
' from these lines, never stacked after whatever is above them (rule 12). Each frame is added
' here with the first screen that uses it. Entries are kept one per line in alphabetical order so
' that changes to different screens do not collide; a new entry goes in its alphabetical place.
function frames() as object
    return {
        ' Now playing for music and audiobooks: every control placed from here; only Up next changes.
        audio: {fromY: 120, artX: 200, artY: 230, art: 600, titleX: 880, titleY: 230, titleWidth: 500, titleHeight: 330,
            controlsY: 590, controlsHeight: 274, nextX: 1460, nextY: 230, nextWidth: 360, nextHeight: 640}
        ' A library or Search (frame "Browse"): the page title's line; Home's moderate hero with its
        ' words from `heroTop`, and the heading of the row in focus on `rowsY`.
        browse: {titleY: 64, heroTop: 110, rowsY: 520}
        ' The red-verb dialog: a panel `width` wide from `x`, its words and buttons `pad` inside it.
        dialog: {x: 510, width: 900, pad: 52}
        ' A library (tv.html frame 4): the toolbar under the page title, the chosen filters' chips and the count on one line under it, then the grid's first cards.
        grid: {toolsY: 144, chipsY: 220, countY: 236, cardsY: 318}
        ' A person or a collection (tv-detail frames 9 and 10, tv-detail.html .pcopy and .tvc): the
        ' words in fixed slots from `eyebrowY` (a person's beside a face `face` across, its top on the
        ' same line); a person's first row's heading on `rowsY`, a collection's on `actionRowsY`, which
        ' leaves the place of its action row above it; once focus goes down the row in focus sits at `scrolledY`.
        group: {face: 280, nameY: 132, factsY: 260, aboutY: 306, rowsY: 440, scrolledY: 164, eyebrowY: 96, actionRowsY: 520}
        ' Live TV's guide: the toolbar beside the page title, the filter chips' line, the time header, the
        ' first row and the row pitch, the channel column, and the
        ' grid from `gridX` at `perMinute` px a minute; `shown` rows fit on the screen.
        guide: {headerY: 244, rowsY: 296, pitch: 100, cell: 90, channelWidth: 300, chipsY: 144, gridX: 520, perMinute: 8, shown: 7, toolbarX: 380, toolbarY: 56}
        ' Every page beside the rail: its content starts at `gutter`.
        gutter: 200
        ' The now-playing card at the empty end of every page's header row: its corner, its size at rest and open.
        nowPlaying: {right: 1824, top: 44, width: 440, height: 84, openWidth: 700, openHeight: 470}
        ' The video player: the picture is the page. The top row hangs from `top`; the seek bar,
        ' the time row and the button row sit on their own lines; everything runs from `left` to `right`.
        ' Over it: the waiting spinner's line, how far below a page notice Playback failed sits, the foot of the prompts at the lower right, and the Finished page's lines.
        player: {left: 96, right: 1824, top: 80, barY: 828, timeY: 862, buttonsY: 912, waitY: 470, noticeDrop: 40, promptBottom: 720, finished: {top: 84, titleY: 120, metaY: 326, buttonsY: 372, rowY: 560}}
        ' Every page beside the rail: its content ends at `rightEdge`.
        rightEdge: 1824
        ' Search (tv.html frame 5): the box on the page title's line, the keys under it, the results
        ' column from `resultsX` with the kinds' chips on the title line; the row in focus is
        ' brought up level with the keys. Under the words of an empty box or of no matches, the
        ' recent searches or the genres to browse start at `underNoteY` (states frame 18).
        ' A Top result (tv.html frame 5) leads the results with its 16:9 picture level with the keys;
        ' the rows start `topHeight` under its top.
        search: {boxWidth: 500, boxHeight: 80, keysY: 176, resultsX: 780, underNoteY: 236, topHeight: 319}
        ' Settings: the list column from `listX` (the Search settings box on `findY`, `findHeight` tall, its
        ' key hints centred on `hintsY` and 50 under it), then the chosen page at `pageX`: title at `top`,
        ' content `headerGap` under it, an intro `introWidth` wide, sections `sectionGap` apart; a row in
        ' focus is kept between `scrollTop` and `foot`. Search settings itself is Search's frame.
        settings: {top: 64, listX: 200, listWidth: 480, listRow: 56, hintsY: 956, pageX: 760, pageWidth: 1064, introWidth: 900, headerGap: 28, row: 84, sectionGap: 44, scrollTop: 120, foot: 1024, findY: 138, findHeight: 64}
        ' The side panel every menu and picker opens in, its words from `top`, its rows from `rowsX` and
        ' the remote's keys at its foot on `hintsY`. Back closes it.
        sheet: {x: 1200, width: 720, top: 64, rowsX: 1260, rowsWidth: 600, hintsY: 1006}
        ' Before a profile is chosen (frame "Sign-in"): a hand-off to a phone (the TV code page, Forgot
        ' PIN?): words from `pageX`, the eyebrow, title, body and buttons on their lines, the QR card's
        ' corner; the wordmark's corner; Who's watching (auth.html frame 16): the title's bottom line,
        ' the faces' row and its pitch, a refusal's line; a PIN (frame 17): the column from `pinX`, its
        ' face, title, name, boxes and note lines, the number pad's corner, the key hints' line
        signIn: {pageX: 120, eyebrowY: 354, titleY: 385, bodyY: 494, buttonsY: 656, cardX: 1060, cardY: 212,
            markX: 120, markY: 72, whoTitleBottom: 391, facesY: 470, facePitch: 296, whoNoteY: 860,
            pinX: 240, pinWidth: 640, pinFaceY: 238, pinTitleY: 462, pinNameY: 540, boxesY: 619, pinNoteY: 775, keysX: 1176, keysY: 219, hintsY: 988, doorButtonsY: 576, doorNoteY: 690, doorTitleY: 416, factorBodyY: 512, factorBoxesY: 640, factorTitleY: 420, noteY: 760, pressY: 976, localTitleY: 127, localBodyY: 229, nearbyLabelY: 300, nearbyRowsY: 340, elsewhereLabelY: 700, localButtonsY: 740, localNoteY: 840}
        ' The whole page could not load (frame "Page states"): icon, title and one sentence from `y`,
        ' `width` wide, then buttons on `buttonsY`.
        state: {y: 300, width: 1000, buttonsY: 760}
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
            bandResumeMid: 848, creditsX: 1264, creditsY: 616, bandHintMid: 878, peekY: 944}
    }
end function

' Page geometry shared by every screen. `cardPad` is the headroom above and below a card inside its
' list cell: a list cuts off whatever leaves the cell, and the card in focus grows.
function metrics() as object
    return {safeLeft: 200, safeRight: 56, railWidth: 150, railOpenWidth: 440, pageShift: 270, cardGap: 28, cardPad: 16, focusScale: 1.08, focusSpread: 30}
end function

' The ten-foot type scale (packages/design typeScale.tenFoot): [weight, size, line height or 0].
' Entries are kept one per line in alphabetical order so that changes to different screens do not
' collide; a new entry goes in its alphabetical place.
function typeRoles() as object
    return {
        about: [400, 24, 35]                ' a title page's or a person's About text
        ' the steps on a sign-in page (auth.html .tvl .lede)
        authBody: [400, 26, 38]
        authTitle: [800, 72, 0]             ' before a profile is chosen: a page's title (auth.html .tvl h1, Who's watching)
        badge: [800, 18, 0]                 ' a small badge on a card
        body: [400, 24, 33]
        bodyStrong: [700, 24, 33]
        button: [700, 26, 0]
        caption: [400, 20, 27]
        cardSubtitle: [400, 20, 0]
        cardTitle: [600, 22, 0]
        code: [800, 92, 0]                  ' the code on a QR card
        coverTitle: [800, 26, 29]           ' the title on a generated cover
        display: [800, 96, 0]               ' the largest title (a page state, a person's name)
        episodeTitle: [700, 24, 30]
        eyebrow: [600, 24, 0]               ' the small line above a title
        faceInitial: [800, 84, 0]           ' the initial on a profile's face
        faceName: [700, 30, 0]              ' before a profile is chosen: a face's name
        faceNote: [600, 22, 0]              ' before a profile is chosen: the line under a face's name
        factLabel: [400, 22, 0]             ' a fact's label in Details (Released, Directed by...)
        finishedTitle: [800, 84, 0]         ' the Finished page's title, two lines above its year line (tv-player frame 7)
        groupLabel: [700, 20, 0]            ' a group's heading in the player's menus
        heading: [600, 32, 0]
        help: [500, 19, 0]                  ' small secondary words (a reason, a choice's value, a channel number)
        keycap: [700, 18, 0]                ' the key in a key hint
        keyLetter: [600, 32, 0]             ' a key on the on-screen keyboard
        lede: [400, 30, 42]                 ' a larger sentence under a title (a page state, a search note)
        ledeStrong: [700, 30, 42]
        listLabel: [600, 25, 0]             ' a row's label in a list or side sheet
        lockup: [800, 100, 0]               ' a big title lockup
        lockupLong: [800, 56, 0]            ' a big title lockup for a long title
        matchCount: [600, 22, 0]            ' how many settings a page holds that match, beside its name in Search settings
        meta: [400, 24, 0]                  ' a line of facts (year, length, rating)
        metaStrong: [700, 24, 0]
        micro: [500, 17, 0]                 ' the smallest words (under a rail item, more above or below in a season list)
        nav: [700, 24, 0]                   ' the rail's items
        padKey: [700, 44, 0]                ' a key on the PIN's number pad
        pageTitle: [800, 64, 0]
        personFace: [800, 96, 0]            ' the initials on a person's face on their page
        personInitials: [700, 44, 0]        ' the initials on a person's card
        pinTitle: [800, 52, 0]              ' before a profile is chosen: the PIN page's title
        rowHeading: [700, 32, 0]
        rowSubtitle: [400, 20, 0]
        seasonName: [700, 26, 0]
        section: [700, 26, 0]               ' a title page's section strip
        skipSeconds: [800, 14, 0]           ' the 10 and 30 inside the player's skip arrows
        synopsis: [400, 24, 35]
        tab: [700, 26, 0]
        tag: [600, 20, 0]                   ' a small label on a card, the hero or the rail
        title: [800, 44, 0]
        welcome: [800, 88, 0]               ' the welcome on the sign-in front door
        wideTitle: [700, 26, 0]             ' the title on a wide card
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
    if shape = "gridSquare" then card = [200, 272]     ' a square cover 200 across (music, audiobooks) with its title and year under it
    if shape = "person" then card = [190, 250]     ' a round face 150 across with a name and a part under it
    return {card: card, headingGap: 60, block: 60 + card[1] + 52}
end function

' A profile's face colour from the name the server gives it; blue when it names none we know.
function faceColour(name as dynamic) as string
    faces = colours().faces
    if name <> invalid and faces[name] <> invalid then return faces[name]
    return faces.blue
end function
