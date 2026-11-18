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
        healthy: "0x86CFB3FF"               ' a state that is well (In step, Playing in Watch Together)
        ink: "0x071018FF"                   ' surface
        key: "0x10212DE6"                   ' the plate under a key at rest
        labelPlate: "0x071018E6"            ' over a picture: the dark plate under a focus label
        line: "0x28404FFF"                  ' line
        lineStrong: "0x3B5668FF"            ' line
        miniGuide: "0x0B1720DB"             ' the live player's guide panel over the picture
        noticeEdge: "0xE8BC6A66"            ' the edge of a Watch Together notice's amber strip (the host away)
        noticePlate: "0xE8BC6A1F"           ' a Watch Together notice's amber strip
        onArt: "0x071018D9"                 ' laid over a card's picture: the plate under its length and tick
        openGroup: "0xEDF5FA14"             ' the faint plate behind a side sheet's open heading and its choices
        overlay: "0x132634FF"               ' surface
        pillPlate: "0x07101880"             ' the player's Watch Together pill over the picture
        plate: "0x1D3848E6"                 ' the plate under a button, chip or key at rest
        plateOn: "0xD8E6EEFF"               ' the plate under a button, chip or key in focus
        promptPlate: "0x09131BD6"           ' the dark card under a prompt over the picture (Up next, the Undo note)
        railPanel: "0x0B1822FF"             ' the open rail's panel
        raised: "0x10212DFF"                ' surface
        raisedBright: "0x1D3848FF"          ' surface
        raisedHigh: "0x162C3AFF"            ' surface
        ratedPlate: "0x5CB3E238"            ' the plate behind a star that is lit, your rating (tv-player frame 7)
        ' the 1 px line between a list's rows
        separator: "0x3B566873"
        stillDim: "0x575757FF"              ' the dimming on a prompt's picture
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
        ' A song's actions (Lyrics, Add to playlist, More) sit on `actionsY`, under the transport.
        audio: {actionsY: 802, fromY: 120, artX: 200, artY: 230, art: 600, titleX: 880, titleY: 230, titleWidth: 500, titleHeight: 330,
            controlsY: 590, controlsHeight: 274, nextX: 1460, nextY: 230, nextWidth: 360, nextHeight: 640}
        ' An audiobook, an author or a series (collections frames 43 to 45): the cover on the left; beside it the
        ' series or eyebrow, the title, the facts, your place, the buttons, then the chapters under their heading
        ' (`row` apart). An author's circle or a series' cover is smaller and higher (`group` at `groupY`), its words
        ' `groupDrop` lower, and its books' rows (`bookRow` tall, `bookGap` between) start at `booksY`.
        book: {coverX: 200, coverY: 96, cover: 420, wordsX: 700, eyebrowY: 96, titleY: 132, metaY: 316, progressY: 380, progressWidth: 380, buttonsY: 426, listY: 592, listWidth: 1124, row: 68,
            groupY: 120, group: 380, groupDrop: 54, booksY: 560, bookRow: 168, bookGap: 10}
        ' A library or Search (frame "Browse"): the page title's line; Home's moderate hero with its
        ' words from `heroTop`, and the heading of the row in focus on `rowsY`.
        browse: {titleY: 64, heroTop: 110, rowsY: 520}
        ' A collection's page (tv-detail frame 10): its action row, Play all and Shuffle, in the place
        ' `group.actionRowsY` leaves above the first row.
        collectionActionsY: 375
        ' Customize Home (collections frames 27 to 29): the wider side panel from `x`, its words from
        ' `pad` inside it to `padRight` from the screen's edge; the title on `top`, the intro from
        ' `introY`, the rows from `rowsY` down to `rowsBottom`, one every `rowPitch`, each `rowHeight`
        ' tall: its grip from `gripX` in a slot `gripWidth` wide, `gap` between its parts, `endPad`
        ' inside its right end, a name with a note under it at `nameTop` and `noteTop`; the landing
        ' line `dropLine` thick; the buttons on `footY`; while a row is held, the key hints on Home's
        ' own foot at `hintsY`.
        customize: {x: 1060, width: 860, pad: 60, padRight: 96, top: 60, introY: 124, rowsY: 218, rowsBottom: 944,
            rowPitch: 76, rowHeight: 70, gripX: 12, gripWidth: 44, gap: 18, endPad: 18, nameTop: 8, noteTop: 41,
            dropLine: 6, footY: 960, hintsY: 1000}
        ' The red-verb dialog: a panel `width` wide from `x`, its words and buttons `pad` inside it.
        dialog: {x: 510, width: 900, pad: 52}
        ' File info (tv-detail frame 8): the panel from `x`, its words `pad` inside it; the eyebrow on
        ' `top`, the name above the meta line on `metaY`, the table from `tableY` with rows
        ' `rowPitch` apart and its columns at `columns` from the words' left; Done on `doneY`.
        fileInfo: {x: 1020, pad: 56, top: 64, metaY: 176, tableY: 240, rowPitch: 58, columns: [0, 170, 300, 450, 610, 710], doneY: 952}
        ' A library (tv.html frame 4): the toolbar under the page title, the chosen filters' chips and the count on one line under it, then the grid's first cards; its Discover tab, Home's hero and rows, sits `discoverDrop` lower than Home so the hero's words stay under the tabs.
        grid: {toolsY: 144, chipsY: 220, countY: 236, cardsY: 318, discoverDrop: 48}
        ' A person or a collection (tv-detail frames 9 and 10, tv-detail.html .pcopy and .tvc): the
        ' words in fixed slots from `eyebrowY` (a person's beside a face `face` across, its top on the
        ' same line); a person's first row's heading on `rowsY`, a collection's on `actionRowsY`, which
        ' leaves the place of its action row above it; once focus goes down the row in focus sits at `scrolledY`.
        group: {face: 280, nameY: 132, factsY: 260, aboutY: 306, rowsY: 440, scrolledY: 164, eyebrowY: 96, actionRowsY: 520,
            ' a person's Filters chip, its top on `filtersY`, ending at the page's right edge
            filtersY: 432}
        ' Live TV's guide: the toolbar beside the page title, the filter chips' line, the time header, the
        ' first row and the row pitch, the channel column, and the
        ' grid from `gridX` at `perMinute` px a minute; `shown` rows fit on the screen.
        guide: {headerY: 244, rowsY: 296, pitch: 100, cell: 90, channelWidth: 300, chipsY: 144, gridX: 520, perMinute: 8, shown: 7, toolbarX: 380, toolbarY: 56}
        ' Every page beside the rail: its content starts at `gutter`.
        gutter: 200
        ' A playlist or collection the viewer has (collections frames 18 and 20): the mosaic `cover`
        ' across from `coverY`, the words from `wordsX` on their lines, the buttons on `actionsY`,
        ' then the rows from `rowsY`, `row` apart: a cover `thumb` wide, the title from `titleX`, the
        ' library from `libraryX`, the row's state at the right edge; the hint at the foot on `hintY`.
        list: {cover: 280, coverY: 72, wordsX: 528, eyebrowY: 100, nameY: 132, factsY: 226, actionsY: 280, rowsY: 456, row: 104, thumb: 52, titleX: 324, libraryX: 1168, hintY: 1006}
        ' The live player's guide (tv-player frame 11, second): the panel's corner and width, the
        ' first channel's row `rowsY` under its top, rows `row` apart.
        miniGuide: {x: 1144, y: 80, width: 680, rowsY: 72, row: 116}
        ' An album or artist page (tv-more frames 6 and 8): the album's cover on the left with its words and
        ' track list beside it; the artist's words and top songs on the left, the circle and albums on the right.
        ' An album's More like this row sits on `relatedY`, its songs stopping above it.
        music: {relatedY: 895, coverX: 200, coverY: 150, cover: 480, wordsX: 760, eyebrowY: 150, titleY: 186, metaY: 340, buttonsY: 410, listY: 520, listWidth: 1064, row: 84,
            artistX: 200, artistEyebrowY: 170, artistTitleY: 206, artistMetaY: 352, artistButtonsY: 420, songsY: 620, songsWidth: 880, circleX: 1268, circleY: 140, circle: 412, albumsX: 1240, albumsY: 616}
        ' The now-playing card at the empty end of every page's header row: its corner, its size at rest and open.
        nowPlaying: {right: 1824, top: 44, width: 440, height: 84, openWidth: 700, openHeight: 470}
        ' The video player: the picture is the page. The top row hangs from `top` (on live its words from
        ' `liveTextX`, after the channel's badge); the seek bar,
        ' the time row and the button row sit on their own lines; everything runs from `left` to `right`.
        ' Over it: the waiting spinner's line, how far below a page notice Playback failed sits, the foot of the prompts at the lower right, and the Finished page's lines.
        player: {left: 96, right: 1824, top: 80, liveTextX: 184, barY: 828, timeY: 862, buttonsY: 912, waitY: 470, noticeDrop: 40, promptBottom: 796, finished: {top: 84, titleY: 120, metaY: 326, buttonsY: 372, rateY: 474, rowY: 578}}
        ' Recordings (from Live TV): the list's sections from `listY`, `width` wide, a section's heading
        ' `headingGap` above its card and `sectionGap` after it; the row in focus is kept above `bottom`.
        recordings: {listY: 176, width: 1300, headingGap: 52, sectionGap: 44, bottom: 1024}
        ' Every page beside the rail: its content ends at `rightEdge`.
        rightEdge: 1824
        ' Saved (tv-more frames 1 to 5), on Browse's lines: its groups `rowGap` apart, a heading `headingGap` over its rows, and what scrolls stops `bottom` above the foot.
        saved: {rowGap: 44, headingGap: 22, bottom: 56}
        ' Saved > Saved views (collections frame 17): a view's wide tile, `width` x `height` with its words `pad` inside and its covers `cover` wide, `gap` between tiles; three across.
        ' A saved view's own page (frame 21): the list page's header, then the library's toolbar on `toolsY` and its cards from `cardsY`.
        savedView: {width: 528, height: 300, pad: 28, cover: 60, gap: 20, toolsY: 392, cardsY: 490}
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
        state: {y: 300, width: 1000, buttonsY: 760,
            ' the line a page that keeps trying by itself says so on (states frame 5)
            statusY: 690}
        ' A long text read in full (a person's biography): the panel from `x`, its words from `pad` inside
        ' it to `padRight` from the screen's edge; the eyebrow on `top`, the title ending `titleGap` above
        ' `textY`, the text from `textY` to `textBottom`, the key hints on `hintsY`.
        textPanel: {x: 1020, pad: 56, padRight: 96, top: 64, titleGap: 24, textY: 220, textBottom: 960, hintsY: 1006}
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
        ' A short note at the top right (kit/Toast): its right edge and its top line, under the
        ' player's top row (tv-player frame 11, below).
        toast: {right: 1824, top: 168}
        ' Watch Together (tv-more frames 11 to 17). The lobby: its card from `cardY` (`cardWidth` x
        ' `cardHeight`, words `cardPad` inside) with its headline on `ledeY`, words on `wordsY` and
        ' buttons on `cardButtonsY`; "Groups you're in" on `groupsHeadY`, its rows from `groupsY`.
        ' The room: the eyebrow and the group's name on `eyebrowY` and `titleY`; what is on in the card
        ' from `nowY` (`nowWidth` x `nowHeight`), the buttons on `actionsY`, the queue's heading and
        ' rows from `queueHeadY` and `queueY`, down to the foot's buttons on `footY`; the people from
        ' `peopleX` (`peopleWidth` across), heading on `peopleHeadY`, rows from `peopleY`; the
        ' invitation's heading and card on `inviteHeadY` and `inviteY`. A notice takes the strip at
        ' `noticeY` and moves both columns down by `noticeShift` (frame 15). The sheets: Join with a
        ' code from `joinX`, its fields `fieldWidth` across and Portico's keyboard at `keysX`, `keysY`;
        ' Start a group from `startX`; their words `sheetPad` inside, from `sheetTop`; the fields from
        ' `joinFieldsY` or `startFieldsY`, `fieldPitch` apart, Join on `joinSubmitY`; Start's "who
        ' controls" on `startWhoY`, a refusal on `startNoteY` and Start on `startSubmitY`.
        together: {cardY: 150, cardWidth: 1180, cardHeight: 512, cardPad: 56, ledeY: 276, wordsY: 430, cardButtonsY: 538,
            groupsHeadY: 702, groupsY: 744, eyebrowY: 52, titleY: 80, nowY: 196, nowWidth: 840, nowHeight: 290, actionsY: 512,
            queueHeadY: 624, queueY: 664, footY: 962, peopleX: 1136, peopleWidth: 688, peopleHeadY: 196, peopleY: 236,
            inviteHeadY: 544, inviteY: 584, noticeY: 176, noticeShift: 120, joinX: 680, startX: 1160, sheetPad: 56,
            sheetTop: 64, fieldWidth: 440, fieldPitch: 135, keysX: 1224, keysY: 94, joinFieldsY: 228, joinSubmitY: 540,
            startFieldsY: 266, startWhoY: 574, startNoteY: 820, startSubmitY: 888}
    }
end function

' Page geometry shared by every screen. `cardPad` is the headroom above and below a card inside its
' list cell: a list cuts off whatever leaves the cell, and the card in focus grows.
function metrics() as object
    return {safeLeft: 200, safeRight: 56, railWidth: 150, railOpenWidth: 440, pageShift: 270, cardGap: 28, cardPad: 16, focusScale: 1.08, focusSpread: 30}
end function

' How much a card or face grows in focus: metrics().focusScale, or none at all with Reduce motion on
' (Settings, Appearance; MainScene keeps it in m.global.looks).
function focusZoom() as float
    if m.global <> invalid and m.global.looks <> invalid and m.global.looks.reduceMotion = true then return 1
    return metrics().focusScale
end function

' The ten-foot type scale (packages/design typeScale.tenFoot): [weight, size, line height or 0].
' Entries are kept one per line in alphabetical order so that changes to different screens do not
' collide; a new entry goes in its alphabetical place.
function typeRoles() as object
    return {
        about: [400, 24, 35]                ' a title page's or a person's About text
        ' the steps on a sign-in page (auth.html .tvl .lede)
        authBody: [400, 26, 38]
        authorInitials: [800, 130, 0]       ' an author's initials over the blur of their covers (collections frame 44)
        authTitle: [800, 72, 0]             ' before a profile is chosen: a page's title (auth.html .tvl h1, Who's watching)
        badge: [800, 18, 0]                 ' a small badge on a card
        body: [400, 24, 33]
        bodyStrong: [700, 24, 33]
        ' where you are in a book on an author's or a series' row ("Chapter 15 of 22, 4 h 34 min left")
        bookPlace: [700, 22, 0]
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
        faceLetter: [800, 26, 0]            ' a person's letter on a small face (Watch Together's people and the pill)
        faceName: [700, 30, 0]              ' before a profile is chosen: a face's name
        faceNote: [600, 22, 0]              ' before a profile is chosen: the line under a face's name
        factLabel: [400, 22, 0]             ' a fact's label in Details (Released, Directed by...)
        field: [700, 30, 0]                 ' what is typed in a sheet's field (kit/Field)
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
        rateLabel: [700, 22, 0]             ' "Rate this" before the stars on Finished (tv-player frame 7)
        rowHeading: [700, 32, 0]
        rowSubtitle: [400, 20, 0]
        seasonName: [700, 26, 0]
        section: [700, 26, 0]               ' a title page's section strip
        skipSeconds: [800, 14, 0]           ' the 10 and 30 inside the player's skip arrows
        synopsis: [400, 24, 35]
        tab: [700, 26, 0]
        tag: [600, 20, 0]                   ' a small label on a card, the hero or the rail
        title: [800, 44, 0]
        upNextTitle: [800, 34, 0]           ' the next title on the Up next card (tv-player frame 5)
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
