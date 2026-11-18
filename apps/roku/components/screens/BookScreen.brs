' Focus: the first button (Resume or Play); Down reaches the list, which keeps its row. Left from the
' buttons or the list opens the rail.
sub init()
    c = colours()
    f = frames().book
    m.f = f
    m.buttons = m.top.findNode("buttons")
    m.buttons.observeField("pressed", "onPressed")
    m.rows = m.top.findNode("rows")
    m.state = m.top.findNode("state")
    m.state.observeField("pressed", "onRetry")
    for each id in ["coverBack", "cover"]
        node = m.top.findNode(id)
        node.translation = [f.coverX, f.coverY]
        node.width = f.cover
        node.height = f.cover
    end for
    m.top.findNode("coverBack").blendColor = c.raised
    disc = m.top.findNode("disc")
    disc.translation = [f.coverX, f.groupY]
    disc.maskSize = [f.group, f.group]
    for each id in ["discBack", "discShade"]
        node = m.top.findNode(id)
        node.width = f.group
        node.height = f.group
        node.color = c.ink
    end for
    initials = m.top.findNode("initials")
    initials.translation = [f.coverX, f.groupY]
    initials.width = f.group
    initials.height = f.group
    setType(initials, "authorInitials", c.text)
    setType(m.top.findNode("eyebrow"), "eyebrow", c.accentStrong)
    title = m.top.findNode("title")
    title.width = f.listWidth
    setType(title, "lockupLong", c.text)
    meta = m.top.findNode("meta")
    meta.width = f.listWidth
    setType(meta, "meta", c.textSecondary)
    track = m.top.findNode("track")
    track.translation = [f.wordsX, f.progressY + 10]
    track.width = f.progressWidth
    track.color = c.track
    m.top.findNode("done").translation = [f.wordsX, f.progressY + 10]
    m.top.findNode("done").color = c.accent
    left = m.top.findNode("left")
    left.translation = [f.wordsX + f.progressWidth + 20, f.progressY - 4]
    setType(left, "metaStrong", c.textSecondary)
    heading = m.top.findNode("listHeading")
    heading.translation = [f.wordsX, f.listY - 44]
    setType(heading, "rowHeading", c.text)
    setType(m.top.findNode("listCount"), "tag", c.textTertiary)
    m.list = []
    m.focus = 0
    m.row = 0
    m.scroll = 0
end sub

sub onItem()
    item = m.top.item
    placeParts(item.kind <> "audiobook")
    m.top.findNode("title").text = item.title
    m.top.findNode("eyebrow").text = ""
    if item.kind = "audiobook"
        api("GET", "/v1/items/" + item.id, invalid, "onBook")
    else if item.kind = "series"
        m.top.findNode("eyebrow").text = "Series"
        api("GET", "/v1/items?series=" + item.id + "&sort=order&limit=100", invalid, "onBooks")     ' a series' books, in their order
    else
        m.top.findNode("eyebrow").text = "Author"
        api("GET", "/v1/items?library=" + item.libraryId + "&author=" + item.id + "&limit=100", invalid, "onBooks")
    end if
end sub

' A book has its big cover and its chapters under their heading. An author's circle or a series'
' cover is smaller and higher, its words lower beside it, and its books start higher (frames 44, 45).
sub placeParts(group as boolean)
    f = m.f
    drop = 0
    if group then drop = f.groupDrop
    m.top.findNode("eyebrow").translation = [f.wordsX, f.eyebrowY + drop]
    m.top.findNode("title").translation = [f.wordsX, f.titleY + drop]
    m.top.findNode("meta").translation = [f.wordsX, f.metaY + drop]
    m.buttons.translation = [f.wordsX, f.buttonsY + drop]
    for each id in ["coverBack", "cover"]
        node = m.top.findNode(id)
        if group
            node.translation = [f.coverX, f.groupY]
            node.width = f.group
            node.height = f.group
        else
            node.translation = [f.coverX, f.coverY]
            node.width = f.cover
            node.height = f.cover
        end if
    end for
    author = (m.top.item.kind = "author")
    m.top.findNode("coverBack").visible = not author
    m.top.findNode("cover").visible = not author
    m.top.findNode("disc").visible = author
    m.top.findNode("initials").visible = author
    if author then m.top.findNode("initials").text = initialsOf(m.top.item.title)
    window = m.top.findNode("window")
    window.translation = [f.wordsX, listTop()]
    window.clippingRect = [-50, -40, f.listWidth + 100, 1080 - listTop()]
end sub

function listTop() as integer
    if m.top.item.kind = "audiobook" then return m.f.listY
    return m.f.booksY
end function

' --- a book --------------------------------------------------------------------------------

sub onBook(event as object)
    result = event.getData()
    if not result.ok
        showState(result)
        return
    end if
    book = result.data
    m.book = book
    cover = pick(book.images, ["cover", "poster"])
    m.top.findNode("cover").uri = imageUrl(cover, m.f.cover, m.f.cover)
    if cover <> invalid and cover <> "" then m.top.art = "glow|" + cover
    m.author = invalid
    narrator = ""
    if book.credits <> invalid
        for each credit in book.credits
            if credit.role = "author" and m.author = invalid then m.author = credit
            if credit.role = "narrator" and narrator = "" then narrator = credit.name
        end for
    end if
    if book.series <> invalid then m.top.findNode("eyebrow").text = book.series.title
    m.chapters = bookChapters(book)
    parts = []
    if m.author <> invalid then parts.push(m.author.name)
    if narrator <> "" then parts.push("Read by " + narrator)
    if book.durationMs <> invalid then parts.push(durationText(book.durationMs))
    if m.chapters.count() > 0 then parts.push(countText(m.chapters.count(), "chapter", "chapters"))
    m.top.findNode("meta").text = parts.join("   ")
    ' your place and the time left, once a book is under way
    at = 0
    if book.state <> invalid and book.state.positionMs <> invalid then at = book.state.positionMs
    going = (at > 0 and book.state.watched <> true and book.durationMs <> invalid and book.durationMs > 0)
    m.top.findNode("track").visible = going
    m.top.findNode("done").visible = going
    m.top.findNode("left").visible = going
    buttons = []
    if going
        m.top.findNode("done").width = m.f.progressWidth * at / book.durationMs
        m.top.findNode("left").text = durationText(book.durationMs - at) + " left"
        label = "Resume"
        chapter = chapterAt(at)
        if chapter >= 0 then label = "Resume chapter " + (chapter + 1).toStr()
        buttons.push({id: "play", text: label, icon: "play"})
        buttons.push({id: "over", text: "Start over", icon: "restart"})
    else
        buttons.push({id: "play", text: "Play", icon: "play"})
    end if
    if m.author <> invalid or m.top.findNode("eyebrow").text <> "" then buttons.push({id: "more", text: "", icon: "more"})
    m.buttons.buttons = buttons
    m.list = []
    for i = 0 to m.chapters.count() - 1
        ch = m.chapters[i]
        m.list.push({number: (i + 1).toStr(), title: ch.title, duration: clockText(ch.endMs - ch.startMs), startMs: ch.startMs, endMs: ch.endMs})
    end for
    if m.list.count() > 0 then headList("Chapters", m.list.count()) else headList("", 0)
    ' the chapters already heard and the one you are in are marked only once the book is under way
    if not going then at = -1
    drawRows(at)
end sub

function chapterAt(ms as integer) as integer
    for i = 0 to m.chapters.count() - 1
        if ms >= m.chapters[i].startMs and ms < m.chapters[i].endMs then return i
    end for
    return -1
end function

' --- an author or a series -----------------------------------------------------------------

sub onBooks(event as object)
    result = event.getData()
    if not result.ok
        showState(result)
        return
    end if
    books = result.data.items
    m.books = books
    total = 0
    resume = invalid
    m.list = []
    for i = 0 to books.count() - 1
        book = books[i]
        if book.durationMs <> invalid then total = total + book.durationMs
        if resume = invalid and goingAt(book) > 0 then resume = book
        number = ""
        if m.top.item.kind = "series"
            number = "Book " + (i + 1).toStr()
            if book.index <> invalid then number = "Book " + book.index.toStr()
        end if
        entry = {art: imageUrl(pick(book.images, ["cover", "poster"]), 128, 128), number: number, title: book.title, length: durationText(book.durationMs), book: book}
        at = goingAt(book)
        if at > 0
            entry.progress = at / book.durationMs
            entry.place = durationText(book.durationMs - at) + " left"
        end if
        m.list.push(entry)
        ' who read it and the chapter you are in come from the book's own read
        if i < 24 then api("GET", "/v1/items/" + book.id, invalid, "onBookRead", {index: i, id: book.id})
    end for
    m.groupMeta = [countText(books.count(), "book", "books"), durationText(total)]
    m.top.findNode("meta").text = m.groupMeta.join("   ")
    if books.count() > 0
        first = pick(books[0].images, ["cover", "poster"])
        m.top.findNode("cover").uri = imageUrl(first, m.f.group, m.f.group)
        if first <> invalid and first <> "" then m.top.art = "glow|" + first
    end if
    if m.top.item.kind = "author" then drawDisc(books)
    m.resume = resume
    groupButtons(invalid)
    headList("", 0)
    drawRows(-1)
end sub

' A book's place, when it is under way and not finished; 0 otherwise.
function goingAt(book as object) as integer
    if book.state = invalid or book.state.positionMs = invalid or book.state.watched = true then return 0
    if book.durationMs = invalid or book.durationMs <= 0 then return 0
    return book.state.positionMs
end function

' Resume names the book on an author's page (frame 44) and its chapter on a series' (frame 45), where
' Start the series over goes back to book 1.
sub groupButtons(chapter as dynamic)
    buttons = []
    if m.resume <> invalid
        label = "Resume " + m.resume.title
        if m.top.item.kind = "series"
            label = "Resume"
            if chapter <> invalid then label = "Resume chapter " + chapter.toStr()
        end if
        buttons.push({id: "resume", text: label, icon: "play"})
        if m.top.item.kind = "series" then buttons.push({id: "seriesOver", text: "Start the series over", icon: "restart"})
    else if m.books.count() > 0
        buttons.push({id: "first", text: "Play " + m.books[0].title, icon: "play"})
    end if
    m.buttons.buttons = buttons
end sub

sub onBookRead(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if not result.ok or ctx.index >= m.list.count() or m.list[ctx.index].book.id <> ctx.id then return
    book = result.data
    entry = m.list[ctx.index]
    credits = book.credits
    if credits = invalid then credits = []
    for each credit in credits
        if credit.role = "narrator" and entry.sub = invalid then entry.sub = "Read by " + credit.name
        ' a series names its author first among its facts (frame 45)
        if credit.role = "author" and ctx.index = 0 and m.top.item.kind = "series" and m.groupMeta.count() = 2
            m.groupMeta.unshift(credit.name)
            m.top.findNode("meta").text = m.groupMeta.join("   ")
        end if
    end for
    at = goingAt(book)
    chapters = bookChapters(book)
    if at > 0 and chapters.count() > 0
        n = 0
        for i = 0 to chapters.count() - 1
            if at >= chapters[i].startMs then n = i + 1
        end for
        entry.place = "Chapter " + n.toStr() + " of " + chapters.count().toStr() + ", " + durationText(book.durationMs - at) + " left"
        if m.resume <> invalid and m.resume.id = book.id and m.top.item.kind = "series" then groupButtons(n)
    end if
    if ctx.index < m.rows.getChildCount() then m.rows.getChild(ctx.index).model = rowModel(entry)
end sub

' The author's circle: up to four of their covers, blurred (the server's ambient wash of each), side by
' side and darkened under the initials, as the native TV app draws it. Every cover stays inside the
' circle's square: the mask cuts only within it, so anything past its edge would show.
sub drawDisc(books as object)
    covers = m.top.findNode("discCovers")
    covers.removeChildrenIndex(covers.getChildCount(), 0)
    n = books.count()
    if n > 4 then n = 4
    for i = 0 to n - 1
        poster = covers.createChild("Poster")
        poster.loadDisplayMode = "scaleToZoom"
        poster.width = m.f.group / n
        poster.height = m.f.group
        poster.translation = [i * m.f.group / n, 0]
        poster.opacity = 0.56
        poster.uri = ambientUrl(pick(books[i].images, ["cover", "poster"]))
    end for
end sub

' A book's chapters are lines of words; an author's or a series' books carry their covers, a taller row.
function rowPitch() as integer
    if m.top.item.kind = "audiobook" then return m.f.row
    return m.f.bookRow + m.f.bookGap
end function

' --- the list ------------------------------------------------------------------------------

' The list's heading with its count beside it, in grey ("Chapters  17").
sub headList(text as string, count as integer)
    heading = m.top.findNode("listHeading")
    heading.text = text
    label = m.top.findNode("listCount")
    label.text = ""
    if text <> "" then label.text = count.toStr()
    label.translation = [m.f.wordsX + heading.boundingRect().width + 16, m.f.listY - 44 + 9]
end sub

sub drawRows(atMs as integer)
    m.rows.removeChildrenIndex(m.rows.getChildCount(), 0)
    for i = 0 to m.list.count() - 1
        entry = m.list[i]
        if entry.book <> invalid
            row = m.rows.createChild("BookRow")
            row.rowWidth = m.f.listWidth
            row.rowHeight = m.f.bookRow
            row.translation = [0, i * rowPitch()]
            row.model = rowModel(entry)
        else
            row = m.rows.createChild("TrackRow")
            row.rowWidth = m.f.listWidth
            row.rowHeight = rowPitch()
            row.translation = [0, i * rowPitch()]
            model = {art: entry.art, number: entry.number, title: entry.title, sub: entry.sub, duration: entry.duration}
            ' a chapter already heard is ticked and dimmed; the one you are in is lit, with your place in it
            if entry.startMs <> invalid and atMs >= 0
                model.done = (entry.endMs <= atMs)
                if atMs >= entry.startMs and atMs < entry.endMs
                    model.playing = true
                    model.progress = (atMs - entry.startMs) / (entry.endMs - entry.startMs)
                end if
            end if
            row.model = model
        end if
    end for
    paintRows()
end sub

function rowModel(entry as object) as object
    return {art: entry.art, number: entry.number, title: entry.title, sub: entry.sub, place: entry.place, length: entry.length, progress: entry.progress}
end function

function clockText(ms as dynamic) as string
    if ms = invalid then return ""
    total = int(ms / 1000)
    h = int(total / 3600)
    mins = int((total mod 3600) / 60)
    secs = total mod 60
    text = ""
    if h > 0 then text = h.toStr() + ":"
    if h > 0 and mins < 10 then text = text + "0"
    text = text + mins.toStr() + ":"
    if secs < 10 then text = text + "0"
    return text + secs.toStr()
end function

sub paintRows()
    for i = 0 to m.rows.getChildCount() - 1
        m.rows.getChild(i).on = (m.focus = 1 and i = m.row and m.top.isInFocusChain())
    end for
    room = 1080 - listTop() - rowPitch() - 40
    y = m.row * rowPitch()
    if y - m.scroll > room then m.scroll = y - room
    if y < m.scroll then m.scroll = y
    m.rows.translation = [0, -m.scroll]
end sub

sub showState(result as object)
    m.state.icon = "warning"
    m.state.title = "This page could not be loaded"
    m.state.body = result.error.message
    m.state.buttons = [{id: "retry", text: "Try again", icon: "refresh"}]
    m.state.visible = true
    m.state.active = true
end sub

sub onRetry()
    m.state.visible = false
    onItem()
    onActive()
end sub

sub onActive()
    if not m.top.active then return
    ' back from the player or a book: the places and the button that resumes them are read again
    if m.shownOnce = true then onItem()
    m.shownOnce = true
    if m.state.visible
        m.state.active = true
    else if m.focus = 1 and m.list.count() > 0
        m.rows.setFocus(true)
    else
        m.focus = 0
        m.buttons.active = true
    end if
    paintRows()
end sub

' --- playing -------------------------------------------------------------------------------

sub playBook(book as object, fromStart as boolean)
    m.top.open = {item: {id: book.id, kind: "audiobook", title: book.title, rootTitle: authorOf(book), images: book.images, durationMs: book.durationMs}, play: true, startOver: fromStart}
end sub

function authorOf(book as object) as dynamic
    if m.author <> invalid then return m.author.name
    if book.credits <> invalid
        for each credit in book.credits
            if credit.role = "author" then return credit.name
        end for
    end if
    if m.top.item.kind = "author" then return m.top.item.title
    return invalid
end function

sub onPressed(event as object)
    which = event.getData()
    if which = "play" then playBook(m.book, false)
    if which = "over" then playBook(m.book, true)
    if which = "resume" then playBook(m.resume, false)
    if which = "first" then playBook(m.books[0], false)
    if which = "seriesOver" then playBook(m.books[0], true)
    if which = "more"
        rows = []
        if m.author <> invalid then rows.push({id: "author", text: "Go to " + m.author.name, icon: ""})
        if m.book.series <> invalid then rows.push({id: "series", text: "Go to " + m.book.series.title, icon: ""})
        m.top.menu = {eyebrow: "", title: m.book.title, status: "", rows: rows}
    end if
end sub

sub onMenuChosen(event as object)
    which = event.getData()
    if which = "author" then m.top.open = {item: {kind: "author", id: m.author.personId, title: m.author.name, libraryId: m.book.libraryId}, play: false}
    if which = "series" then m.top.open = {item: {kind: "series", id: m.book.series.id, title: m.book.series.title, libraryId: m.book.libraryId}, play: false}
end sub

' A chapter starts the book from its start; a book in the list plays.
sub chooseRow()
    entry = m.list[m.row]
    if entry.book <> invalid
        ' a book in an author's or a series' list opens its page, as a card does; Resume there plays
        m.top.open = {item: entry.book, play: false}
    else
        m.top.open = {item: {id: m.book.id, kind: "audiobook", title: m.book.title, rootTitle: authorOf(m.book), images: m.book.images, durationMs: m.book.durationMs}, play: true, startAtMs: entry.startMs}
    end if
end sub

function pressKey(key as string) as boolean
    if m.focus <> 1 then return false
    if key = "up" and m.row > 0
        m.row = m.row - 1
    else if key = "down" and m.row < m.list.count() - 1
        m.row = m.row + 1
    else
        return false
    end if
    paintRows()
    return true
end function

function onKeyEvent(key as string, press as boolean) as boolean
    holdKey(key, press and m.focus = 1 and (key = "up" or key = "down"))
    if not press or m.state.visible then return false
    if m.focus = 0
        if key = "down" and m.list.count() > 0
            m.focus = 1
            m.rows.setFocus(true)
            paintRows()
            return true
        end if
        return false
    end if
    if key = "OK"
        chooseRow()
        return true
    else if key = "up" and m.row = 0
        m.focus = 0
        m.buttons.active = true
        paintRows()
        return true
    end if
    return pressKey(key)
end function
