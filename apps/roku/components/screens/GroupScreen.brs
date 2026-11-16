sub init()
    c = colours()
    m.rows = m.top.findNode("rows")
    setType(m.top.findNode("eyebrow"), "eyebrow", c.accentStrong)
    setType(m.top.findNode("name"), "pageTitle", c.text)
    setType(m.top.findNode("facts"), "bodyStrong", c.textSecondary)
    setType(m.top.findNode("about"), "about", c.textSecondary)
    setType(m.top.findNode("initials"), "personInitials", c.textSecondary)
    m.shelves = []       ' {items, shelf, y}
    m.focus = 0
    m.rowsTop = 430
end sub

sub onItem()
    item = m.top.item
    isPerson = (item.kind = "person")
    x = 200
    if isPerson then x = 390
    m.top.findNode("face").visible = isPerson
    m.top.findNode("initials").visible = isPerson
    if isPerson then m.top.findNode("initials").text = initialsOf(item.title)
    for each id in ["eyebrow", "name", "facts", "about"]
        m.top.findNode(id).translation = [x, 0]
    end for
    head("", item.title, "", "")
    m.top.quiet = true
    if isPerson
        m.top.art = ""
        api("GET", "/v1/people/" + item.id, invalid, "onPerson")
    else
        m.top.findNode("eyebrow").text = "Collection"
        api("GET", "/v1/items/" + item.id, invalid, "onCollection")
        api("GET", "/v1/items?collection=" + item.id + "&limit=100", invalid, "onTitles", {heading: "In collection order", role: ""})
    end if
end sub

' Lay the heading out: each line follows the one above, and a line with nothing in it takes no room.
sub head(eyebrow as string, name as string, facts as string, about as string)
    x = m.top.findNode("name").translation[0]
    y = 64
    lines = [["eyebrow", eyebrow, 38], ["name", name, 86], ["facts", facts, 44], ["about", oneParagraph(about), 0]]
    if m.top.item.kind <> "person" and eyebrow = "" then lines[0][1] = m.top.findNode("eyebrow").text
    for each line in lines
        label = m.top.findNode(line[0])
        label.text = line[1]
        label.translation = [x, y]
        if line[1] <> "" then y = y + line[2]
    end for
    bottom = y + m.top.findNode("about").boundingRect().height
    if bottom < 260 then bottom = 260
    m.rowsTop = bottom + 44
    place()
end sub

sub onCollection(event as object)
    result = event.getData()
    if not result.ok then return
    full = result.data
    m.top.art = pageArt(full)
    facts = ""
    if full.counts <> invalid and full.counts.children > 0 then facts = countText(full.counts.children, "title", "titles")
    about = ""
    if full.summary <> invalid then about = full.summary
    head("Collection", full.title, facts, about)
end sub

sub onPerson(event as object)
    result = event.getData()
    if not result.ok then return
    person = result.data
    photo = pick(person.images, ["portrait", "photo", "profile", "poster"])
    if photo <> "" then m.top.art = "poster|" + photo
    facts = ""
    if person.birthDate <> invalid then facts = "Born " + dateText(person.birthDate)
    if person.birthplace <> invalid
        if facts <> "" then facts = facts + "      "
        facts = facts + person.birthplace
    end if
    if person.titleCount <> invalid
        if facts <> "" then facts = facts + "      "
        facts = facts + countText(person.titleCount, "title", "titles") + " on this server"
    end if
    about = ""
    if person.biography <> invalid then about = person.biography
    names = {actor: "Acting", director: "Directing", writer: "Writing", producer: "Production", composer: "Music", creator: "Created"}
    works = ""
    if person.roles <> invalid
        for each role in person.roles
            name = names[role.role]
            if name = invalid then name = ucase(left(role.role, 1)) + mid(role.role, 2)
            if works <> "" then works = works + ", "
            works = works + name
            api("GET", "/v1/items?person=" + person.id + "&role=" + role.role + "&limit=60", invalid, "onTitles", {heading: name, role: role.role})
        end for
    end if
    head(works, person.name, facts, about)
end sub

sub onTitles(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if not result.ok then return
    items = withoutReading(result.data.items)
    if items.count() = 0 then return
    shelf = m.rows.createChild("Shelf")
    shelf.shape = "gridPoster"
    shelf.heading = ctx.heading
    shelf.subheading = countText(items.count(), "title", "titles")
    cards = createObject("roSGNode", "ContentNode")
    for each item in items
        card = gridCard(item)
        if item.state <> invalid then card.played = (item.state.watched = true)
        card.art = imageUrl(card.art, 200, 300)
        cards.createChild("ContentNode").addFields(card)
    end for
    shelf.cards = cards
    shelf.observeField("selectedIndex", "onSelected")
    m.shelves.push({items: items, shelf: shelf})
    place()
    if m.shelves.count() = 1 and m.top.active then shelf.active = true
end sub

sub place()
    block = shelfMetrics("gridPoster").block + 16
    for i = 0 to m.shelves.count() - 1
        m.shelves[i].shelf.translation = [0, i * block]
    end for
    y = m.rowsTop
    if m.focus > 0 then y = 200 - m.focus * block
    m.rows.translation = [0, y]
    m.top.findNode("head").visible = (m.focus = 0)
end sub

sub onActive()
    if not m.top.active then return
    if m.shelves.count() > 0 then m.shelves[m.focus].shelf.active = true else m.top.setFocus(true)
end sub

sub onSelected(event as object)
    row = m.shelves[m.focus]
    at = event.getData()
    if at >= 0 and at < row.items.count() then m.top.open = {item: row.items[at], play: false}
end sub

function pressKey(key as string) as boolean
    if key = "down" and m.focus < m.shelves.count() - 1
        m.focus = m.focus + 1
    else if key = "up" and m.focus > 0
        m.focus = m.focus - 1
    else
        return false
    end if
    place()
    m.shelves[m.focus].shelf.active = true
    return true
end function

function onKeyEvent(key as string, press as boolean) as boolean
    holdKey(key, press and (key = "up" or key = "down"))
    if not press then return false
    if key = "up" or key = "down"
        pressKey(key)
        return true
    end if
    return false
end function
