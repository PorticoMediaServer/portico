' The tabs, in today's order, and what each reads (the routes the other apps use):
' My List and Favorites are titles (GET /v1/items?inList / ?favorite): the films, shows and
' collections this app has pages for; Playlists, Collections and Saved views are the profile's
' lists (GET /v1/lists?kind=); History is GET /v1/history, newest first, its films and episodes.
function savedTabs() as object
    return [
        {id: "list", text: "My List", query: "inList=true", empty: "My List is empty", body: "Add a title to My List from its page and it waits for you here."},
        {id: "favorites", text: "Favorites", query: "favorite=true", empty: "No favorites yet", body: "Mark anything as a favorite and it is kept here."},
        {id: "playlist", text: "Playlists", kind: "playlist", one: "playlist", many: "playlists", eyebrow: "Playlist", heading: "In playlist order", empty: "No playlists yet", body: "Playlists made on your phone or computer appear here."},
        {id: "collection", text: "Collections", kind: "collection", one: "collection", many: "collections", eyebrow: "Collection", heading: "In collection order", empty: "No collections yet", body: "Personal collections group titles across a library the way you like."},
        {id: "saved_view", text: "Saved views", kind: "saved_view", one: "saved view", many: "saved views", eyebrow: "Saved view", heading: "What it finds now", empty: "No saved views yet", body: "Saved views made from a library's filters on your phone or computer appear here."},
        {id: "history", text: "History", empty: "Nothing watched yet", body: "What you watch and listen to on this profile appears here."}
    ]
end function

sub init()
    c = colours()
    f = frames()
    m.tabs = savedTabs()
    m.tools = m.top.findNode("tools")
    m.box = m.top.findNode("box")
    m.content = m.top.findNode("content")
    m.gridState = m.top.findNode("gridState")
    m.page = m.top.findNode("page")
    m.confirm = m.top.findNode("confirm")
    heading = m.top.findNode("heading")
    setType(heading, "title", c.text)
    heading.translation = [f.gutter, f.browse.titleY]
    m.top.findNode("tabMark").color = c.accent
    m.tools.translation = [f.gutter, f.grid.toolsY]
    count = m.top.findNode("count")
    setType(count, "meta", c.textSecondary)
    ' what the tab shows starts a heading's height above the library's first card line, so a shape's
    ' heading or History's first day sits there; the window cuts anything scrolled above it
    m.contentTop = f.grid.cardsY - 46
    m.box.clippingRect = [-60, m.contentTop - 6, 1920, 1080 - m.contentTop + 6]
    m.gridState.translation = [f.gutter, f.grid.cardsY]
    m.gridState.width = f.state.width
    m.gridState.observeField("pressed", "load")
    m.page.observeField("pressed", "onPagePressed")
    m.confirm.observeField("chosen", "onClearConfirmed")
    m.tab = 0
    m.tool = 0
    m.region = "content"  ' "tabs", "tools", "content" (a grid or History's rows), "page" or "state"
    m.sort = "added"
    m.filter = ""
    m.generation = 0
    m.keepAt = 0
    drawTabs()
    load()
    m.top.observeField("focusedChild", "paint")
end sub

' --- the tabs ---------------------------------------------------------------------------------

sub drawTabs()
    c = colours()
    tabs = m.top.findNode("tabs")
    x = frames().gutter + m.top.findNode("heading").boundingRect().width + 56
    m.tabX = []
    for each spec in m.tabs
        label = tabs.createChild("Label")
        label.text = spec.text
        label.translation = [x, frames().browse.titleY + 12]
        setType(label, "tab", c.textTertiary)
        m.tabX.push([x, label.boundingRect().width])
        x = x + label.boundingRect().width + 40
    end for
end sub

function tabSpec() as object
    return m.tabs[m.tab]
end function

' --- reading a tab ------------------------------------------------------------------------------

' Each tab reads from one or two sources; the next page of the first unfinished one is asked for as
' focus nears the end of what is shown. A source is {path, square, items, cursor, done, total} and,
' once drawn, its grid (or History's rows).
sub load()
    m.generation = m.generation + 1
    spec = tabSpec()
    m.page.visible = false
    m.gridState.visible = false
    m.content.removeChildrenIndex(m.content.getChildCount(), 0)
    m.content.translation = [0, 0]
    m.scroll = 0
    m.shown = false
    m.rows = []          ' History's rows: {node, entry}
    m.row = 0
    m.at = 0             ' which source's grid has focus
    m.asking = 0
    m.top.findNode("count").text = ""
    m.sources = []
    if spec.query <> invalid
        base = "/v1/items?" + spec.query + "&sort=" + m.sort + "&order=" + sortOrder(m.sort)
        if m.filter <> "" then base = base + "&" + filterParam(m.filter)
        m.sources.push({path: base + "&kind=movie,show,collection", square: false, name: "Movies and shows"})
    else if spec.kind <> invalid
        m.sources.push({path: "/v1/lists?kind=" + spec.kind, square: false, name: ""})
    else
        m.sources.push({path: "/v1/history?", square: false, name: ""})
    end if
    for each source in m.sources
        source.items = []
        source.cursor = invalid
        source.done = false
        source.total = 0
        source.grid = invalid
    end for
    drawTools()
    ' the two shapes are read together at first, so a tab knows at once whether it has both
    for i = 0 to m.sources.count() - 1
        ask(i)
    end for
end sub

function sortOrder(sort as string) as string
    if sort = "title" then return "asc"
    return "desc"
end function

function filterParam(id as string) as string
    params = {unplayed: "watched=false", inProgress: "inProgress=true", watched: "watched=true", favorites: "favorite=true"}
    return params[id]
end function

sub ask(at as integer)
    source = m.sources[at]
    m.asking = m.asking + 1
    path = source.path + "&limit=100&total=true"
    if source.cursor <> invalid then path = path + "&cursor=" + source.cursor
    api("GET", path, invalid, "onPage", {generation: m.generation, at: at})
end sub

sub onPage(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if ctx.generation <> m.generation then return
    m.asking = m.asking - 1
    source = m.sources[ctx.at]
    if not result.ok
        if not m.shown then tabSays("error", "Couldn't load " + lcase(tabSpec().text), whyFailed(result), "Try again")
        return
    end if
    fresh = result.data.items
    if tabSpec().query <> invalid then fresh = withoutReading(fresh)
    if tabSpec().id = "history" then fresh = playedVideo(fresh)
    source.cursor = result.data.nextCursor
    source.done = (source.cursor = invalid or source.cursor = "")
    if result.data.total <> invalid then source.total = result.data.total else source.total = source.items.count() + fresh.count()
    source.items.append(fresh)
    if m.asking > 0 and not m.shown then return      ' the tab is drawn once its first answers are in
    if tabSpec().id = "history" then showHistory() else showGrids()
end sub

' History's films and episodes: what OK can play again here.
function playedVideo(entries as object) as object
    kept = []
    for each entry in entries
        if entry.item <> invalid and (entry.item.kind = "movie" or entry.item.kind = "episode") then kept.push(entry)
    end for
    return kept
end function

' The next page is asked for when focus comes near the end of what is shown.
sub askMore(nearEnd as boolean)
    if m.asking > 0 or not nearEnd then return
    for i = 0 to m.sources.count() - 1
        if not m.sources[i].done
            ask(i)
            return
        end if
    end for
end sub

' --- the grids: titles (posters, then squares under their own heading), or the lists' tiles -------

sub showGrids()
    c = colours()
    f = frames()
    k = metrics()
    spec = tabSpec()
    total = 0
    shapes = 0
    for each source in m.sources
        total = total + source.total
        if source.items.count() > 0 then shapes = shapes + 1
    end for
    count = m.top.findNode("count")
    if spec.kind <> invalid then count.text = countText(total, spec.one, spec.many) else count.text = countText(total, "title", "titles")
    if total = 0
        if m.filter <> "" then tabSays("empty", "Nothing here", "No titles match. Switch the filter off to see more.", "") else emptyTab()
        return
    end if
    ' each shape is one grid as tall as its rows; the window scrolls, not the grid. With two shapes
    ' each grid has its heading a shelf's heading gap above its cards, the first at the window's top
    gap = shelfMetrics("gridPoster").headingGap
    y = f.grid.cardsY
    if shapes > 1 then y = m.contentTop + gap
    for each source in m.sources
        if source.items.count() > 0
            if source.grid = invalid then makeGrid(source, shapes > 1)
            drawn = source.grid.content.getChildCount()
            for i = drawn to source.items.count() - 1
                source.grid.content.createChild("ContentNode").addFields(cardOf(source.items[i], source.square))
            end for
            if shapes > 1
                if y > m.contentTop + gap then y = y + gap
                source.heading.text = source.name + "   " + source.total.toStr()
                source.heading.translation = [f.gutter, y - gap]
            end if
            rows = int((source.items.count() + 6) / 7)
            source.grid.numRows = rows
            source.grid.translation = [f.gutter, y - k.cardPad]
            source.pitch = source.grid.itemSize[1] + source.grid.itemSpacing[1]
            y = y + rows * source.pitch + f.saved.rowGap
        end if
    end for
    if not m.shown
        m.shown = true
        ' after a change from the menu, focus stays at the same place (the next card, or the last)
        m.at = firstGrid()
        at = m.keepAt
        m.keepAt = 0
        items = m.sources[m.at].items.count()
        if at >= items then at = items - 1
        m.sources[m.at].grid.jumpToItem = at
        if m.region <> "tabs" and m.region <> "tools" then m.region = "content"
        focusRegion()
    end if
end sub

sub makeGrid(source as object, headed as boolean)
    c = colours()
    k = metrics()
    if headed
        source.heading = m.content.createChild("Label")
        setType(source.heading, "rowHeading", c.text)
    end if
    grid = m.content.createChild("MarkupGrid")
    grid.itemComponentName = "Card"
    grid.numColumns = 7
    grid.drawFocusFeedback = false
    grid.vertFocusAnimationStyle = "floatingFocus"
    ' a square's cell: art as wide as the poster's (a 2:3 poster is 1.5 times as tall) over the same words
    card = shelfMetrics("gridPoster").card
    if source.square then card = [card[0], card[1] - card[0] * 1.5 + card[0]]
    grid.itemSize = [card[0], card[1] + k.cardPad * 2]
    grid.itemSpacing = [k.cardGap, 10]
    grid.content = createObject("roSGNode", "ContentNode")
    grid.observeField("itemFocused", "onCardFocused")
    grid.observeField("itemSelected", "onCardSelected")
    source.grid = grid
end sub

function firstGrid() as integer
    for i = 0 to m.sources.count() - 1
        if m.sources[i].grid <> invalid then return i
    end for
    return 0
end function

function cardOf(entry as object, square as boolean) as object
    spec = tabSpec()
    if spec.kind <> invalid
        ' a list's tile: its first cover, its name and how many it holds
        art = ""
        if entry.covers <> invalid and entry.covers.count() > 0 then art = pick(entry.covers[0], ["poster", "cover", "thumb"])
        line = countText(entry.count, "item", "items")
        if entry.owner <> invalid and entry.role <> "owner" then line = line + "   Shared by " + entry.owner.name
        return {id: entry.id, kind: "savedList", art: imageUrl(art, 200, 300), whole: false, title: entry.name, sub: line, progress: -1, badge: "", overlay: false, under: true, oneLine: true, artHeight: 300}
    end if
    card = gridCard(entry)
    if square then card.artHeight = shelfMetrics("gridPoster").card[0]
    if entry.state <> invalid then card.played = (entry.state.watched = true)
    card.art = imageUrl(card.art, 200, card.artHeight)
    return card
end function

sub onCardFocused(event as object)
    grid = event.getRoSGNode()
    source = m.sources[m.at]
    if not grid.isSameNode(source.grid) then return
    at = event.getData()
    placeFocusedRow(source, int(at / 7))
    askMore(at >= source.items.count() - 21)
end sub

' The window moves only to keep the row in focus clear of the screen's foot and the toolbar.
sub placeFocusedRow(source as object, row as integer)
    f = frames()
    k = metrics()
    top = source.grid.translation[1] + k.cardPad + row * source.pitch
    bottom = top + source.pitch
    if bottom - m.scroll > 1080 - f.saved.bottom then m.scroll = bottom - (1080 - f.saved.bottom)
    if top - m.scroll < f.grid.cardsY then m.scroll = top - f.grid.cardsY
    if m.scroll < 0 then m.scroll = 0
    m.content.translation = [0, -m.scroll]
end sub

sub onCardSelected(event as object)
    entry = m.sources[m.at].items[event.getData()]
    spec = tabSpec()
    if spec.kind = invalid
        m.top.open = {item: entry, play: false}
        return
    end if
    ' a list opens as a page of its titles (GroupScreen); a saved view is read with its own query
    path = "/v1/items?list=" + entry.id
    if spec.kind = "saved_view" then path = "/v1/items?library=" + entry.library + "&" + entry.query
    about = ""
    if entry.description <> invalid then about = entry.description
    m.top.open = {item: {kind: "savedList", id: entry.id, title: entry.name, eyebrow: spec.eyebrow, heading: spec.heading, path: path, summary: about, counts: {children: entry.count}, images: {}}, play: false}
end sub

' Down from a shape's last row goes to the next shape in the same column; Up from its first row
' goes back to the one above, then to the toolbar.
function moveBetweenGrids(key as string) as boolean
    target = m.at
    if key = "down" then target = target + 1 else target = target - 1
    if target < 0 or target >= m.sources.count() then return false
    if m.sources[target].grid = invalid then return false
    column = m.sources[m.at].grid.itemFocused mod 7
    m.at = target
    items = m.sources[m.at].items.count()
    at = column
    if key = "up" then at = int((items - 1) / 7) * 7 + column
    if at >= items then at = items - 1
    m.sources[m.at].grid.jumpToItem = at
    focusRegion()
    placeFocusedRow(m.sources[m.at], int(at / 7))
    return true
end function

' --- History: rows grouped by day ----------------------------------------------------------------

sub showHistory()
    c = colours()
    f = frames()
    source = m.sources[0]
    if source.items.count() = 0
        emptyTab()
        return
    end if
    width = f.rightEdge - f.gutter
    if not m.shown
        m.y = m.contentTop
        m.lastDay = ""
    end if
    for i = m.rows.count() to source.items.count() - 1
        entry = source.items[i]
        day = dayName(entry.playedAt)
        if day <> m.lastDay
            if m.lastDay <> "" then m.y = m.y + f.saved.rowGap - f.saved.headingGap
            label = m.content.createChild("Label")
            label.text = day
            label.translation = [f.gutter, m.y]
            setType(label, "groupLabel", c.textSecondary)
            m.y = m.y + label.boundingRect().height + f.saved.headingGap
            m.lastDay = day
        end if
        row = m.content.createChild("ListRow")
        row.rowWidth = width
        row.model = historyRow(entry)
        row.translation = [f.gutter, m.y]
        m.rows.push({node: row, entry: entry})
        m.y = m.y + row.rowHeight
    end for
    if not m.shown
        m.shown = true
        if m.region <> "tabs" and m.region <> "tools" then m.region = "content"
        focusRegion()
    end if
end sub

' One played thing: its picture, its title and line, then what is left and the time of day.
function historyRow(entry as object) as object
    item = entry.item
    title = entry.title
    line = ""
    if entry.subtitle <> invalid then line = entry.subtitle
    art = ""
    remaining = "Played"
    if item <> invalid
        if item.kind = "episode"
            if item.rootTitle <> invalid then title = item.rootTitle
            line = (episodeCode(item) + "   " + item.title).trim()
        else if item.year <> invalid and line = ""
            line = item.year.toStr()
        end if
        art = imageUrl(pick(item.images, ["still", "thumb", "backdrop", "poster", "cover"]), 156, 88)
        if timeLeftText(item) <> "" then remaining = timeLeftText(item)
    end if
    return {label: title, help: line, scope: "", value: remaining + "      " + clockText(entry.playedAt), control: "", switchOn: false, icon: "", art: art, danger: false}
end function

' "Today", "Yesterday", a weekday within the week, then the date.
function dayName(iso as string) as string
    when = createObject("roDateTime")
    when.fromISO8601String(iso)
    when.toLocalTime()
    now = createObject("roDateTime")
    now.toLocalTime()
    days = int(now.asSeconds() / 86400) - int(when.asSeconds() / 86400)
    if days = 0 then return "Today"
    if days = 1 then return "Yesterday"
    if days < 7 then return when.getWeekday()
    return dateText(iso)
end function

' "3:12 PM", in the TV's own time zone.
function clockText(iso as string) as string
    when = createObject("roDateTime")
    when.fromISO8601String(iso)
    when.toLocalTime()
    h = when.getHours()
    half = "AM"
    if h >= 12 then half = "PM"
    h = h mod 12
    if h = 0 then h = 12
    mins = when.getMinutes().toStr()
    if len(mins) = 1 then mins = "0" + mins
    return h.toStr() + ":" + mins + " " + half
end function

sub placeHistory()
    f = frames()
    if m.rows.count() = 0 then return
    node = m.rows[m.row].node
    y = node.translation[1]
    bottom = y + node.rowHeight
    if bottom - m.scroll > 1080 - f.saved.bottom then m.scroll = bottom - (1080 - f.saved.bottom)
    if y - m.scroll < f.grid.cardsY then m.scroll = y - f.grid.cardsY
    if m.row = 0 or m.scroll < 0 then m.scroll = 0
    m.content.translation = [0, -m.scroll]
end sub

' --- empty, and failed --------------------------------------------------------------------------

' An empty tab says what goes here and offers one action, which has focus (tv-more frame 5).
sub emptyTab()
    spec = tabSpec()
    m.page.icon = "bookmark"
    m.page.title = spec.empty
    m.page.body = spec.body
    m.page.buttons = [{id: "home", text: "Browse Home", icon: ""}]
    m.page.visible = true
    m.shown = true
    ' nothing to sort or filter: no toolbar (frame 5), so Up from the state reaches the tabs
    m.tools.removeChildrenIndex(m.tools.getChildCount(), 0)
    m.toolSpecs = []
    if m.region <> "tabs" then m.region = "page"
    focusRegion()
end sub

sub onPagePressed()
    m.top.goHome = true
end sub

' The tab's own part could not load, or a filter leaves nothing: one row in its place (rule 9).
sub tabSays(kind as string, title as string, reason as string, action as string)
    m.gridState.kind = kind
    m.gridState.title = title
    m.gridState.reason = reason
    m.gridState.action = action
    m.gridState.visible = true
    m.shown = true
    if m.region <> "tabs" and m.region <> "tools"
        if action <> "" then m.region = "state" else m.region = "tools"
    end if
    focusRegion()
end sub

' --- the toolbar ---------------------------------------------------------------------------------

' My List and Favorites: Sort, then one quick filter at a time; History: Clear history. The list
' tabs have no toolbar (the server reads lists in one order, with no filters).
sub drawTools()
    c = colours()
    f = frames()
    m.tools.removeChildrenIndex(m.tools.getChildCount(), 0)
    spec = tabSpec()
    m.toolSpecs = []
    if spec.query <> invalid
        m.toolSpecs.push({id: "sort", text: sortName(m.sort)})
        m.toolSpecs.push({id: "unplayed", text: "Unplayed"})
        m.toolSpecs.push({id: "inProgress", text: "In progress"})
        m.toolSpecs.push({id: "watched", text: "Watched"})
        if spec.id = "list" then m.toolSpecs.push({id: "favorites", text: "Favorites"})
    else if spec.id = "history"
        m.toolSpecs.push({id: "clear", text: "Clear history…"})
    end if
    m.toolNodes = []
    x = 0
    for i = 0 to m.toolSpecs.count() - 1
        b = m.tools.createChild("Button")
        on = (m.filter = m.toolSpecs[i].id)
        if on then b.icon = "check"
        b.switchedOn = on
        b.text = m.toolSpecs[i].text
        b.translation = [x, 0]
        m.toolNodes.push(b)
        x = x + b.buttonWidth + 18
        if i = 0 and spec.query <> invalid
            rule = m.tools.createChild("Rectangle")
            rule.color = c.line
            rule.width = 2
            rule.height = 40
            rule.translation = [x, 16]
            x = x + 20
        end if
    end for
    if m.tool >= m.toolSpecs.count() then m.tool = 0
    ' the count is on the line under the toolbar, or on the toolbar's own line when there is none
    if m.toolSpecs.count() = 0 then m.top.findNode("count").translation = [f.gutter, f.grid.toolsY] else m.top.findNode("count").translation = [f.gutter, f.grid.countY]
end sub

sub pressTool()
    spec = m.toolSpecs[m.tool]
    if spec.id = "sort"
        rows = []
        for each id in ["added", "title", "year", "duration"]
            rows.push({id: id, text: sortName(id), current: (id = m.sort)})
        end for
        m.top.menu = {eyebrow: tabSpec().text, title: "Sort by", status: "", rows: rows}
    else if spec.id = "clear"
        m.confirm.model = {title: "Clear your viewing history?", body: "Everything this profile has watched or listened to is removed from History. Watched marks and resume points stay.", verb: "Clear history"}
    else
        ' one quick filter at a time; pressing the one that is on switches it off
        if m.filter = spec.id then m.filter = "" else m.filter = spec.id
        load()
    end if
end sub

sub onClearConfirmed()
    m.top.setFocus(true)
    if m.confirm.chosen = "confirm" then api("DELETE", "/v1/history", invalid, "onCleared")
    focusRegion()
end sub

sub onCleared(event as object)
    if event.getData().ok then load()
end sub

' --- focus ---------------------------------------------------------------------------------------

' The region keeps its name while its content is still on the way; the page holds focus meanwhile.
sub focusRegion()
    if m.region = "tools" and m.toolSpecs.count() = 0 then m.region = "tabs"
    if m.region = "content" and m.shown and tabSpec().id <> "history" and m.sources[m.at].grid <> invalid
        m.sources[m.at].grid.setFocus(true)
    else if m.region = "page" and m.page.visible
        m.page.active = true
    else if m.region = "state" and m.gridState.visible
        m.gridState.active = true
    else
        ' the tabs and toolbar (and a tab still on its way) hold focus as one group: focusing the page
        ' itself left a button below holding it, so Left and Right never reached the tabs
        m.tools.setFocus(true)
    end if
    paint()
end sub

sub onActive()
    if m.top.active then focusRegion()
end sub

sub onToTop()
    m.region = "content"
    load()
end sub

sub paint()
    c = colours()
    has = m.top.isInFocusChain()
    tabs = m.top.findNode("tabs")
    for i = 0 to tabs.getChildCount() - 1
        if i = m.tab then tabs.getChild(i).color = c.text else tabs.getChild(i).color = c.textTertiary
    end for
    y = frames().browse.titleY
    mark = m.top.findNode("tabMark")
    mark.translation = [m.tabX[m.tab][0], y + 52]
    mark.width = m.tabX[m.tab][1]
    ring = m.top.findNode("tabRing")
    ring.visible = (has and m.region = "tabs")
    out = metrics().focusSpread + 6
    ring.translation = [m.tabX[m.tab][0] - 18 - out, y + 2 - out]
    ring.width = m.tabX[m.tab][1] + 36 + out * 2
    ring.height = 56 + out * 2
    for i = 0 to m.toolNodes.count() - 1
        m.toolNodes[i].on = (has and m.region = "tools" and i = m.tool)
    end for
    inRows = (has and m.region = "content" and tabSpec().id = "history")
    for i = 0 to m.rows.count() - 1
        m.rows[i].node.on = (inRows and i = m.row)
        m.rows[i].node.separator = false
    end for
    if inRows then placeHistory()
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    history = (m.region = "content" and tabSpec().id = "history" and m.rows.count() > 0)
    holdKey(key, press and history and (key = "up" or key = "down"))
    if not press then return false
    if key = "back" then return backToTop()
    if history
        if key = "OK"
            entry = m.rows[m.row].entry
            if entry.item <> invalid then m.top.open = {item: entry.item, play: true}
            return true
        end if
        return pressKey(key)
    end if
    if m.region = "content" and (key = "up" or key = "down")
        if moveBetweenGrids(key) then return true
    end if
    if m.region = "content" or m.region = "page" or m.region = "state"
        if key = "up"
            m.region = "tools"
            focusRegion()
            return true
        end if
        return false          ' Left from the first column: the rail
    end if
    if m.region = "tools"
        if key = "right"
            if m.tool < m.toolSpecs.count() - 1 then m.tool = m.tool + 1
        else if key = "left"
            if m.tool = 0 then return false
            m.tool = m.tool - 1
        else if key = "up"
            m.region = "tabs"
        else if key = "down"
            m.region = contentRegion()
        else if key = "OK"
            pressTool()
            return true
        else
            return false
        end if
        focusRegion()
        return true
    end if
    ' the tabs: moving along them changes the page as focus lands
    if key = "right" and m.tab < m.tabs.count() - 1
        m.tab = m.tab + 1
        m.filter = ""
        load()
    else if key = "left"
        if m.tab = 0 then return false
        m.tab = m.tab - 1
        m.filter = ""
        load()
    else if key = "down"
        m.region = "tools"
        if m.toolSpecs.count() = 0 then m.region = contentRegion()
    else if key <> "up" and key <> "right"
        return false
    end if
    focusRegion()
    return true
end function

' Up and Down through History's rows; the next page is asked for eight rows before the end.
function pressKey(key as string) as boolean
    if key = "down" and m.row < m.rows.count() - 1
        m.row = m.row + 1
        askMore(m.row >= m.rows.count() - 8)
    else if key = "up" and m.row > 0
        m.row = m.row - 1
    else if key = "up"
        m.region = "tools"
        focusRegion()
        return true
    else
        return false
    end if
    paint()
    return true
end function

' Back goes to the top of the page (the first tab, its first title), and from there to Home.
function backToTop() as boolean
    atTop = (m.tab = 0 and (m.region = "page" or (m.region = "content" and m.shown and m.sources[m.at].grid <> invalid and m.at = firstGrid() and m.sources[m.at].grid.itemFocused = 0)))
    if atTop
        m.top.goHome = true
    else if m.tab <> 0
        m.tab = 0
        m.filter = ""
        m.region = "content"
        load()
        focusRegion()
    else
        m.region = "content"
        load()
    end if
    return true
end function

' Where Down from the toolbar goes: what the tab shows.
function contentRegion() as string
    if m.page.visible then return "page"
    if m.gridState.visible and m.gridState.action <> "" then return "state"
    if m.gridState.visible then return m.region
    return "content"
end function

' The Sort menu's choice.
sub onMenuChosen(event as object)
    m.sort = event.getData()
    load()
end sub
