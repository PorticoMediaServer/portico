sub init()
    c = colours()
    f = frames()
    g = f.group
    m.rows = m.top.findNode("rows")
    m.head = m.top.findNode("head")
    m.state = m.top.findNode("state")
    m.actions = m.top.findNode("actions")     ' a collection's Play all and Shuffle
    m.actions.translation = [f.gutter, f.collectionActionsY]
    m.actions.observeField("pressed", "onPlay")
    m.onActions = false  ' true while they hold focus
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
    ' a person's Filters, and the biography read in full
    m.filters = m.top.findNode("filters")
    m.filters.buttons = [{id: "filters", text: "Filters", icon: "sliders"}]
    m.filters.observeField("pressed", "onFilters")
    m.reader = m.top.findNode("reader")
    m.reader.observeField("closed", "onReaderClosed")
end sub

sub onItem()
    item = m.top.item
    m.isPerson = (item.kind = "person")
    m.generation = m.generation + 1     ' answers to an earlier visit of this page are dropped
    m.rows.removeChildrenIndex(m.rows.getChildCount(), 0)
    m.slots = []         ' one for each row asked for, in the order they are shown: {heading, path, state, items, node, focus, total}
    m.shown = []         ' the slots on the page, top to bottom
    m.focus = 0          ' which of them has focus
    m.onAbout = false    ' true while a person's biography holds focus
    m.onFilters = false  ' true while a person's Filters chip holds focus
    m.role = "all"       ' a person's rows shown: "all", "cast" (acting) or "crew" (the rest)
    m.biography = ""
    m.filters.visible = false
    paintAbout()
    m.detail = invalid   ' a collection's own facts, once read
    m.detailFailed = false
    m.onActions = false  ' true while a collection's Play all and Shuffle hold focus
    m.actions.visible = false
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
    else if item.kind = "savedList"
        ' a profile's own list from Saved: its titles in its own order (a saved view's: what it finds now)
        placeWords(item.eyebrow, item.title, "", "", "")
        askRow(item.heading, item.path)
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
    eyebrow = "Collection"
    if full.eyebrow <> invalid then eyebrow = full.eyebrow
    placeWords(eyebrow, full.title, strong, span, about)
    ' Play all and Shuffle appear once there are titles to play
    if slot.state = "ok" and slot.items.count() > 0 and not m.actions.visible
        m.actions.buttons = [{id: "all", text: "Play all", icon: "play"}, {id: "shuffle", text: "Shuffle", icon: "shuffle"}]
        m.actions.visible = true
    end if
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
    if person.biography <> invalid
        about = oneParagraph(person.biography)
        m.biography = person.biography
    end if
    names = {actor: "Acting", director: "Directing", writer: "Writing", producer: "Production", composer: "Music", creator: "Created"}
    works = ""
    if person.roles <> invalid
        for each role in person.roles
            name = names[role.role]
            if name = invalid then name = ucase(left(role.role, 1)) + mid(role.role, 2)
            if works <> "" then works = works + ", "
            works = works + name
            askRow(name, "/v1/items?person=" + person.id + "&role=" + role.role + "&sort=release&order=desc")
            m.slots[m.slots.count() - 1].role = role.role
        end for
    end if
    placeWords(works, person.name, born, facts, about)
    ' Filters only when there is something to filter: rows of acting and rows of other work
    cast = false
    crew = false
    for each slot in m.slots
        if slot.role = "actor" then cast = true else crew = true
    end for
    m.filters.visible = (cast and crew)
    if m.filters.visible then m.filters.translation = [frames().rightEdge - m.filters.getChild(0).buttonWidth, frames().group.filtersY]
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
        if slot.node <> invalid and not roleShown(slot)
            slot.node.visible = false
        else if slot.node <> invalid
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
    ' focus lands on Play all, or else the first row, once there is one, if the page itself still holds it
    ' (a collection has Play all; a person has the biography and Filters; no page has both)
    if m.top.hasFocus() and m.actions.visible
        m.onActions = true
        m.actions.active = true
    else if m.shown.count() > 0 and m.top.hasFocus() and not m.onAbout
        m.shown[0].focus.active = true
    end if
end sub

sub onActive()
    if not m.top.active then return
    if m.state.visible
        m.state.active = true
    else if m.onActions
        m.actions.active = true
    else if m.onAbout
        m.top.setFocus(true)
    else if m.onFilters
        m.filters.active = true
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

' A row is shown under a person's Filters: every row for All, acting for Cast, the rest for Crew.
function roleShown(slot as object) as boolean
    if not m.isPerson or m.role = "all" or slot.role = invalid then return true
    if m.role = "cast" then return slot.role = "actor"
    return slot.role <> "actor"
end function

' The biography has focus: the plate behind it and the ring round it.
sub paintAbout()
    plate = m.top.findNode("aboutPlate")
    ring = m.top.findNode("aboutRing")
    plate.visible = m.onAbout
    ring.visible = m.onAbout
    if not m.onAbout then return
    pad = metrics().cardPad
    words = m.about.boundingRect()
    plate.translation = [m.about.translation[0] - pad, m.about.translation[1] - pad]
    plate.width = m.about.width + pad * 2
    plate.height = words.height + pad * 2
    plate.blendColor = colours().glass
    out = metrics().focusSpread + 6
    ring.translation = [plate.translation[0] - out, plate.translation[1] - out]
    ring.width = plate.width + out * 2
    ring.height = plate.height + out * 2
end sub

' The biography read in full has closed: focus is back on it.
sub onReaderClosed()
    m.top.setFocus(true)
end sub

function canReadAbout() as boolean
    return m.isPerson and m.biography <> "" and m.about.visible
end function

sub toAbout()
    m.onFilters = false
    m.onAbout = true
    paintAbout()
    m.top.setFocus(true)
end sub

sub toFilters()
    m.onAbout = false
    paintAbout()
    m.onFilters = true
    m.filters.active = true
end sub

sub toFirstRow()
    m.onAbout = false
    m.onFilters = false
    paintAbout()
    m.focus = 0
    placeRows()
    if m.shown.count() > 0 then m.shown[0].focus.active = true
end sub

' Filters: All, Cast or Crew, in the side sheet; the choice comes back in onMenuChosen.
sub onFilters()
    rows = []
    for each choice in [{id: "all", text: "All"}, {id: "cast", text: "Cast"}, {id: "crew", text: "Crew"}]
        rows.push({id: "role:" + choice.id, text: choice.text, icon: "", current: (m.role = choice.id)})
    end for
    m.top.menu = {eyebrow: m.top.item.title, title: "Filters", status: "", rows: rows, hints: [{keys: ["OK"], text: "Choose"}, {keys: ["Back"], text: "Close"}]}
end sub

function pressKey(key as string) as boolean
    ' the person's own stops above the rows: the biography, then Filters
    if m.onAbout
        if key <> "down" then return false
        if m.filters.visible then toFilters() else toFirstRow()
        return true
    end if
    if m.onFilters
        if key = "down"
            toFirstRow()
            return true
        else if key = "up" and canReadAbout()
            toAbout()
            return true
        end if
        return false
    end if
    if key = "up" and m.focus = 0 and m.isPerson and (m.filters.visible or canReadAbout())
        if m.filters.visible then toFilters() else toAbout()
        return true
    end if
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

' Play all and Shuffle, above the rows: Up from the first row reaches them, Down returns to it;
' other keys on them go on to the scene (Left to the rail, Back).
function actionsKey(key as string) as boolean
    if not m.onActions
        m.onActions = true
        m.actions.active = true
        return true
    end if
    if key <> "down" or m.shown.count() = 0 then return false
    m.onActions = false
    m.focus = 0
    placeRows()
    m.shown[0].focus.active = true
    return true
end function

' The collection's whole run, in its order or shuffled, as one queue the player moves through.
sub onPlay(event as object)
    items = m.slots[0].items
    ids = []
    for each item in items
        ids.push(item.id)
    end for
    first = items[0]
    queue = {source: {items: ids}}
    ' a list or saved view from Saved: the server builds the whole run, not only the 200 titles read here
    item = m.top.item
    if item.kind = "savedList" and item.listKind = "saved_view" then queue = {source: {homeRow: {id: "view:" + item.id}}}
    if event.getData() = "shuffle"
        ' the server plays startAt first and shuffles the rest, so the first is picked here
        first = items[rnd(items.count()) - 1]
        queue.shuffle = true
        queue.startAt = first.id
    end if
    m.top.open = {item: first, play: true, queue: queue}
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    ' OK on the biography reads it in full
    if press and key = "OK" and m.onAbout and not m.state.visible
        m.reader.model = {eyebrow: m.top.findNode("eyebrow").text, title: m.top.item.title, text: m.biography}
        return true
    end if
    if press and key = "options" then return cardKey()
    holdKey(key, press and (key = "up" or key = "down"))
    if press and not m.state.visible and (m.onActions or (key = "up" and m.focus = 0 and m.actions.visible)) then return actionsKey(key)
    if not press or m.state.visible then return false
    if key = "up" or key = "down" then return pressKey(key)     ' Up at the top goes on to the scene
    return false
end function

' * on a card in the rows: the title's menu (kit/CardMenu.brs).
function cardKey() as boolean
    if m.state.visible or not m.rows.isInFocusChain() or m.focus >= m.shown.count() then return false
    row = m.shown[m.focus]
    at = row.node.focusedIndex
    if row.state = "ok" and at >= 0 and at < row.items.count()
        m.menuAt = {row: row, at: at}
        m.top.menu = cardMenu(row.items[at], [], [])
    end if
    return true
end function

sub onMenuChosen(event as object)
    which = event.getData()
    if left(which, 5) = "role:"
        ' Filters chosen in the sheet: the rows change under the chip, and focus stays on it
        m.role = mid(which, 6)
        m.focus = 0
        placeRows()
        m.onFilters = true
        return
    end if
    cardMenuChosen(which)
end sub

' The card takes the title's new state (its watched tick); the row keeps its place.
sub cardMenuChanged(item as object)
    if m.menuAt = invalid then return
    card = gridCard(item)
    if item.state <> invalid then card.played = (item.state.watched = true)
    card.art = imageUrl(card.art, 200, 300)
    fresh = createObject("roSGNode", "ContentNode")
    fresh.addFields(card)
    m.menuAt.row.node.cards.replaceChild(fresh, m.menuAt.at)
end sub
