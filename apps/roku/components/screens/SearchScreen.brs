sub init()
    c = colours()
    m.keyboard = m.top.findNode("keyboard")
    m.results = m.top.findNode("results")
    m.page = m.top.findNode("page")
    m.query = m.top.findNode("query")
    m.note = m.top.findNode("note")
    m.rest = m.top.findNode("rest")
    setType(m.query, "heading", c.text)
    setType(m.note, "lede", c.textSecondary)
    m.results.clippingRect = [740, 0, 1180, 1080]
    m.keyboard.observeField("text", "onTyped")
    m.rest.observeField("fire", "search")
    m.rows = []          ' {kind, items, shelf, y}
    m.focus = -1         ' -1 the keyboard, otherwise a row of results
    m.asked = ""
    m.note.text = "Search every library you can see: films, shows, episodes, people, music and audiobooks."
end sub

sub onActive()
    if not m.top.active then return
    if m.focus >= 0 and m.focus < m.rows.count() then m.rows[m.focus].shelf.active = true else m.keyboard.active = true
end sub

sub onTyped()
    m.query.text = m.keyboard.text
    m.rest.control = "stop"
    m.rest.control = "start"       ' results follow once typing rests
end sub

sub search()
    text = m.keyboard.text.trim()
    if text = m.asked then return
    m.asked = text
    if text = ""
        show([])
        m.note.text = "Search every library you can see: films, shows, episodes, people, music and audiobooks."
        return
    end if
    api("GET", "/v1/search?q=" + text.encodeUriComponent(), invalid, "onResults", {text: text})
end sub

sub onResults(event as object)
    result = event.getData()
    if event.getRoSGNode().ctx.text <> m.asked then return
    if not result.ok
        show([])
        m.note.text = "Can't reach the server. Try again in a moment."
        return
    end if
    show(result.data.groups)
    if m.rows.count() = 0 then m.note.text = "Nothing found for “" + m.asked + "”. Check the spelling, or try fewer letters." else m.note.text = ""
end sub

function groupName(kind as string) as string
    names = {movie: "Movies", show: "Shows", episode: "Episodes", person: "People", artist: "Artists", album: "Albums", track: "Songs", audiobook: "Audiobooks", collection: "Collections"}
    if names[kind] <> invalid then return names[kind]
    return ""
end function

' One shelf for each kind with something found. Typing never moves focus: it stays on the keyboard.
sub show(groups as object)
    m.page.removeChildrenIndex(m.page.getChildCount(), 0)
    m.rows = []
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
            shelf = m.page.createChild("Shelf")
            shelf.slotX = 780
            shelf.shape = shape
            shelf.heading = name
            if group.total <> invalid then shelf.subheading = group.total.toStr()
            cards = createObject("roSGNode", "ContentNode")
            size = shelfMetrics(shape).card
            for each item in items
                card = invalid
                if group.kind = "person"
                    card = personCard({personId: item.id, name: item.name, role: "", image: item.image})
                else if group.kind = "episode"
                    card = episodeCard(item, false)
                    if item.rootTitle <> invalid then card.sub = item.rootTitle + "   " + episodeCode(item)
                else
                    card = gridCard(item)
                    if group.kind = "track" or group.kind = "album" or group.kind = "audiobook" then card.artHeight = 200
                end if
                card.art = imageUrl(card.art, size[0], 300)
                cards.createChild("ContentNode").addFields(card)
            end for
            shelf.cards = cards
            shelf.translation = [0, y]
            shelf.observeField("selectedIndex", "onSelected")
            m.rows.push({kind: group.kind, items: items, shelf: shelf, y: y})
            y = y + shelfMetrics(shape).block + 24
        end if
    end for
    m.focus = -1
    m.page.translation = [0, 176]
end sub

sub moveTo(i as integer)
    m.focus = i
    if i < 0
        m.keyboard.active = true
        return
    end if
    m.page.translation = [0, 176 - m.rows[i].y]
    m.rows[i].shelf.active = true
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
    if key = "up" or key = "down"
        pressKey(key)
        return true
    end if
    return false
end function
