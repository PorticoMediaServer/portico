sub init()
    m.page = m.top.findNode("page")
    m.hero = m.top.findNode("hero")
    m.notice = m.top.findNode("notice")
    m.slide = m.top.findNode("slide")
    m.slideTo = m.top.findNode("slideTo")
    m.turn = m.top.findNode("turn")
    m.rows = []        ' {row, shelf, y, asked}; shelf is made when focus comes near
    m.spots = []
    m.spot = 0
    m.focus = -2       ' -2 nothing to focus yet, -1 the spotlight, otherwise a row
    m.hero.observeField("pressed", "onHeroPressed")
    m.notice.observeField("pressed", "load")
    m.turn.observeField("fire", "onTurn")
    load()
end sub

sub load()
    m.notice.visible = false
    api("GET", "/v1/home", invalid, "onHome")
end sub

sub onHome(event as object)
    result = event.getData()
    if not result.ok
        say("warning", "Can't reach the server", "You're still signed in. Try again in a moment.", [{id: "retry", text: "Try again now", icon: "refresh"}])
        return
    end if
    home = result.data
    m.spots = []
    if home.spotlight <> invalid
        for each spot in home.spotlight
            if not isReading(spot.entry) then m.spots.push(spot)
        end for
    end if
    m.rows = []
    for each row in home.rows
        row.items = withoutReading(row.items)
        ' A row is kept if it has titles, or will read them as it comes near (`deferred`).
        if row.hidden <> true and row.id <> "continue_reading" and (row.items.count() > 0 or row.deferred = true)
            m.rows.push({row: row, shelf: invalid, y: 0, asked: false})
        end if
    end for
    place()
    if m.spots.count() = 0 and m.rows.count() = 0
        say("", "Nothing here yet", "Titles show up on Home as they are added to this server's libraries.", [{id: "retry", text: "Look again", icon: "refresh"}])
        return
    end if
    m.hero.visible = true
    if m.spots.count() > 0 then moveTo(-1) else moveTo(0)
    if m.spots.count() > 1 then m.turn.control = "start"
end sub

' A whole-page message in place of Home, with focus on its button.
sub say(icon as string, title as string, body as string, buttons as object)
    m.focus = -2
    m.hero.visible = false
    m.notice.icon = icon
    m.notice.title = title
    m.notice.body = body
    m.notice.buttons = buttons
    m.notice.visible = true
    m.notice.active = true
end sub

' Give each row its place down the page. A row with nothing to show yet takes no room.
sub place()
    y = 0
    for each entry in m.rows
        entry.y = y
        if entry.row.items.count() > 0 then y = y + shelfMetrics(entry.row.shape).block
        if entry.shelf <> invalid then entry.shelf.translation = [0, entry.y]
    end for
end sub

' The address of more of a row's titles: the row names its library and query (API.md, Home).
function rowItemsPath(row as object, cursor as dynamic) as string
    path = "/v1/items?library=" + row.library
    if row.query <> invalid and row.query <> "" then path = path + "&" + row.query
    if cursor <> invalid then path = path + "&cursor=" + cursor
    return path
end function

' Read a row's titles when it comes near: a deferred row's first ones, or the next page.
sub readRow(i as integer)
    entry = m.rows[i]
    row = entry.row
    if entry.asked or row.library = invalid then return
    if row.deferred = true and row.items.count() = 0
        entry.asked = true
        api("GET", rowItemsPath(row, invalid), invalid, "onRowItems", {index: i, id: row.id})
    else if row.nextCursor <> invalid and entry.shelf <> invalid and entry.shelf.focusedIndex >= row.items.count() - 10
        entry.asked = true
        api("GET", rowItemsPath(row, row.nextCursor), invalid, "onRowItems", {index: i, id: row.id})
    end if
end sub

sub onRowItems(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if ctx.index >= m.rows.count() then return
    entry = m.rows[ctx.index]
    if entry.row.id <> ctx.id then return
    entry.asked = false
    if not result.ok
        entry.row.nextCursor = invalid   ' the row stops where it is; nothing moves
        return
    end if
    more = withoutReading(result.data.items)
    entry.row.items.append(more)
    entry.row.nextCursor = result.data.nextCursor
    entry.row.deferred = false
    if entry.shelf <> invalid
        appendCards(entry.shelf.cards, entry.row.shape, more)
    else
        place()
        if m.focus >= 0 then makeNear(m.focus)
    end if
end sub

sub onActive()
    if not m.top.active then return
    if m.notice.visible
        m.notice.active = true
    else if m.focus = -1
        m.hero.active = true
    else if m.focus >= 0
        m.rows[m.focus].shelf.active = true
    end if
end sub

sub appendCards(cards as object, shape as string, items as object)
    g = shelfMetrics(shape).card
    for each item in items
        card = cardFor(item, shape)
        card.art = imageUrl(card.art, g[0], g[1])
        cards.createChild("ContentNode").addFields(card)
    end for
end sub

function makeShelf(i as integer) as dynamic
    entry = m.rows[i]
    if entry.shelf <> invalid then return entry.shelf
    row = entry.row
    if row.items.count() = 0 then return invalid
    shelf = m.page.createChild("Shelf")
    shelf.translation = [0, entry.y]
    shelf.shape = row.shape
    shelf.heading = row.title
    if row.reason <> invalid then shelf.subheading = row.reason
    cards = createObject("roSGNode", "ContentNode")
    appendCards(cards, row.shape, row.items)
    shelf.cards = cards
    shelf.observeField("focusedIndex", "onCardFocused")
    shelf.observeField("selectedIndex", "onCardSelected")
    entry.shelf = shelf
    return shelf
end function

' Make the rows near focus, and ask for the titles of those that have not read theirs yet.
sub makeNear(i as integer)
    for n = 0 to i + 2
        if n < m.rows.count()
            readRow(n)
            makeShelf(n)
        end if
    end for
end sub

' The nearest row at or after i (or before it, going up) that has cards; -1 for the spotlight, -2 for none.
function rowFrom(i as integer, direction as integer) as integer
    while i >= 0 and i < m.rows.count()
        if m.rows[i].row.items.count() > 0 then return i
        i = i + direction
    end while
    if i < 0 and m.spots.count() > 0 then return -1
    return -2
end function

' Move focus to the spotlight (-1) or a row, and bring the page to it.
sub moveTo(i as integer)
    makeNear(i)
    if i >= 0 then makeShelf(i)
    m.focus = i
    pageY = 830
    first = rowFrom(0, 1)
    if i = -1
        m.hero.mode = "spotlight"
        showSpot()
        m.hero.visible = true
        if m.top.visible then m.hero.active = true
    else
        m.hero.mode = "preview"
        m.hero.visible = (i = first)
        pageY = 507 - m.rows[i].y
        if i = first then pageY = 595 - m.rows[i].y
        if m.top.visible then m.rows[i].shelf.active = true
        describe()
    end if
    m.slide.control = "stop"
    m.slideTo.keyValue = [m.page.translation, [0, pageY]]
    m.slide.control = "start"
end sub

function focusedItem() as dynamic
    if m.focus < 0 then return invalid
    entry = m.rows[m.focus]
    at = entry.shelf.focusedIndex
    if at < 0 or at >= entry.row.items.count() then at = 0
    return entry.row.items[at]
end function

' The hero and the backdrop follow the card in focus.
sub describe()
    item = focusedItem()
    if item = invalid then return
    model = heroFor(item)
    m.top.art = model.art
    if m.focus <> rowFrom(0, 1) then return
    ok = "Details"
    if m.rows[m.focus].row.id = "continue" then ok = "Resume"
    m.hero.okText = ok
    m.hero.model = model
end sub

sub showSpot()
    spot = m.spots[m.spot]
    model = heroFor(spot.entry)
    model.eyebrow = spot.reason.text.fallback
    buttons = []
    if spot.play
        text = "Play"
        target = spot.resumeTarget
        if target <> invalid
            if progressOf(target) >= 0
                text = "Resume"
            else if target.kind = "episode" and episodeCode(target) <> ""
                text = "Play " + episodeCode(target)
            end if
        end if
        buttons.push({id: "play", text: text, icon: "play"})
    end if
    if spot.myList
        if spot.entry.state.inList
            buttons.push({id: "list", text: "My List", icon: "check"})
        else
            buttons.push({id: "list", text: "My List", icon: "plus"})
        end if
    end if
    buttons.push({id: "details", text: "Details", icon: ""})
    m.hero.pages = m.spots.count()
    m.hero.page = m.spot
    m.hero.buttons = buttons
    m.hero.model = model
    m.top.art = model.art
end sub

' The spotlight turns by itself only while it is the opening state; focus stays where it is.
sub onToTop()
    if m.focus = -2 then return
    if m.spots.count() > 0 then moveTo(-1) else moveTo(rowFrom(0, 1))
end sub

sub onTurn()
    ' never behind the rail or a sheet: the page you left is the page you come back to
    if m.focus <> -1 or not m.top.isInFocusChain() then return
    m.spot = (m.spot + 1) mod m.spots.count()
    showSpot()
end sub

sub onCardFocused(event as object)
    if m.focus < 0 or not event.getRoSGNode().isSameNode(m.rows[m.focus].shelf) then return
    describe()
    readRow(m.focus)   ' rows keep going: the next page is asked for about ten cards before the end
end sub

sub onCardSelected(event as object)
    item = focusedItem()
    if item <> invalid then m.top.open = {item: item, play: m.rows[m.focus].row.id = "continue"}
end sub

sub onHeroPressed(event as object)
    spot = m.spots[m.spot]
    which = event.getData()
    if which = "play"
        target = spot.entry
        if spot.resumeTarget <> invalid then target = spot.resumeTarget
        m.top.open = {item: target, play: true}
    else if which = "details"
        m.top.open = {item: spot.entry, play: false}
    else if which = "list"
        ' My List changes at once on the screen; the server is told, and a refusal puts it back.
        spot.entry.state.inList = not spot.entry.state.inList
        m.spots[m.spot] = spot
        showSpot()
        api("PUT", "/v1/items/" + spot.entry.id + "/state", {inList: spot.entry.state.inList, madeAgoMs: 0}, "onListSaved", {index: m.spot, id: spot.entry.id})
    end if
end sub

sub onListSaved(event as object)
    if event.getData().ok then return
    ctx = event.getRoSGNode().ctx
    if ctx.index >= m.spots.count() or m.spots[ctx.index].entry.id <> ctx.id then return
    spot = m.spots[ctx.index]
    spot.entry.state.inList = not spot.entry.state.inList
    m.spots[ctx.index] = spot
    if m.focus = -1 and m.spot = ctx.index then showSpot()
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    holdKey(key, press and (key = "up" or key = "down"))
    if not press then return false
    return pressKey(key)
end function

function pressKey(key as string) as boolean
    if m.focus = -2 then return false
    if key = "down"
        target = rowFrom(m.focus + 1, 1)
        if target >= 0 then moveTo(target)
        return true
    else if key = "up"
        target = rowFrom(m.focus - 1, -1)
        if m.focus >= 0 and target <> -2 then moveTo(target)
        return true
    else if key = "back"
        ' Back from anywhere on Home is the opening state; from there it leaves the app.
        if m.focus >= 0 and m.spots.count() > 0
            moveTo(-1)
            return true
        end if
    end if
    return false
end function
