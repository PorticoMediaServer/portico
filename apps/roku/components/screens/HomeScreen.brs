' Home opens on the first card of the first row (Continue Watching when there is one). Up and Down
' move between rows, each row keeps its card, and the hero and the backdrop describe the card in
' focus wherever it is. Back goes to that opening state, and from there leaves the app.
sub init()
    f = frames()
    m.rowsY = f.browse.rowsY
    m.page = m.top.findNode("page")
    m.hero = m.top.findNode("hero")
    m.hero.translation = [f.gutter, f.browse.heroTop]
    ' rows leaving upwards are cut a little above the slot's heading, under the hero's hints
    m.top.findNode("window").clippingRect = [0, m.rowsY - 30, 1920, 1080 - m.rowsY + 30]
    m.notice = m.top.findNode("notice")
    m.slide = m.top.findNode("slide")
    m.slideTo = m.top.findNode("slideTo")
    m.rows = []        ' {row, shelf, y, asked}; shelf is made when focus comes near
    m.details = {}     ' item id -> the item in full, for the hero's synopsis
    m.rest = m.top.findNode("rest")
    m.rest.observeField("fire", "readDetail")
    m.loadingDelay = m.top.findNode("loadingDelay")
    m.loadingDelay.observeField("fire", "onLoadingDelay")
    m.focus = -2       ' -2 nothing to focus yet, otherwise the row in focus
    m.notice.observeField("pressed", "load")
    load()
end sub

' Home's rows, or a library's own (its Discover tab), which have the same shape.
function homePath() as string
    if m.top.library <> "" then return "/v1/libraries/" + m.top.library + "/landing"
    return "/v1/home"
end function

sub load()
    m.notice.visible = false
    m.loadingDelay.control = "start"
    api("GET", homePath(), invalid, "onHome", {path: homePath()})
end sub

sub onHome(event as object)
    result = event.getData()
    ' a Discover tab is made as Home and then told its library: Home's own answer is not its
    if event.getRoSGNode().ctx.path <> homePath() then return
    if not result.ok
        if result.status = 0
            say("warning", "Can't reach the server", "You're still signed in. Try again in a moment.", [{id: "retry", text: "Try again now", icon: "refresh"}])
        else
            ' the server answered and refused (viewing hours, away from home): its own sentence says why
            say("warning", "Home isn't available right now", result.error.message, [{id: "retry", text: "Try again now", icon: "refresh"}])
        end if
        return
    end if
    home = result.data
    ' a late answer never takes focus from the rail or a sheet: only a Home that had it keeps it
    own = m.top.isInFocusChain()
    for each entry in m.rows
        if entry.shelf <> invalid then m.page.removeChild(entry.shelf)
    end for
    m.rows = []
    m.focus = -2
    for each row in home.rows
        row.items = withoutReading(row.items)
        ' A row is kept if it has titles, or will read them as it comes near (`deferred`).
        if row.hidden <> true and row.id <> "continue_reading" and (row.items.count() > 0 or row.deferred = true)
            ' the row in focus keeps its order, whatever the server now says (tv-focus frame 6)
            if m.keep <> invalid and m.keep.row = row.id and m.keep.order <> invalid then row.items = keptOrder(m.keep.order, row.items)
            fitShape(row)
            m.rows.push({row: row, shelf: invalid, y: 0, asked: false})
        end if
    end for
    place()
    if rowFrom(0, 1) < 0
        ' only rows that read their titles later: read them in turn until one has some
        if readDeferred() then return
        say("", "Nothing here yet", "Titles show up on Home as they are added to this server's libraries.", [{id: "retry", text: "Look again", icon: "refresh"}])
        return
    end if
    m.hero.visible = true
    if m.keep <> invalid and m.keep.row <> ""
        keep = m.keep
        m.keep = invalid
        ids = [keep.row]
        ids.append(keep.after)
        for each id in ids
            for i = 0 to m.rows.count() - 1
                if m.rows[i].row.id = id and m.rows[i].row.items.count() > 0
                    moveTo(i, own)
                    return
                end if
            end for
        end for
    end if
    m.keep = invalid
    moveTo(rowFrom(0, 1), own)
end sub

' A row whose titles all have square covers (audiobooks, music) is a row of squares, not posters
' with bands above and below (rule 7: one shape to a row, squares for music and audiobooks).
sub fitShape(row as object)
    target = row
    if row.out <> invalid then target = row.out
    if row.shape = "landscape" or row.items.count() = 0 then return
    for each item in row.items
        if not isCover(item.kind) then return
    end for
    target.shape = "square"
end sub

' A whole-page message in place of Home, with focus on its button.
' Home has been loading for 300 ms: the hero's lines and the first row as outlines. Home itself
' keeps focus; an outline never takes it.
sub onLoadingDelay()
    if m.focus <> -2 or m.notice.visible then return
    row = m.top.findNode("loadingRow")
    if row.getChildCount() = 0
        g = shelfMetrics("landscape")
        k = metrics()
        x = frames().gutter
        heading = row.createChild("Poster")
        heading.translation = [x, m.rowsY + 6]
        heading.width = 300
        heading.height = 32
        for i = 0 to 3
            card = row.createChild("Poster")
            card.translation = [x + i * (g.card[0] + k.cardGap), m.rowsY + g.headingGap]
            card.width = g.card[0]
            card.height = g.card[1]
        end for
        for each part in row.getChildren(-1, 0)
            part.uri = "pkg:/images/round-14.9.png"
            part.blendColor = colours().raisedHigh
        end for
    end if
    row.visible = true
    m.hero.visible = true
    m.hero.loading = true
end sub

sub stopLoading()
    m.loadingDelay.control = "stop"
    m.top.findNode("loadingRow").visible = false
    m.hero.loading = false
end sub

sub say(icon as string, title as string, body as string, buttons as object)
    stopLoading()
    m.focus = -2
    m.hero.visible = false
    m.top.art = ""
    m.notice.icon = icon
    m.notice.title = title
    m.notice.body = body
    m.notice.buttons = buttons
    m.notice.visible = true
    if m.top.isInFocusChain() then m.notice.active = true
end sub

' Give each row its place down the page. A row with nothing to show yet takes no room. A row that
' fills in above the one in focus never moves it: the page keeps the focused row in its slot.
sub place()
    y = 0
    for each entry in m.rows
        entry.y = y
        if entry.row.items.count() > 0 then y = y + shelfMetrics(entry.row.shape).block
        if entry.shelf <> invalid then entry.shelf.translation = [0, entry.y]
    end for
    if m.focus >= 0 and m.slide.state <> "running" then m.page.translation = [0, m.rowsY - m.rows[m.focus].y]
end sub

' The address of more of a row's titles: every row, built-in, saved view or collection, goes on
' at /v1/home/rows/{id}, the same way See all pages it.
function rowItemsPath(row as object, cursor as dynamic) as string
    path = "/v1/home/rows/" + row.id + "?"
    if m.top.library <> "" then path = path + "library=" + m.top.library + "&"
    if cursor <> invalid then path = path + "cursor=" + cursor
    return path
end function

' Read a row's titles when it comes near: a deferred row's first ones, or the next page.
sub readRow(i as integer)
    entry = m.rows[i]
    row = entry.row
    if entry.asked then return
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
        dropSkeleton(entry)
        ' a row that has titles keeps its cursor, and the next card in focus asks again; an unread
        ' row is not asked again and again
        entry.row.deferred = false
        if m.focus = -2 then onHomeEmpty()
        return
    end if
    more = withoutReading(result.data.items)
    if entry.row.items.count() = 0 then fitShape({items: more, shape: entry.row.shape, out: entry.row})
    entry.row.items.append(more)
    entry.row.nextCursor = result.data.nextCursor
    entry.row.deferred = false
    if entry.shelf <> invalid
        appendCards(entry.shelf.cards, entry.row.shape, more, entry.skeleton = true)
        dropSkeleton(entry)
        if ctx.index = m.focus then describe()     ' focus may be waiting where the skeleton was
    else
        place()
        if m.focus >= 0 then makeNear(m.focus)
    end if
    if m.focus = -2 then onHomeEmpty()
end sub

' Home has nothing to focus yet: the first row that has titles takes focus, or the next deferred
' row is read, or, when none is left, Home says it is empty.
sub onHomeEmpty()
    first = rowFrom(0, 1)
    if first >= 0
        m.hero.visible = true
        moveTo(first, m.top.isInFocusChain())
    else if not readDeferred()
        say("", "Nothing here yet", "Titles show up on Home as they are added to this server's libraries.", [{id: "retry", text: "Look again", icon: "refresh"}])
    end if
end sub

' Ask for the first deferred row that has not read its titles; false when there is none.
function readDeferred() as boolean
    for i = 0 to m.rows.count() - 1
        row = m.rows[i].row
        if row.deferred = true and row.items.count() = 0
            if not m.rows[i].asked then readRow(i)
            return true
        end if
    end for
    return false
end function

sub onActive()
    if not m.top.active then return
    ' back from a title's page or the player: what was played or changed there shows here
    if m.covered = true and not m.notice.visible
        m.covered = false
        m.top.covered = false
        refresh()
    end if
    if m.notice.visible
        m.notice.active = true
    else if m.focus = -2
        m.top.setFocus(true)     ' still loading: Home holds focus until a row can take it
    else
        m.rows[m.focus].shelf.active = true
    end if
end sub

sub appendCards(cards as object, shape as string, items as object, beforeLast = false as boolean)
    ' With the skeleton at the row's end the cards go in before it, so focus waiting on it is on the
    ' page's first card once they are in: taking out the card focus is on would send a row back to
    ' its first card (walk result pages-03, step B4).
    g = shelfMetrics(shape).card
    for each item in items
        card = cardFor(item, shape)
        card.art = imageUrl(card.art, g[0], g[1])
        node = createObject("roSGNode", "ContentNode")
        node.addFields(card)
        if beforeLast then cards.insertChild(node, cards.getChildCount() - 1) else cards.appendChild(node)
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
    ' Home read again: the row keeps the card it had (the last one, when it has fewer now)
    if m.memory <> invalid and m.memory[row.id] <> invalid
        at = m.memory[row.id]
        if at > row.items.count() - 1 then at = row.items.count() - 1
        if at > 0 then shelf.jumpTo = at
    end if
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

' The nearest row at or after i (or before it, going up) that has cards; -1 for none.
function rowFrom(i as integer, direction as integer) as integer
    while i >= 0 and i < m.rows.count()
        if m.rows[i].row.items.count() > 0 then return i
        i = i + direction
    end while
    return -1
end function

' Move focus to a row, and slide the page so that row's heading sits on the slot. `grab` false only
' marks where focus will be, for a page that does not have focus now (the rail or a sheet has it).
sub moveTo(i as integer, grab = true as boolean)
    if i < 0 then return
    stopLoading()
    makeNear(i)
    m.focus = i
    if grab then m.rows[i].shelf.active = true
    describe()
    m.slide.control = "stop"
    m.slideTo.keyValue = [m.page.translation, [0, m.rowsY - m.rows[i].y]]
    m.slide.control = "start"
end sub

function focusedItem() as dynamic
    if m.focus < 0 then return invalid
    entry = m.rows[m.focus]
    at = entry.shelf.focusedIndex
    if at < 0 then at = 0
    if at >= entry.row.items.count() then return invalid     ' the skeleton card: no title yet
    return entry.row.items[at]
end function

' The hero and the backdrop follow the card in focus. The card's own words show at once; its
' synopsis and the rest fill their slots when the title is read in full, so nothing moves.
sub describe()
    item = focusedItem()
    if item = invalid then return
    full = m.details[readFor(item)]
    if full = invalid
        m.rest.control = "stop"
        m.rest.control = "start"
    end if
    if item.kind = "episode"
        ' an episode keeps its own eyebrow, title and progress; its show gives the meta line, the
        ' synopsis and the backdrop (tv.html frame 1)
        model = heroFor(item)
        if full <> invalid
            show = heroFor(full)
            model.year = show.year
            model.facts = show.facts
            model.tag = show.tag
            model.synopsis = show.synopsis
            model.art = show.art
        end if
    else
        if full = invalid then full = item
        model = heroFor(full)
    end if
    m.top.art = model.art
    ok = "Details"
    if m.rows[m.focus].row.id = "continue" then ok = "Resume"
    m.hero.okText = ok
    m.hero.model = model
end sub

' What the hero reads in full for a card: the title itself, or for an episode its show.
function readFor(item as object) as string
    if item.kind = "episode" and item.rootId <> invalid then return item.rootId
    return item.id
end function

sub readDetail()
    item = focusedItem()
    if item = invalid or m.details[readFor(item)] <> invalid then return
    api("GET", "/v1/items/" + readFor(item), invalid, "onDetail", {id: readFor(item)})
end sub

sub onDetail(event as object)
    result = event.getData()
    if not result.ok then return
    m.details[event.getRoSGNode().ctx.id] = result.data
    item = focusedItem()
    if item <> invalid and readFor(item) = event.getRoSGNode().ctx.id then describe()
end sub

sub onCovered()
    if m.top.covered then m.covered = true
end sub

' The opening state: the page at the top, focus on the first row at the card it remembers.
sub onToTop()
    if m.focus >= 0 then moveTo(rowFrom(0, 1))
end sub

sub onCardFocused(event as object)
    if m.focus < 0 or not event.getRoSGNode().isSameNode(m.rows[m.focus].shelf) then return
    describe()
    readRow(m.focus)   ' rows keep going: the next page is asked for about ten cards before the end
    ' focus reached the last loaded card before the next page came: one skeleton card after it,
    ' and focus can wait on it; the first card of the page takes its place when it arrives
    entry = m.rows[m.focus]
    if entry.row.nextCursor <> invalid and entry.skeleton <> true and entry.shelf.focusedIndex >= entry.row.items.count() - 1
        entry.skeleton = true
        ' made whole before it joins the row: the card in that slot draws it at once
        card = createObject("roSGNode", "ContentNode")
        card.addFields({skeleton: true, kind: "", art: "", title: ""})
        entry.shelf.cards.appendChild(card)
    end if
end sub

' The skeleton card at a row's end goes as the next page arrives (or fails).
sub dropSkeleton(entry as object)
    if entry.skeleton <> true or entry.shelf = invalid then return
    entry.skeleton = false
    cards = entry.shelf.cards
    last = cards.getChildCount() - 1
    onIt = (entry.shelf.focusedIndex >= last)
    cards.removeChildIndex(last)
    ' the page did not come and focus was on the skeleton: back to the last card, not the first
    if onIt and last > 0 then entry.shelf.jumpTo = last - 1
end sub

sub onCardSelected(event as object)
    item = focusedItem()
    if item = invalid then return
    if m.rows[m.focus].row.id = "continue" then m.top.open = {item: item, play: true} else openPage(item)
end sub

' Open a title's page. An episode has none of its own: its show's page opens (a show page starts
' on the season with something left to play).
sub openPage(item as object)
    if item.kind = "episode" and item.rootId <> invalid
        m.top.open = {item: {id: item.rootId, kind: "show", title: item.rootTitle}, play: false}
    else
        m.top.open = {item: item, play: false}
    end if
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    holdKey(key, press and (key = "up" or key = "down"))
    if not press then return false
    if key = "options" and m.focus >= 0
        openMenu()
        return true
    end if
    ' a library's Discover gives Up from its first row to the page's tabs
    if key = "up" and m.top.library <> "" and rowFrom(m.focus - 1, -1) < 0 then return false
    return pressKey(key)
end function

sub openMenu()
    item = focusedItem()
    if item = invalid then return
    extras = []
    if m.rows[m.focus].row.id = "continue" then extras.push({id: "dismiss", text: "Remove from Continue Watching", icon: "close"})
    m.top.menu = cardMenu(item, extras, [])
end sub

' The card menu's own rows are done by kit/CardMenu.brs; Home's own is Continue Watching's.
sub onMenuChosen(event as object)
    which = event.getData()
    if cardMenuChosen(which) then return
    if which = "dismiss" then api("PUT", "/v1/items/" + m.cardItem.id + "/state", {continueDismissed: true, madeAgoMs: 0}, "onMenuSaved")
end sub

sub cardMenuChanged(item as object)
    refresh()
end sub

' Home is read again after a change made from a menu.
sub onMenuSaved(event as object)
    if event.getData().ok then refresh()
end sub

' Read Home again and keep focus where it was: the same row, and the same place along it (the
' next card, or the last one, when the card in focus has gone).
sub refresh()
    if m.focus < 0 then return
    m.keep = {row: m.rows[m.focus].row.id, after: [], order: []}
    for each item in m.rows[m.focus].row.items
        m.keep.order.push(item.id)
    end for
    ' every row's card, and the rows after the one in focus: if that row has gone (its last
    ' card went), focus moves to the row below it at the card it remembers (tv-focus frame 49)
    m.memory = {}
    for i = 0 to m.rows.count() - 1
        entry = m.rows[i]
        if entry.shelf <> invalid then m.memory[entry.row.id] = entry.shelf.focusedIndex
        if i > m.focus then m.keep.after.push(entry.row.id)
    end for
    m.details = {}     ' progress and played may have changed
    api("GET", homePath(), invalid, "onHome", {path: homePath()})
end sub

function pressKey(key as string) as boolean
    if m.focus < 0 then return false
    if key = "down"
        target = rowFrom(m.focus + 1, 1)
        if target >= 0 then moveTo(target)
        return true
    else if key = "up"
        target = rowFrom(m.focus - 1, -1)
        ' Up from the first row goes on to the scene (the now-playing card)
        if target < 0 then return false
        moveTo(target)
        return true
    else if key = "back"
        ' Back from anywhere on Home is the opening state; from there it leaves the app.
        first = rowFrom(0, 1)
        if m.focus <> first
            moveTo(first)
            return true
        end if
    end if
    return false
end function
