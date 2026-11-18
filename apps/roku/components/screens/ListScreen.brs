sub init()
    c = colours()
    f = frames()
    l = f.list
    m.l = l
    cover = m.top.findNode("cover")
    cover.translation = [f.gutter, l.coverY]
    plate = m.top.findNode("coverPlate")
    plate.blendColor = c.raised
    plate.width = l.cover
    plate.height = l.cover
    m.tiles = m.top.findNode("tiles")
    for i = 0 to 3
        tile = m.tiles.getChild(i)
        tile.width = l.cover / 2
        tile.height = l.cover / 2
        tile.translation = [(i mod 2) * l.cover / 2, int(i / 2) * l.cover / 2]
    end for
    m.eyebrow = m.top.findNode("eyebrow")
    setType(m.eyebrow, "eyebrow", c.accentStrong)
    m.eyebrow.translation = [l.wordsX, l.eyebrowY]
    m.name = m.top.findNode("name")
    setType(m.name, "pageTitle", c.text)
    m.name.width = f.rightEdge - l.wordsX
    m.name.translation = [l.wordsX, l.nameY]
    m.strong = m.top.findNode("factsStrong")
    setType(m.strong, "metaStrong", c.text)
    m.facts = m.top.findNode("facts")
    setType(m.facts, "meta", c.textSecondary)
    m.actions = m.top.findNode("actions")
    m.actions.translation = [l.wordsX - 8, l.actionsY]
    m.actions.observeField("pressed", "onAction")
    m.window = m.top.findNode("window")
    m.window.clippingRect = [0, l.rowsY - 10, 1920, l.hintY - 40 - l.rowsY]
    m.rows = m.top.findNode("rows")
    m.plate = m.top.findNode("rowPlate")
    m.plate.blendColor = c.raisedBright
    m.ring = m.top.findNode("rowRing")
    hint = m.top.findNode("hint")
    hint.translation = [f.gutter, l.hintY]
    m.state = m.top.findNode("state")
    m.state.observeField("pressed", "load")
    m.items = []
    m.focus = -1          ' -1 the buttons, otherwise a row
    m.list = invalid
    m.top.observeField("focusedChild", "paint")
end sub

sub onItem()
    m.eyebrow.text = m.top.item.eyebrow
    m.name.text = m.top.item.title
    load()
end sub

' The list itself (its revision, maker and who sees it) and its titles in its order.
sub load()
    m.state.visible = false
    api("GET", "/v1/lists/" + m.top.item.id, invalid, "onList")
    api("GET", "/v1/items?list=" + m.top.item.id + "&limit=200&total=true", invalid, "onItems")
end sub

sub onList(event as object)
    result = event.getData()
    if not result.ok
        failed(result)
        return
    end if
    m.list = result.data
    m.name.text = m.list.name
    drawWords()
end sub

sub onItems(event as object)
    result = event.getData()
    if not result.ok
        failed(result)
        return
    end if
    m.items = result.data.items
    drawRows()
    drawWords()
    if m.top.isInFocusChain() and m.focus < 0 and not m.actions.isInFocusChain() then m.actions.active = true
end sub

sub failed(result as object)
    m.state.icon = "warning"
    m.state.title = "Couldn't open " + m.top.item.title
    m.state.body = whyFailed(result)
    m.state.buttons = [{id: "retry", text: "Try again now", icon: "refresh"}]
    m.state.translation = [0, 0]
    m.state.visible = true
    if m.top.isInFocusChain() then m.state.active = true
end sub

function mayChange() as boolean
    return m.list <> invalid and (m.list.role = "owner" or m.list.role = "editor")
end function

function noun() as string
    if m.top.item.listKind = "collection" then return "collection"
    return "playlist"
end function

' The mosaic, "12 items   24 h 10 min   Made by you", and the buttons once there is something to play.
sub drawWords()
    l = m.l
    covers = []
    for each item in m.items
        picture = pick(item.images, ["poster", "cover", "thumb"])
        if picture <> "" and covers.count() < 4 then covers.push(imageUrl(picture, l.cover / 2, l.cover / 2))
    end for
    for i = 0 to 3
        tile = m.tiles.getChild(i)
        tile.visible = (covers.count() = 4)
        if tile.visible then tile.uri = covers[i]
    end for
    if covers.count() > 0 and covers.count() < 4
        ' fewer than four: the first cover fills the square
        first = m.tiles.getChild(0)
        first.visible = true
        first.uri = covers[0]
        first.width = l.cover
        first.height = l.cover
    else
        m.tiles.getChild(0).width = l.cover / 2
        m.tiles.getChild(0).height = l.cover / 2
    end if
    total = 0
    for each item in m.items
        if item.durationMs <> invalid then total = total + item.durationMs
    end for
    m.strong.text = countText(m.items.count(), "item", "items")
    facts = []
    if total > 0 then facts.push(durationText(total))
    if m.list <> invalid
        if m.list.role = "owner"
            facts.push("Made by you")
        else if noun() = "collection"
            ' a collection someone else shared (collections frame 20)
            m.eyebrow.text = "Shared collection"
            facts.push("Shared by the owner")
        else if m.list.owner <> invalid
            facts.push("Made by " + m.list.owner.name)
        end if
        if noun() = "collection" and m.list.role = "owner"
            if m.list.everyone = true then facts.push("Visible to everyone") else facts.push("Only you")
        end if
    end if
    m.facts.text = facts.join("   ")
    m.strong.translation = [l.wordsX, l.factsY]
    m.facts.translation = [l.wordsX + m.strong.boundingRect().width + 26, l.factsY]
    buttons = []
    if m.items.count() > 0 then buttons = [{id: "all", text: "Play all", icon: "play"}, {id: "shuffle", text: "Shuffle", icon: "shuffle"}]
    m.actions.buttons = buttons
    m.actions.visible = (buttons.count() > 0)
    hint = m.top.findNode("hint")
    if mayChange() and m.items.count() > 0 and noun() = "collection"
        hint.hints = [{keys: ["*"], text: "on a row: Go to item and Remove"}]
    else if mayChange() and m.items.count() > 0
        hint.hints = [{keys: ["*"], text: "on a row: Go to item, Move up, Move down and Remove"}]
    else if m.items.count() > 0
        hint.hints = [{keys: ["*"], text: "on a row: Go to item"}]
    else
        hint.hints = []
    end if
end sub

' Each title: its cover, its name and what it is, the library it lives in, then time left, Watched
' or its length.
sub drawRows()
    c = colours()
    f = frames()
    l = m.l
    for i = m.rows.getChildCount() - 1 to 2 step -1
        m.rows.removeChildIndex(i)
    end for
    names = {}
    if m.global.hasField("libraryNames") then names = m.global.libraryNames     ' the libraries' names (MainScene, with the card menu)
    y = l.rowsY
    for each item in m.items
        row = m.rows.createChild("Group")
        row.translation = [0, y]
        thumb = row.createChild("Poster")
        thumb.uri = imageUrl(pick(item.images, ["poster", "cover", "thumb"]), l.thumb, int(l.thumb * 1.5))
        thumb.loadDisplayMode = "scaleToZoom"
        thumb.width = l.thumb
        thumb.height = int(l.thumb * 1.5)
        thumb.translation = [f.gutter + 32, (l.row - thumb.height) / 2]
        title = row.createChild("Label")
        title.text = item.title
        title.width = l.libraryX - l.titleX - 40
        title.translation = [l.titleX, l.row / 2 - 38]
        setType(title, "listLabel", c.text)
        what = row.createChild("Label")
        what.text = rowWhat(item)
        what.width = title.width
        what.translation = [l.titleX, l.row / 2 + 2]
        setType(what, "meta", c.textSecondary)
        library = row.createChild("Label")
        if item.libraryId <> invalid and names[item.libraryId] <> invalid then library.text = names[item.libraryId]
        library.translation = [l.libraryX, 0]
        library.height = l.row
        library.vertAlign = "center"
        setType(library, "meta", c.textTertiary)
        state = row.createChild("Label")
        state.text = rowState(item)
        state.width = 400
        state.horizAlign = "right"
        state.translation = [f.rightEdge - 28 - 400, 0]
        state.height = l.row
        state.vertAlign = "center"
        if progressOf(item) >= 0 then setType(state, "metaStrong", c.accentStrong) else setType(state, "meta", c.textSecondary)
        line = row.createChild("Rectangle")
        line.color = c.separator
        line.width = f.rightEdge - f.gutter - 20
        line.height = 1
        line.translation = [f.gutter + 20, l.row - 1]
        y = y + l.row
    end for
    paint()
end sub

function rowWhat(item as object) as string
    kinds = {movie: "Movie", show: "Show", episode: "Episode", album: "Album", track: "Song", audiobook: "Audiobook", collection: "Collection"}
    what = kinds[item.kind]
    if what = invalid then what = ""
    if item.kind = "episode" and item.rootTitle <> invalid then what = item.rootTitle + "   " + episodeCode(item)
    if item.year <> invalid and item.kind <> "episode" then what = (what + "   " + item.year.toStr()).trim()
    return what
end function

function rowState(item as object) as string
    if progressOf(item) >= 0 then return timeLeftText(item)
    if item.state <> invalid and item.state.watched = true then return "Watched"
    return durationText(item.durationMs)
end function

sub onActive()
    if not m.top.active then return
    if m.state.visible
        m.state.active = true
    else if m.focus >= 0
        m.rows.setFocus(true)
    else if m.actions.visible
        m.actions.active = true
    else
        m.top.setFocus(true)
    end if
    paint()
end sub

' The row in focus has the bright plate and the ring; the rows move only to keep it in the window.
sub paint()
    f = frames()
    l = m.l
    on = (m.focus >= 0 and m.top.isInFocusChain())
    m.plate.visible = on
    m.ring.visible = on
    if not on
        m.rows.translation = [0, 0]
        return
    end if
    y = l.rowsY + m.focus * l.row
    out = metrics().focusSpread + 6
    m.plate.translation = [f.gutter + 12, y + 4]
    m.plate.width = f.rightEdge - f.gutter
    m.plate.height = l.row - 8
    m.ring.translation = [f.gutter + 12 - out, y + 4 - out]
    m.ring.width = m.plate.width + out * 2
    m.ring.height = m.plate.height + out * 2
    room = l.hintY - 40 - l.rowsY
    scroll = 0
    if (m.focus + 1) * l.row > room then scroll = (m.focus + 1) * l.row - room
    m.rows.translation = [0, -scroll]
end sub

function pressKey(key as string) as boolean
    if key = "down"
        if m.focus < m.items.count() - 1
            m.focus = m.focus + 1
            m.rows.setFocus(true)
            paint()
            return true
        end if
        return false
    end if
    if m.focus > 0
        m.focus = m.focus - 1
        paint()
        return true
    end if
    if m.focus = 0 and m.actions.visible
        m.focus = -1
        m.actions.active = true
        paint()
        return true
    end if
    return false      ' Up from the buttons goes on to the scene
end function

function onKeyEvent(key as string, press as boolean) as boolean
    holdKey(key, press and m.focus >= 0 and (key = "up" or key = "down"))
    if not press or m.state.visible then return false
    if key = "up" or key = "down" then return pressKey(key)
    if m.focus < 0 then return false
    if key = "OK"
        playFrom(m.focus, false)
        return true
    end if
    if key = "options"
        rowMenu(m.focus)
        return true
    end if
    if key = "left" then return true
    return false
end function

' The whole list as one queue from a row, in its order, or shuffled.
sub playFrom(at as integer, shuffle as boolean)
    ' the server builds the whole list (not only the 200 titles read here), in its own order
    first = m.items[at]
    queue = {source: {list: m.top.item.id}, startAt: first.id}
    if shuffle
        first = m.items[rnd(m.items.count()) - 1]
        queue = {source: {list: m.top.item.id}, shuffle: true, startAt: first.id}
    end if
    m.top.open = {item: first, play: true, queue: queue}
end sub

sub onAction(event as object)
    which = event.getData()
    if which = "all" then playFrom(0, false)
    if which = "shuffle" then playFrom(0, true)
end sub

' A row's menu (collections frames 19 and 20): Go to item; for a playlist the viewer may change, Move
' up, Move down and Remove; for a collection the viewer made, Remove (a collection is reordered on a
' phone or computer). Moving leaves the menu open, so OK can be pressed again.
sub rowMenu(at as integer)
    item = m.items[at]
    rows = [{id: "row:go", text: "Go to item", icon: "info"}]
    if mayChange() and noun() = "collection"
        rows.push({id: "row:remove", text: "Remove from collection", icon: "close", gapBefore: true})
    else if mayChange()
        if at > 0 then rows.push({id: "row:up", text: "Move up", icon: "chevronUp"})
        if at < m.items.count() - 1 then rows.push({id: "row:down", text: "Move down", icon: "chevronDown"})
        rows.push({id: "row:remove", text: "Remove from " + noun(), icon: "close", gapBefore: true})
    end if
    m.rowAt = at
    m.top.menu = {eyebrow: m.list.name, title: item.title, status: rowWhat(item), rows: rows, hints: [{keys: ["OK"], text: "Choose"}, {keys: ["Back"], text: "Close"}]}
end sub

sub onMenuChosen()
    which = m.top.menuChosen
    if left(which, 4) = "row:"
        item = m.items[m.rowAt]
        what = mid(which, 5)
        if what = "go"
            m.top.open = {item: item, play: false}
        else if what = "remove"
            api("DELETE", "/v1/lists/" + m.list.id + "/entries/" + item.entryId, invalid, "onChanged", {at: m.rowAt, reopen: false}, false, "", "", m.list.revision)
        else
            ' Move up: before the row above; Move down: after the row below
            body = {before: m.items[m.rowAt - 1].entryId}
            moved = m.rowAt - 1
            if what = "down"
                body = {after: m.items[m.rowAt + 1].entryId}
                moved = m.rowAt + 1
            end if
            api("PATCH", "/v1/lists/" + m.list.id + "/entries/" + item.entryId, body, "onChanged", {at: moved, reopen: true}, false, "", "", m.list.revision)
        end if
    end if
end sub

' The server's list after a move or removal: its titles are read again, focus on the row that moved.
sub onChanged(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if result.ok and result.data <> invalid and result.data.revision <> invalid then m.list.revision = result.data.revision
    m.focus = ctx.at
    if m.focus >= m.items.count() - 1 and not ctx.reopen then m.focus = m.items.count() - 2
    m.reopen = ctx.reopen
    api("GET", "/v1/items?list=" + m.top.item.id + "&limit=200&total=true", invalid, "onReread")
end sub

sub onReread(event as object)
    result = event.getData()
    if not result.ok then return
    m.items = result.data.items
    if m.focus >= m.items.count() then m.focus = m.items.count() - 1
    drawRows()
    drawWords()
    m.rows.setFocus(true)
    if m.items.count() = 0
        m.focus = -1
        if m.actions.visible then m.actions.active = true
    end if
    paint()
    if m.reopen = true and m.focus >= 0 then rowMenu(m.focus)
end sub
