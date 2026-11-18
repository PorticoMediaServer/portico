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
    m.keyboard.observeField("text", "onTyped")
    m.rest.observeField("fire", "askSearch")
    m.failed.observeField("pressed", "onRetry")
    m.rows = []          ' {kind, items, node, y}: a shelf, or the row saying the search failed
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
end sub

sub onActive()
    if not m.top.active then return
    if m.focus >= 0 and m.focus < m.rows.count() then m.rows[m.focus].node.active = true else m.keyboard.active = true
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
        showGroups([])
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
        showGroups([])
        m.failed.title = "Couldn't search"
        m.failed.reason = whyFailed(result)
        m.failed.action = "Try again"
        m.failed.visible = true
        m.rows.push({kind: "failed", items: [], node: m.failed, y: 0})
        return
    end if
    showGroups(result.data.groups)
    if m.rows.count() = 0
        m.none.text = "No matches for “" + m.asked + "”"
        m.note.text = "Check the spelling or try a shorter search."
    end if
end sub

function groupName(kind as string) as string
    names = {movie: "Movies", show: "Shows", episode: "Episodes", person: "People", artist: "Artists", album: "Albums", track: "Songs", audiobook: "Audiobooks", collection: "Collections"}
    if names[kind] <> invalid then return names[kind]
    return ""
end function

' One shelf for each kind with something found. Typing never moves focus: it stays on the keyboard.
sub showGroups(groups as object)
    m.page.removeChildrenIndex(m.page.getChildCount(), 0)
    m.failed.visible = false
    m.rows = []
    s = frames().search
    y = 0
    for each group in groups
        name = groupName(group.kind)
        items = []
        if group.kind = "person" and group.people <> invalid
            for each person in group.people
                face = pick(person.images, ["portrait", "photo", "profile", "poster"])
                if face = "" then face = invalid
                items.push({id: person.id, kind: "person", name: person.name, image: face})
            end for
        else if group.items <> invalid
            items = withoutReading(group.items)
        end if
        if name <> "" and items.count() > 0
            shape = "gridPoster"
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
            if group.kind <> "person" then copies = copyNames(items, m.libraryNames)
            for n = 0 to items.count() - 1
                item = items[n]
                card = invalid
                if group.kind = "person"
                    card = personCard({personId: item.id, name: item.name, role: "", image: item.image})
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
            m.rows.push({kind: group.kind, items: items, node: shelf, y: y, shape: shape})
            y = y + shelfMetrics(shape).block
        end if
    end for
    m.focus = -1
    m.page.translation = [0, s.keysY]
    clipResults()
end sub

' How tall a card's picture is in each shelf shape (its words are under it).
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
        if row.shape <> invalid
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

sub moveTo(i as integer)
    m.focus = i
    if i < 0
        m.keyboard.active = true
        return
    end if
    m.page.translation = [0, frames().search.keysY - m.rows[i].y]
    clipResults()
    m.rows[i].node.active = true
end sub

sub onSelected(event as object)
    if m.focus < 0 then return
    row = m.rows[m.focus]
    at = event.getData()
    if at < 0 or at >= row.items.count() then return
    item = row.items[at]
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
            moveTo(0)
            return true
        end if
        return key = "right"      ' nothing found yet: Right stops
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
    if key = "up" or key = "down"
        pressKey(key)
        return true
    end if
    return false
end function
