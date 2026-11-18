sub init()
    c = colours()
    f = frames()
    g = f.group
    m.rows = m.top.findNode("rows")
    m.head = m.top.findNode("head")
    m.state = m.top.findNode("state")
    m.smallTitle = m.top.findNode("smallTitle")
    m.name = m.top.findNode("name")
    m.about = m.top.findNode("about")
    m.face = m.top.findNode("face")
    m.face.translation = [f.gutter, f.group.eyebrowY]
    m.face.size = g.face
    setType(m.top.findNode("eyebrow"), "eyebrow", c.accentStrong)
    setType(m.top.findNode("factsStrong"), "metaStrong", c.text)
    setType(m.top.findNode("facts"), "meta", c.textSecondary)
    setType(m.about, "synopsis", c.textSecondary)
    m.about.width = f.title.copyWidth
    setType(m.smallTitle, "title", c.text)
    m.smallTitle.translation = [f.gutter, f.browse.titleY]
    ' how tall the name is on one line: a longer name takes lines from the description below it
    setType(m.name, "display", c.text)
    m.name.text = "Ag"
    m.oneLine = m.name.boundingRect().height
    m.state.observeField("pressed", "onItem")
    m.generation = 0
    m.block = shelfMetrics("gridPoster").block
end sub

sub onItem()
    item = m.top.item
    m.isPerson = (item.kind = "person")
    m.generation = m.generation + 1     ' answers to an earlier visit of this page are dropped
    m.rows.removeChildrenIndex(m.rows.getChildCount(), 0)
    m.slots = []         ' one for each row asked for, in the order they are shown: {heading, path, state, items, node, focus, total}
    m.shown = []         ' the slots on the page, top to bottom
    m.focus = 0          ' which of them has focus
    m.detail = invalid   ' a collection's own facts, once read
    m.detailFailed = false
    m.state.visible = false
    m.rows.visible = true
    m.head.visible = true
    m.smallTitle.text = item.title
    m.top.findNode("face").visible = m.isPerson
    ' a person's words start beside the face
    x = frames().gutter
    if m.isPerson then x = x + frames().group.face + 60
    m.top.findNode("words").translation = [x, 0]
    m.name.width = frames().rightEdge - x
    if m.isPerson
        m.face.photo = ""
        m.face.name = item.title
        m.top.art = ""
        placeWords("", item.title, "", "", "")
        api("GET", "/v1/people/" + item.id, invalid, "onPerson", {generation: m.generation})
    else
        placeWords("Collection", item.title, "", "", "")
        api("GET", "/v1/items/" + item.id, invalid, "onCollection", {generation: m.generation})
        askRow("In collection order", "/v1/items?collection=" + item.id + "&sort=order")
    end if
    placeRows()
end sub

' The words in their slots from frames().group.eyebrowY: eyebrow, name, the facts line (its first part
' in bold), then the description cut by line count. A name too long for one line steps down a size
' and wraps, never cut, and takes its extra height from the description's lines.
sub placeWords(eyebrow as string, name as string, strong as string, facts as string, about as string)
    f = frames()
    g = f.group
    m.top.findNode("eyebrow").text = eyebrow
    m.top.findNode("eyebrow").translation = [0, g.eyebrowY]
    c = colours()
    m.name.wrap = false
    setType(m.name, "display", c.text)
    m.name.text = name
    width = m.name.width
    m.name.width = 0
    if m.name.boundingRect().width > width
        setType(m.name, "lockupLong", c.text)
        m.name.lineSpacing = -14
        if m.name.boundingRect().width > width * 1.9 then setType(m.name, "title", c.text)
        m.name.wrap = true
        m.name.maxLines = 3
    end if
    m.name.width = width
    m.name.translation = [-4, g.nameY - 14]
    extra = m.name.boundingRect().height - m.oneLine
    if extra < 0 then extra = 0
    line = typeRoles().synopsis[2]
    strongLabel = m.top.findNode("factsStrong")
    factsLabel = m.top.findNode("facts")
    strongLabel.text = strong
    factsLabel.text = facts
    strongLabel.translation = [0, g.factsY + extra]
    gap = 0
    if strong <> "" and facts <> "" then gap = 26
    factsLabel.translation = [strongLabel.boundingRect().width + gap, g.factsY + extra]
    ' a person's biography has three lines; a collection's description one, above its action row
    lines = 3
    if not m.isPerson then lines = 1
    lines = lines - int((extra + line - 1) / line)
    m.about.visible = (lines > 0 and about <> "")
    if lines < 1 then lines = 1
    m.about.maxLines = lines
    m.about.text = about
    m.about.translation = [0, g.aboutY + extra]
end sub

sub onCollection(event as object)
    result = event.getData()
    if event.getRoSGNode().ctx.generation <> m.generation then return
    if not result.ok
        m.detailFailed = true
        if m.slots[0].state = "error" then showPageState(result)
        return
    end if
    m.detail = result.data
    collectionWords()
end sub

' A collection's words come from its own facts and from its titles, whichever arrives first: the
' count (the list's total when the server's count is empty), the years its titles span, its
' description; its art is its own backdrop, else its first title's.
sub collectionWords()
    full = m.top.item
    if m.detail <> invalid then full = m.detail
    slot = m.slots[0]
    count = 0
    if full.counts <> invalid and full.counts.children <> invalid then count = full.counts.children
    if count = 0 and slot.state = "ok" then count = slot.total
    strong = ""
    span = ""
    if slot.state = "ok"
        films = true
        first = 0
        last = 0
        for each item in slot.items
            if item.kind <> "movie" then films = false
            if item.year <> invalid
                if first = 0 or item.year < first then first = item.year
                if item.year > last then last = item.year
            end if
        end for
        if count > 0 and films then strong = countText(count, "film", "films")
        if first > 0 and first = last then span = first.toStr()
        if first > 0 and first < last then span = first.toStr() + " to " + last.toStr()
    end if
    if strong = "" and count > 0 then strong = countText(count, "title", "titles")
    about = ""
    if full.summary <> invalid then about = oneParagraph(full.summary)
    placeWords("Collection", full.title, strong, span, about)
    if pick(full.images, ["backdrop"]) <> ""
        m.top.art = pageArt(full)
    else if slot.state = "ok"
        m.top.art = pageArt(slot.items[0])
    end if
end sub

sub onPerson(event as object)
    result = event.getData()
    if event.getRoSGNode().ctx.generation <> m.generation then return
    if not result.ok
        showPageState(result)
        return
    end if
    person = result.data
    photo = pick(person.images, ["portrait", "photo", "profile", "poster"])
    m.face.name = person.name
    if photo <> ""
        m.top.art = "poster|" + photo
        m.face.photo = imageUrl(photo, frames().group.face, frames().group.face)
    end if
    born = ""
    if person.birthDate <> invalid then born = "Born " + dateText(person.birthDate)
    facts = ""
    if person.birthplace <> invalid then facts = person.birthplace
    if person.titleCount <> invalid
        if facts <> "" then facts = facts + "      "
        facts = facts + countText(person.titleCount, "title", "titles") + " on this server"
    end if
    about = ""
    if person.biography <> invalid then about = oneParagraph(person.biography)
    names = {actor: "Acting", director: "Directing", writer: "Writing", producer: "Production", composer: "Music", creator: "Created"}
    works = ""
    if person.roles <> invalid
        for each role in person.roles
            name = names[role.role]
            if name = invalid then name = ucase(left(role.role, 1)) + mid(role.role, 2)
            if works <> "" then works = works + ", "
            works = works + name
            askRow(name, "/v1/items?person=" + person.id + "&role=" + role.role + "&sort=release&order=desc")
        end for
    end if
    placeWords(works, person.name, born, facts, about)
end sub

' Nothing on the page could load: the whole-page state in its place (rule 9).
sub showPageState(result as object)
    m.head.visible = false
    m.rows.visible = false
    m.state.icon = "warning"
    m.state.title = "Couldn't open " + m.top.item.title
    m.state.body = whyFailed(result)
    m.state.buttons = [{id: "retry", text: "Try again now", icon: "refresh"}]
    m.state.visible = true
    if m.top.isInFocusChain() then m.state.active = true
end sub

' A row is asked for at once and keeps its place in the order, whenever its answer comes.
sub askRow(heading as string, path as string)
    m.slots.push({heading: heading, path: path + "&limit=200&total=true", state: "wait", items: [], node: invalid, focus: invalid, total: 0})
    requestRow(m.slots.count() - 1)
end sub

sub requestRow(at as integer)
    api("GET", m.slots[at].path, invalid, "onTitles", {generation: m.generation, slot: at})
end sub

sub onTitles(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if ctx.generation <> m.generation then return
    slot = m.slots[ctx.slot]
    had = (slot.node <> invalid and m.focus < m.shown.count() and m.shown[m.focus].node.isSameNode(slot.node))
    if slot.node <> invalid then m.rows.removeChild(slot.node)
    slot.node = invalid
    if not result.ok
        if not m.isPerson and m.detailFailed
            showPageState(result)
            return
        end if
        slot.state = "error"
        failedRow(slot, result)
    else
        slot.items = withoutReading(result.data.items)
        slot.state = "empty"
        if slot.items.count() > 0
            slot.state = "ok"
            shelfOf(slot)
            ' the count is the server's: a row shows at most one page of 200
            slot.total = slot.items.count()
            if result.data.total <> invalid then slot.total = result.data.total
            slot.node.subheading = countText(slot.total, "title", "titles")
        end if
    end if
    if not m.isPerson then collectionWords()
    placeRows()
    if had and m.focus < m.shown.count() then m.shown[m.focus].focus.active = true
    if had and m.focus >= m.shown.count() then m.top.setFocus(true)
end sub

sub shelfOf(slot as object)
    shelf = m.rows.createChild("Shelf")
    shelf.shape = "gridPoster"
    shelf.heading = slot.heading
    cards = createObject("roSGNode", "ContentNode")
    for each item in slot.items
        card = gridCard(item)
        if item.state <> invalid then card.played = (item.state.watched = true)
        card.art = imageUrl(card.art, 200, 300)
        cards.createChild("ContentNode").addFields(card)
    end for
    shelf.cards = cards
    shelf.observeField("selectedIndex", "onSelected")
    slot.node = shelf
    slot.focus = shelf
end sub

' A row that could not load: its heading stays, and one row says so with Try again (rule 9).
sub failedRow(slot as object, result as object)
    shelf = m.rows.createChild("Shelf")
    shelf.heading = slot.heading
    row = shelf.createChild("StateRow")
    row.translation = [frames().gutter, shelfMetrics("gridPoster").headingGap]
    row.title = "Couldn't load these titles"
    row.reason = whyFailed(result)
    row.action = "Try again"
    row.observeField("pressed", "onRetry")
    slot.node = shelf
    slot.focus = row
end sub

sub onRetry(event as object)
    row = event.getRoSGNode()
    for i = 0 to m.slots.count() - 1
        if m.slots[i].focus <> invalid and m.slots[i].focus.isSameNode(row) then requestRow(i)
    end for
end sub

' The rows on show are the answered ones up to the first still on its way, so a row never lands
' above one already shown; a row with nothing in it is left out whole. The row in focus sits at
' the first row's line, and the words above give way to the page's name once focus goes past it.
sub placeRows()
    m.shown = []
    waiting = false
    for each slot in m.slots
        if slot.state = "wait" then waiting = true
        if slot.node <> invalid
            slot.node.visible = not waiting
            if not waiting
                slot.node.translation = [0, m.shown.count() * m.block]
                m.shown.push(slot)
            end if
        end if
    end for
    if m.focus >= m.shown.count() then m.focus = 0
    ' rows above the one in focus have scrolled away under the page's name
    for i = 0 to m.focus - 1
        m.shown[i].node.visible = false
    end for
    g = frames().group
    rowsY = g.rowsY
    if not m.isPerson then rowsY = g.actionRowsY     ' a collection's row sits under its action row's place
    if m.focus = 0 then m.rows.translation = [0, rowsY] else m.rows.translation = [0, g.scrolledY - m.focus * m.block]
    m.head.visible = (m.focus = 0 and not m.state.visible)
    m.smallTitle.visible = (m.focus > 0)
    m.top.quiet = (m.focus > 0)     ' the art dims once rows lie over it
    ' focus lands on the first row once there is one, if the page itself still holds it
    if m.shown.count() > 0 and m.top.hasFocus() then m.shown[0].focus.active = true
end sub

sub onActive()
    if not m.top.active then return
    if m.state.visible
        m.state.active = true
    else if m.focus < m.shown.count()
        m.shown[m.focus].focus.active = true
    else
        m.top.setFocus(true)
    end if
end sub

sub onSelected(event as object)
    if m.focus >= m.shown.count() then return
    row = m.shown[m.focus]
    at = event.getData()
    if at >= 0 and at < row.items.count() then m.top.open = {item: row.items[at], play: false}
end sub

function pressKey(key as string) as boolean
    if key = "down" and m.focus < m.shown.count() - 1
        m.focus = m.focus + 1
    else if key = "up" and m.focus > 0
        m.focus = m.focus - 1
    else
        return false
    end if
    placeRows()
    m.shown[m.focus].focus.active = true
    return true
end function

function onKeyEvent(key as string, press as boolean) as boolean
    holdKey(key, press and (key = "up" or key = "down"))
    if not press or m.state.visible then return false
    if key = "up" or key = "down" then return pressKey(key)     ' Up at the top goes on to the scene
    return false
end function
