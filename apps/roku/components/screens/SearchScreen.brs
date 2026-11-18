sub init()
    c = colours()
    f = frames()
    s = f.search
    m.keyboard = m.top.findNode("keyboard")
    m.results = m.top.findNode("results")
    m.page = m.top.findNode("page")
    m.query = m.top.findNode("query")
    m.none = m.top.findNode("none")
    m.note = m.top.findNode("note")
    m.failed = m.top.findNode("failed")
    m.rest = m.top.findNode("rest")
    field = m.top.findNode("box")
    field.blendColor = c.raised
    field.width = s.boxWidth
    field.height = s.boxHeight
    field.translation = [f.gutter, f.browse.titleY]
    glass = m.top.findNode("glass")
    glass.blendColor = c.textTertiary
    glass.translation = [f.gutter + 26, f.browse.titleY + (s.boxHeight - 28) / 2]
    setType(m.query, "heading", c.text)
    m.query.width = s.boxWidth - 100
    m.query.height = s.boxHeight
    m.query.translation = [f.gutter + 72, f.browse.titleY]
    m.keyboard.translation = [f.gutter, s.keysY]
    ' with nothing found: a page title on the results column's title line, then one sentence
    setType(m.none, "title", c.text)
    m.none.width = f.rightEdge - s.resultsX
    m.none.translation = [s.resultsX, f.browse.titleY]
    setType(m.note, "lede", c.textSecondary)
    m.note.width = f.state.width
    m.note.translation = [s.resultsX, f.browse.titleY + 76]
    m.failed.translation = [s.resultsX, s.keysY]
    m.failed.width = f.rightEdge - s.resultsX
    m.results.clippingRect = [s.resultsX - 40, 0, 1920 - s.resultsX + 40, 1080]
    m.page.translation = [0, s.keysY]     ' rows start level with the keys (the recent searches come before any search)
    m.keyboard.observeField("text", "onTyped")
    m.rest.observeField("fire", "askSearch")
    m.failed.observeField("pressed", "onRetry")
    m.rows = []          ' {kind, items, node, y, fixed}: the kinds' chips, a shelf, the recent searches, the genres to browse, or the row saying the search failed
    m.focus = -1         ' -1 the keyboard, otherwise a row of results
    m.asked = ""
    m.libraryNames = {}
    api("GET", "/v1/libraries", invalid, "onLibraries")
    showPrompt()
end sub

' The libraries' names, for telling apart the same title found in two of them.
sub onLibraries(event as object)
    result = event.getData()
    if not result.ok or result.data.items = invalid then return
    for each library in result.data.items
        m.libraryNames[library.id] = library.name
    end for
end sub

sub showPrompt()
    m.none.text = ""
    m.note.text = "Search every library you can see: films, shows, episodes, people, music and audiobooks."
    api("GET", "/v1/search/history", invalid, "onHistory")
end sub

' An empty box offers the profile's recent searches: OK types one again; Clear forgets them all.
sub onHistory(event as object)
    result = event.getData()
    if m.asked <> "" or not result.ok or result.data.items = invalid or result.data.items.count() = 0 then return
    m.recent = []
    buttons = []
    for each entry in result.data.items
        if buttons.count() < 6
            buttons.push({id: "q:" + buttons.count().toStr(), text: entry.query, icon: "search"})
            m.recent.push(entry.query)
        end if
    end for
    buttons.push({id: "clear", text: "Clear", icon: ""})
    row = noteRow("Recent searches")
    bar = row.createChild("ButtonRow")
    bar.translation = [frames().search.resultsX, shelfMetrics("gridPoster").headingGap]
    bar.buttons = buttons
    bar.observeField("pressed", "onRecent")
    m.rows.push({kind: "recent", items: [], node: bar, y: 0, fixed: true})
end sub

' A heading under the note's words for the recent searches or the genres, fixed at underNoteY.
function noteRow(heading as string) as object
    s = frames().search
    row = m.page.createChild("Group")
    row.translation = [0, s.underNoteY - s.keysY]
    label = row.createChild("Label")
    setType(label, "rowHeading", colours().text)
    label.text = heading
    label.translation = [s.resultsX, 0]
    return row
end function

sub onRecent(event as object)
    which = event.getData()
    if which = "clear"
        api("DELETE", "/v1/search/history", invalid, "onForgotten")
        return
    end if
    moveTo(-1)
    m.keyboard.text = m.recent[which.mid(2).toInt()]
end sub

sub onForgotten(event as object)
    if not event.getData().ok or m.asked <> "" then return
    moveTo(-1)
    showGroups([], invalid)
end sub

' A search the viewer acted on (opened a result or a kind's list) goes to the top of the recent ones.
sub recordSearch()
    if m.asked <> "" then api("GET", "/v1/search?q=" + m.asked.encodeUriComponent() + "&record=true&limit=1", invalid, "onRecorded")
end sub

sub onRecorded()
end sub

sub onActive()
    if not m.top.active then return
    ' back from a page: focus where the page left it, or the keyboard if that row has gone, and
    ' m.focus says which, so a typed letter (or any key) goes where the ring is
    if m.focus >= 0 and m.focus < m.rows.count() then moveTo(m.focus) else moveTo(-1)
end sub

sub onTyped()
    m.query.text = m.keyboard.text
    m.rest.control = "stop"
    m.rest.control = "start"       ' results follow once typing rests
end sub

sub askSearch()
    text = m.keyboard.text.trim()
    if text = m.asked then return
    m.asked = text
    if text = ""
        showGroups([], invalid)
        showPrompt()
        return
    end if
    api("GET", "/v1/search?q=" + text.encodeUriComponent(), invalid, "onResults", {text: text})
end sub

sub onRetry()
    m.asked = ""
    askSearch()
end sub

sub onResults(event as object)
    result = event.getData()
    if event.getRoSGNode().ctx.text <> m.asked then return
    m.none.text = ""
    m.note.text = ""
    if not result.ok
        ' the results are the one part of the page that failed: the keyboard still works (rule 9)
        showGroups([], invalid)
        m.failed.title = "Couldn't search"
        m.failed.reason = whyFailed(result)
        m.failed.action = "Try again"
        m.failed.visible = true
        m.rows.push({kind: "failed", items: [], node: m.failed, y: 0})
        return
    end if
    showGroups(result.data.groups, result.data.topResult)
    if m.rows.count() = 0
        m.none.text = "No matches for “" + m.asked + "”"
        m.note.text = "Check the spelling or try a shorter search."
        api("GET", "/v1/genres?counts=true", invalid, "onGenres", {text: m.asked})
    end if
end sub

' No matches: the genres to browse instead (states frame 18), each opening its titles as a grid.
sub onGenres(event as object)
    result = event.getData()
    if event.getRoSGNode().ctx.text <> m.asked or m.rows.count() > 0 or not result.ok or result.data.genres = invalid or result.data.genres.count() = 0 then return
    row = noteRow("")
    shelf = row.createChild("Shelf")
    shelf.slotX = frames().search.resultsX
    shelf.shape = "landscape"
    shelf.heading = "Browse instead"
    size = shelfMetrics("landscape").card
    cards = createObject("roSGNode", "ContentNode")
    for each genre in result.data.genres
        art = ""
        if genre.cover <> invalid then art = pick(genre.cover, ["backdrop"])
        sub_ = ""
        if genre.count <> invalid then sub_ = countText(genre.count, "title", "titles")
        cards.createChild("ContentNode").addFields({id: genre.value, kind: "genre", art: imageUrl(art, size[0], size[1]), whole: false, title: genre.label, sub: sub_, progress: -1, badge: "", overlay: true})
    end for
    shelf.cards = cards
    shelf.observeField("selectedIndex", "onSelected")
    m.rows.push({kind: "genre", items: result.data.genres, node: shelf, y: 0, fixed: true})
end sub

function groupName(kind as string) as string
    names = {movie: "Movies", show: "Shows", episode: "Episodes", person: "People", artist: "Artists", album: "Albums", track: "Songs", audiobook: "Audiobooks", collection: "Collections", channel: "Live TV"}
    if names[kind] <> invalid then return names[kind]
    return ""
end function

' One shelf for each kind with something found. Typing never moves focus: it stays on the keyboard.
sub showGroups(groups as object, top as dynamic)
    m.page.removeChildrenIndex(m.page.getChildCount(), 0)
    m.failed.visible = false
    inRows = (m.focus >= 0)     ' focus was in a row, which these new rows replace
    m.rows = []
    s = frames().search
    y = 0
    if showTop(top) then y = s.topHeight
    for each group in groups
        name = groupName(group.kind)
        items = []
        if group.kind = "person" and group.people <> invalid
            for each person in group.people
                face = pick(person.images, ["portrait", "photo", "profile", "poster"])
                if face = "" then face = invalid
                items.push({id: person.id, kind: "person", name: person.name, image: face})
            end for
        else if group.kind = "channel" and group.channels <> invalid
            items = group.channels
        else if group.items <> invalid
            items = withoutReading(group.items)
        end if
        if name <> "" and items.count() > 0
            shape = "gridPoster"
            if group.kind = "channel" then shape = "landscape"
            if group.kind = "episode" then shape = "episode"
            if group.kind = "person" then shape = "person"
            square = (group.kind = "track" or group.kind = "album" or group.kind = "audiobook" or group.kind = "artist")
            if square then shape = "gridSquare"
            shelf = m.page.createChild("Shelf")
            shelf.slotX = s.resultsX
            shelf.shape = shape
            shelf.heading = name
            shelf.subheading = foundText(group)
            cards = createObject("roSGNode", "ContentNode")
            size = shelfMetrics(shape).card
            copies = []
            if group.kind <> "person" and group.kind <> "channel" then copies = copyNames(items, m.libraryNames)
            for n = 0 to items.count() - 1
                item = items[n]
                card = invalid
                if group.kind = "person"
                    card = personCard({personId: item.id, name: item.name, role: "", image: item.image})
                else if group.kind = "channel"
                    card = channelCard(item)
                else if group.kind = "episode"
                    card = episodeCard(item, false)
                    if item.rootTitle <> invalid then card.sub = item.rootTitle + "   " + episodeCode(item)
                else
                    card = gridCard(item)
                    if square then card.artHeight = 200
                end if
                card.art = imageUrl(card.art, size[0], pictureHeight(shape))
                if n < copies.count() and copies[n] <> ""
                    ' an episode keeps its show and number and adds the library; any other title says only the library
                    if group.kind = "episode" then card.sub = card.sub + "   " + copies[n] else card.sub = copies[n]
                end if
                cards.createChild("ContentNode").addFields(card)
            end for
            shelf.cards = cards
            shelf.translation = [0, y]
            shelf.observeField("selectedIndex", "onSelected")
            m.rows.push({kind: group.kind, items: items, node: shelf, y: y, total: foundText(group), shape: shape})
            y = y + shelfMetrics(shape).block
        end if
    end for
    ' On TV each kind's chip is the See all for its row: its whole list as a grid (people have no grid)
    chips = []
    for each row in m.rows
        ' people and channels have no grid of their own, and a Top result is not a kind
        if row.kind <> "person" and row.kind <> "channel" and row.kind <> "top" then chips.push({id: row.kind, text: groupName(row.kind), detail: row.total, icon: ""})
    end for
    if chips.count() > 0
        bar = m.page.createChild("ButtonRow")
        bar.translation = [s.resultsX, frames().browse.titleY - s.keysY]
        bar.buttons = chips
        bar.observeField("pressed", "onChip")
        m.rows.unshift({kind: "chips", items: [], node: bar, y: 0, fixed: true})
    end if
    m.focus = -1
    m.page.translation = [0, s.keysY]
    clipResults()
    ' new results replace the rows: focus in a row that has gone goes to the keyboard as m.focus
    ' says, never left on nothing while the ring is drawn somewhere else
    if inRows and m.top.isInFocusChain() then moveTo(-1)
end sub

' How tall a card's picture is in each shelf shape (its words are under it).
' A channel found by name: its logo whole on the card, its name and number; OK plays it.
function channelCard(channel as object) as object
    sub_ = channel.number
    if channel.matchedProgramme <> invalid and channel.matchedProgramme.title <> invalid then sub_ = sub_ + "   " + channel.matchedProgramme.title
    art = ""
    if channel.logo <> invalid then art = channel.logo     ' made a picture address with the row's others
    return {id: channel.id, kind: "channel", art: art, whole: true, title: channel.name, sub: sub_, progress: -1, badge: "", overlay: true}
end function

' The Top result (tv.html frame 5): the one title the server says is clearly what was typed, its
' 16:9 picture as a one-card row with its words beside it; true when there is one to show. Only
' the kinds this app opens a page for are shown, so OK on it always goes somewhere.
function showTop(top as dynamic) as boolean
    if top = invalid then return false
    item = top.item
    if top.kind = "person" and top.person <> invalid then item = {id: top.person.id, kind: "person", title: top.person.name, images: top.person.images}
    if item = invalid or not opensPage(top.kind) then return false
    c = colours()
    s = frames().search
    g = shelfMetrics("landscape")
    shelf = m.page.createChild("Shelf")
    shelf.slotX = s.resultsX
    shelf.shape = "landscape"
    shelf.translation = [0, -g.headingGap]      ' the picture's top on the row's line, with no heading
    card = cardFor(item, "landscape")
    card.overlay = false
    if card.art = "" then card.art = pick(item.images, ["portrait", "photo", "poster", "cover"])
    card.art = imageUrl(card.art, g.card[0], g.card[1])
    cards = createObject("roSGNode", "ContentNode")
    cards.createChild("ContentNode").addFields(card)
    shelf.cards = cards
    shelf.observeField("selectedIndex", "onSelected")
    words = m.page.createChild("Group")
    words.translation = [s.resultsX + g.card[0] + 40, 0]
    width = frames().rightEdge - words.translation[0]
    label = words.createChild("Label")
    setType(label, "eyebrow", c.accentStrong)
    label.text = "Top result"
    label.translation = [0, 30]
    title = words.createChild("Label")
    setType(title, "title", c.text)
    title.text = item.title
    title.width = width
    title.wrap = true
    title.maxLines = 2
    title.translation = [0, 66]
    y = 66 + title.boundingRect().height + 10
    meta = words.createChild("Label")
    setType(meta, "metaStrong", c.text)
    year = ""
    if item.year <> invalid then year = item.year.toStr() + "   "
    meta.text = year + topKindName(top.kind)
    meta.translation = [0, y]
    left = timeLeftText(item)
    if progressOf(item) >= 0 and left <> ""
        ' part-way through: how far, and how long is left (tv.html frame 5)
        track = words.createChild("Rectangle")
        track.color = c.track
        track.width = 160
        track.height = 6
        track.translation = [0, y + 54]
        bar = words.createChild("Rectangle")
        bar.color = c.accent
        bar.width = 160 * progressOf(item)
        bar.height = 6
        bar.translation = [0, y + 54]
        note = words.createChild("Label")
        setType(note, "metaStrong", c.text)
        note.text = left
        note.translation = [176, y + 40]
    end if
    m.rows.push({kind: "top", items: [item], node: shelf, y: 0})
    return true
end function

' The kinds a Top result can open here: each has its page in this app.
function opensPage(kind as string) as boolean
    return kind = "movie" or kind = "show" or kind = "person"
end function

function pictureHeight(shape as string) as integer
    if shape = "gridPoster" then return 300
    if shape = "gridSquare" then return 200
    if shape = "person" then return 150
    return shelfMetrics(shape).card[0] * 9 / 16       ' an episode's still
end function

' Rows below the one in focus run on down the screen; the first that does not fit is cut through
' its pictures, never through a line of its words (and left out if not even its heading fits).
sub clipResults()
    s = frames().search
    bottom = 1080
    top = m.page.translation[1]
    for each row in m.rows
        if row.shape <> invalid and row.fixed <> true
            y = top + row.y
            g = shelfMetrics(row.shape)
            if y + g.headingGap + g.card[1] > 1080
                bottom = y + g.headingGap + pictureHeight(row.shape)
                if bottom > 1080 then bottom = 1080
                if y + g.headingGap > 1080 then bottom = y
                exit for
            end if
        end if
    end for
    m.results.clippingRect = [s.resultsX - 40, 0, 1920 - s.resultsX + 40, bottom]
end sub

sub onChip(event as object)
    kind = event.getData()
    recordSearch()
    m.top.open = {item: {kind: "list", title: groupName(kind), path: "/v1/search?q=" + m.asked.encodeUriComponent() + "&kind=" + kind, view: kind}, play: false}
end sub

sub moveTo(i as integer)
    m.focus = i
    if i < 0
        m.keyboard.active = true
        return
    end if
    y = m.rows[i].y
    if m.rows[i].fixed = true then y = 0     ' the chips and the rows under the note's words never scroll
    m.page.translation = [0, frames().search.keysY - y]
    clipResults()
    m.rows[i].node.active = true
end sub

sub onSelected(event as object)
    if m.focus < 0 then return
    row = m.rows[m.focus]
    at = event.getData()
    if at < 0 or at >= row.items.count() then return
    item = row.items[at]
    if row.kind = "genre"
        m.top.open = {item: {kind: "list", title: item.label, path: "/v1/items?tag=" + item.value + "&sort=title", view: ""}, play: false}
        return
    end if
    recordSearch()
    if row.kind = "channel"
        m.top.open = {item: {kind: "channel", id: item.id, title: item.name, channel: item}, play: true}
        return
    end if
    if row.kind = "person" then item = {kind: "person", id: item.id, title: item.name}
    m.top.open = {item: item, play: (row.kind = "episode")}
end sub

function pressKey(key as string) as boolean
    if m.focus < 0 then return false
    if key = "down" and m.focus < m.rows.count() - 1
        moveTo(m.focus + 1)
        return true
    else if key = "up" and m.focus > 0
        moveTo(m.focus - 1)
        return true
    end if
    return false
end function

function onKeyEvent(key as string, press as boolean) as boolean
    holdKey(key, press and m.focus >= 0 and (key = "up" or key = "down"))
    if not press then return false
    if m.focus < 0
        ' the keyboard gave the key up: Right from its last column reaches the results
        if key = "right" and m.rows.count() > 0
            ' the first row of results, not the chips above it
            first = 0
            if m.rows[0].kind = "chips" and m.rows.count() > 1 then first = 1
            moveTo(first)
            return true
        end if
        return key = "right"      ' nothing found yet: Right stops
    end if
    if key = "options"
        row = m.rows[m.focus]
        at = row.node.focusedIndex
        if row.kind <> "person" and at >= 0 and at < row.items.count() then m.top.menu = cardMenu(row.items[at], [], [])
        return true
    end if
    if key = "left" or key = "back"
        moveTo(-1)                ' Left from a row's first card (or Back): the keyboard, at the key it left
        return true
    end if
    if left(key, 4) = "Lit_" or key = "backspace"
        ' a letter from the phone app while focus is in the results: back to the keyboard, typed in the box
        moveTo(-1)
        m.keyboard.typed = key
        return true
    end if
    if key = "up" or key = "down" then return pressKey(key)     ' Up at the top goes on to the scene
    return false
end function

sub onMenuChosen(event as object)
    cardMenuChosen(event.getData())
end sub

' Results draw no watched or saved marks, so a change from the menu leaves them as they are.
sub cardMenuChanged(item as object)
end sub
