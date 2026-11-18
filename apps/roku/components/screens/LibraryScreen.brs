sub init()
    c = colours()
    f = frames()
    k = metrics()
    m.grid = m.top.findNode("grid")
    m.views = m.top.findNode("views")
    m.tools = m.top.findNode("tools")
    m.viewRing = m.top.findNode("viewRing")
    m.gridState = m.top.findNode("gridState")
    m.page = m.top.findNode("page")
    m.discover = m.top.findNode("discover")
    m.home = invalid
    name = m.top.findNode("name")
    setType(name, "title", c.text)
    name.translation = [f.gutter, f.browse.titleY]
    m.chevron = m.top.findNode("chevron")
    m.chevron.blendColor = c.textSecondary
    m.siblings = []
    m.views.translation = [0, f.browse.titleY + 12]
    m.top.findNode("viewMark").color = c.accent
    m.tools.translation = [f.gutter, f.grid.toolsY]
    m.chips = m.top.findNode("chips")
    m.chips.translation = [f.gutter, f.grid.chipsY]
    count = m.top.findNode("count")
    setType(count, "meta", c.textSecondary)
    count.translation = [f.gutter, f.grid.countY]
    m.grid.translation = [f.gutter, f.grid.cardsY - k.cardPad]
    m.grid.itemSpacing = [k.cardGap, 10]
    m.grid.clippingRect = [-60, -40, 1840, 1080 - f.grid.cardsY + 56]
    m.gridState.translation = [f.gutter, f.grid.cardsY]
    m.gridState.width = f.state.width
    m.grid.observeField("itemFocused", "onCardFocused")
    m.page.observeField("pressed", "onEmptyPressed")     ' an empty library's Browse Home (a failed page's Try again is onLibrary)
    m.grid.observeField("itemSelected", "onCardSelected")
    m.gridState.observeField("pressed", "onGridState")
    m.page.observeField("pressed", "onLibrary")
    m.region = "grid"     ' "views", "tools", "chips", "grid" or "page"
    m.view = 0
    m.tool = 0
    m.chip = 0
    m.sort = invalid
    m.quick = {}          ' which quick filters are on, by id
    m.chosen = {}         ' the Filters panel's chosen values: {filter id: [{value, label}]}
    m.facets = {}         ' each filter's choices as the server last listed them
    m.panelOpen = false   ' the Filters panel is showing (late facet answers redraw it)
    m.facetsWaiting = 0
    m.allTotal = invalid  ' the view's count with nothing narrowing it, for "10 of 80 movies"
    m.items = []
    m.cursor = invalid
    m.asking = false
    m.generation = 0
    m.detail = invalid
    m.top.observeField("focusedChild", "paint")
end sub

sub onLibrary()
    library = m.top.library
    m.top.findNode("name").text = library.name
    m.top.art = ""
    m.page.visible = false
    m.siblings = []
    m.chevron.visible = false
    api("GET", "/v1/libraries/" + library.id, invalid, "onDetail", {id: library.id})
    api("GET", "/v1/libraries", invalid, "onSiblings", {id: library.id})
end sub

' One list as a grid: the page's title and count over it, nothing to sort or filter.
sub onItem()
    list = m.top.item
    m.list = list
    m.libraryNames = {}
    api("GET", "/v1/libraries", invalid, "onLibraries")
    m.top.findNode("name").text = list.title
    m.top.art = ""
    m.detail = {views: [{kind: list.view}], sorts: [], filters: []}
    m.toolSpecs = []
    drawViews()
    drawTools()
    loadList()
end sub

' A list across libraries (a search kind, a genre) tells the same title in two libraries apart by
' the library's name.
sub onLibraries(event as object)
    result = event.getData()
    if not result.ok or result.data.items = invalid then return
    for each library in result.data.items
        m.libraryNames[library.id] = library.name
    end for
end sub

sub onDetail(event as object)
    result = event.getData()
    if event.getRoSGNode().ctx.id <> m.top.library.id then return
    if not result.ok
        showPageState(result)
        return
    end if
    m.detail = result.data
    if m.region = "page" then m.region = "grid"
    m.chosen = {}
    m.allTotal = invalid
    if m.detail.views = invalid or m.detail.views.count() = 0 then m.detail.views = [{kind: ""}]
    if m.detail.sorts <> invalid and m.detail.sorts.count() > 0 then m.sort = m.detail.sorts[0]
    m.filterSpecs = filterSpecs(m.detail.filters)
    m.view = 0
    buildTools()
    drawViews()
    drawChips()
    loadList()
    ' Discover is offered when the library's own Home has rows; it sits left of the first view,
    ' which the page opens on
    api("GET", "/v1/libraries/" + m.top.library.id + "/landing", invalid, "onLanding", {id: m.top.library.id})
end sub

sub onLanding(event as object)
    result = event.getData()
    if event.getRoSGNode().ctx.id <> m.top.library.id or not result.ok or result.data.rows = invalid then return
    if result.data.rows.count() = 0 or m.detail.views[0].kind = "discover" then return
    m.detail.views.unshift({kind: "discover"})
    m.view = m.view + 1
    drawViews()
    paint()
end sub

function onDiscover() as boolean
    return m.detail <> invalid and m.detail.views[m.view].kind = "discover"
end function

' Sort and Filters, and Jump to when the sort has letters or decades to jump to, then the quick
' filters this library's titles have the facts for (a music library has no "Unplayed"). Filters
' shows only when the library names a filter it can list.
sub buildTools()
    m.toolSpecs = [{id: "sort", text: "Sort", icon: "sort"}]
    if m.filterSpecs.count() > 0 then m.toolSpecs.push({id: "filters", text: "Filters", icon: "sliders"})
    if jumpFacet() <> "" then m.toolSpecs.push({id: "jump", text: "Jump to"})
    m.leading = m.toolSpecs.count()
    m.toolSpecs.append(quickFilters(m.detail.filters))
    if m.tool >= m.toolSpecs.count() then m.tool = m.toolSpecs.count() - 1
    drawTools()
end sub

' What Jump to jumps by for the sort in use: first letters by title, decades by year; "" for none.
function jumpFacet() as string
    if m.sort = invalid then return ""
    if m.sort.id = "title" then return "letter"
    if m.sort.id = "year" or m.sort.id = "release" then return "year"
    return ""
end function

' Nothing on the page could load: the whole-page state, in place of the page (rule 9).
' A library (or a view of it) with no titles at all (tv-focus frame 46): the toolbar and the grid
' give way to one state with one action, which has focus; the name and views stay above it.
' Titles that arrive later never take focus.
sub showEmpty(on as boolean)
    was = (m.empty = true)
    m.empty = on
    m.tools.visible = not on
    m.top.findNode("count").visible = not on
    if not on
        if was then m.page.visible = false
        return
    end if
    m.grid.visible = false
    m.gridState.visible = false
    m.page.icon = ""
    m.page.title = "Nothing here yet"
    m.page.body = "Titles appear in this library as the server finds them."
    m.page.buttons = [{id: "home", text: "Browse Home", icon: "home"}]
    m.page.visible = true
    if m.region <> "views" then m.region = "page"
    if m.top.isInFocusChain() then focusRegion()     ' never takes focus from the rail or a sheet
end sub

sub onEmptyPressed(event as object)
    if event.getData() = "home" and m.empty = true then m.top.goHome = true
end sub

' A view that showed nothing: is it empty in itself, or only narrowed to nothing? The view's own
' count, with nothing narrowing it, says which.
sub askEmpty()
    if m.top.library.id = invalid then return       ' a list that is not a library's (Search's See all) has no empty page
    view = m.detail.views[m.view]
    path = "/v1/items?library=" + m.top.library.id
    if view.kind <> "" then path = path + "&kind=" + view.kind
    api("GET", path + "&limit=1&total=true", invalid, "onEmptyCount", {generation: m.generation})
end sub

sub onEmptyCount(event as object)
    result = event.getData()
    if event.getRoSGNode().ctx.generation <> m.generation or not result.ok or result.data.total <> 0 then return
    showEmpty(true)
end sub

sub showPageState(result as object)
    m.grid.visible = false
    m.gridState.visible = false
    m.page.icon = "warning"
    m.page.title = "Couldn't open " + m.top.library.name
    m.page.body = whyFailed(result)
    m.page.buttons = [{id: "retry", text: "Try again now", icon: "refresh"}]
    m.page.visible = true
    m.region = "page"
    if m.top.isInFocusChain() then m.page.active = true
end sub

' The libraries holding the same kind as this one (Films and Films from TMDB): with more than one,
' the name carries a chevron and its menu switches between them (tv-focus frame 18).
sub onSiblings(event as object)
    result = event.getData()
    if event.getRoSGNode().ctx.id <> m.top.library.id or not result.ok or result.data.items = invalid then return
    m.siblings = []
    for each library in result.data.items
        if library.kind = m.top.library.kind then m.siblings.push(library)
    end for
    if m.siblings.count() < 2 then m.siblings = []
    m.chevron.visible = (m.siblings.count() > 0)
    name = m.top.findNode("name")
    m.chevron.translation = [frames().gutter + name.boundingRect().width + 12, frames().browse.titleY + (name.boundingRect().height - 20) / 2]
    if m.detail <> invalid then drawViews()
    paint()
end sub

function hasSwitcher() as boolean
    return m.siblings.count() > 0
end function

' The views beside the name, with the one shown underlined.
sub drawViews()
    c = colours()
    m.views.removeChildrenIndex(m.views.getChildCount(), 0)
    x = frames().gutter + m.top.findNode("name").boundingRect().width + 56
    if hasSwitcher() then x = x + 32       ' past the chevron
    m.viewX = []
    for each view in m.detail.views
        label = m.views.createChild("Label")
        label.text = viewName(view.kind)
        label.translation = [x, 0]
        setType(label, "tab", c.textTertiary)
        m.viewX.push([x, label.boundingRect().width])
        x = x + label.boundingRect().width + 40
    end for
    m.views.visible = (m.detail.views.count() > 1)
end sub

' The toolbar: kit buttons, Sort set apart from the quick filters by a rule. A filter that is on
' carries a tick and, out of focus, the blue plate.
sub drawTools()
    ' Save view keeps this view's filters and sort under a name (Saved > Saved views); it is always the
    ' toolbar's last control, at its right end, and only on a library's own page
    if m.savedAs = invalid then m.savedAs = ""
    last = m.toolSpecs.count() - 1
    if m.top.library.id <> invalid and (last < 0 or m.toolSpecs[last].id <> "save") then m.toolSpecs.push({id: "save", text: "Save view", icon: "plus"})
    c = colours()
    m.tools.removeChildrenIndex(m.tools.getChildCount(), 0)
    m.toolNodes = []
    x = 0
    for i = 0 to m.toolSpecs.count() - 1
        spec = m.toolSpecs[i]
        b = m.tools.createChild("Button")
        on = (m.quick[spec.id] = true)
        if spec.icon <> invalid then b.icon = spec.icon
        if on then b.icon = "check"
        b.switchedOn = on
        if spec.id = "sort" and m.sort <> invalid then b.text = sortName(m.sort.id) else b.text = spec.text
        if spec.id = "filters" and chosenCount() > 0 then b.detail = chosenCount().toStr()
        if spec.id = "save" and m.savedAs <> ""
            ' saved: says so until the view changes
            b.icon = "check"
            b.text = "Saved"
        else if spec.id = "save" and m.saveFailed = true
            b.text = "Not saved: try again"
        end if
        b.translation = [x, 0]
        if spec.id = "save" then b.translation = [frames().rightEdge - frames().gutter - b.buttonWidth, 0]
        m.toolNodes.push(b)
        x = x + b.buttonWidth + 18
        if i = m.leading - 1 and m.toolSpecs.count() > m.leading
            rule = m.tools.createChild("Rectangle")
            rule.color = c.line
            rule.width = 2
            rule.height = 40
            rule.translation = [x, 16]
            x = x + 20
        end if
    end for
    paint()
end sub

function chosenCount() as integer
    n = 0
    for each id in m.chosen
        n = n + m.chosen[id].count()
    end for
    return n
end function

' The chosen filter values as chips on the count's line, each removed with OK, then Clear all; the
' count follows them.
sub drawChips()
    m.chips.removeChildrenIndex(m.chips.getChildCount(), 0)
    m.chipSpecs = []
    for each spec in m.filterSpecs
        values = m.chosen[spec.id]
        if values <> invalid
            for each v in values
                m.chipSpecs.push({id: spec.id, value: v.value, text: spec.label + ": " + v.label})
            end for
        end if
    end for
    if m.chipSpecs.count() > 0 then m.chipSpecs.push({id: "", text: "Clear all"})
    x = 0
    for each spec in m.chipSpecs
        b = m.chips.createChild("Button")
        if spec.id <> "" then b.icon = "close"
        b.text = spec.text
        b.translation = [x, 0]
        x = x + b.buttonWidth + 18
    end for
    if m.chip >= m.chipSpecs.count() then m.chip = m.chipSpecs.count() - 1
    if m.chip < 0 then m.chip = 0
    gap = 0
    if x > 0 then gap = 6
    count = m.top.findNode("count")
    count.translation = [frames().gutter + x + gap, frames().grid.countY]
    if m.region = "chips" and m.chipSpecs.count() = 0 then m.region = "tools"
end sub

' Anything narrowing the view: a quick filter or a chosen value.
function narrowed() as boolean
    return m.quick.count() > 0 or chosenCount() > 0
end function

function listPath() as string
    if m.list <> invalid then return m.list.path
    view = m.detail.views[m.view]
    q = "/v1/items?library=" + m.top.library.id
    if view.kind <> "" then q = q + "&kind=" + view.kind
    if m.sort <> invalid then q = q + "&sort=" + m.sort.id + "&order=" + m.sort.order
    for each spec in m.toolSpecs
        if m.quick[spec.id] = true then q = q + "&" + spec.param
    end for
    return q + filterParams(m.chosen, m.filterSpecs)
end function

' Read the list from its start (a new view, sort or filter). The page never waits for the count.
sub loadList()
    if m.detail = invalid then return
    ' the grid's cells hold the card with headroom for the one in focus to grow (metrics().cardPad)
    card = shelfMetrics(gridShape()).card
    m.grid.itemSize = [card[0], card[1] + metrics().cardPad * 2]
    m.gridState.visible = false
    m.grid.visible = true
    showEmpty(false)
    m.generation = m.generation + 1
    m.items = []
    m.cursor = invalid
    m.asking = true
    m.total = invalid
    m.top.findNode("count").text = ""
    api("GET", listPath() + "&limit=60", invalid, "onPage", {generation: m.generation, first: true})
    api("GET", listPath() + "&limit=1&total=true", invalid, "onTotal", {generation: m.generation, all: false})
    if narrowed() and m.allTotal = invalid
        view = m.detail.views[m.view]
        whole = "/v1/items?library=" + m.top.library.id
        if view.kind <> "" then whole = whole + "&kind=" + view.kind
        api("GET", whole + "&limit=1&total=true", invalid, "onTotal", {generation: m.generation, all: true})
    end if
    if m.savedAs <> "" and m.savedAs <> listPath()
        m.savedAs = ""        ' the view changed since it was saved
        drawTools()
    end if
end sub

' The count under the toolbar: "104 movies", or "10 of 104 movies" while something narrows the view.
sub onTotal(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if ctx.generation <> m.generation or not result.ok or result.data.total = invalid then return
    if ctx.all then m.allTotal = result.data.total else m.total = result.data.total
    if m.total = invalid then return
    kind = m.detail.views[m.view].kind
    text = kindCount(m.total, kind)
    if narrowed() and m.allTotal <> invalid then text = m.total.toStr() + " of " + kindCount(m.allTotal, kind)
    m.top.findNode("count").text = text
    if not ctx.all then refreshPanel()
end sub

sub onPage(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if ctx.generation <> m.generation then return
    m.asking = false
    ' the plain cards that stood in for this page give their places to its titles (or go, if it failed)
    if ctx.first <> true and m.grid.content <> invalid
        extra = m.grid.content.getChildCount() - m.items.count()
        if extra > 0 then m.grid.content.removeChildrenIndex(extra, m.items.count())
    end if
    if not result.ok
        ' the toolbar works, so only the grid's part says so; a later page is asked again on the next move
        if ctx.first = true then gridSays("error", "Couldn't load these titles", whyFailed(result), "Try again")
        return
    end if
    page = result.data
    if page.groups <> invalid
        ' one kind of a search: the page is that kind's group, whose first page carries the count
        ' (the count's own request reads no total from a search, and so leaves this one)
        page = {items: [], nextCursor: invalid}
        if result.data.groups.count() > 0 then page = result.data.groups[0]
        if ctx.first = true and page.total <> invalid then m.top.findNode("count").text = kindCount(page.total, m.detail.views[m.view].kind)
    end if
    fresh = withoutReading(page.items)
    m.cursor = page.nextCursor
    if ctx.first = true
        ' the grid keeps its content node and only its cards change: a new node, set while the
        ' toolbar has focus, drew a focus ring on the first card as well
        if m.grid.content = invalid then m.grid.content = createObject("roSGNode", "ContentNode")
        m.grid.content.removeChildrenIndex(m.grid.content.getChildCount(), 0)
    end if
    copies = []
    if m.list <> invalid then copies = copyNames(fresh, m.libraryNames)
    for i = 0 to fresh.count() - 1
        item = fresh[i]
        card = cardOf(item, "")
        if m.list <> invalid and copies[i] <> "" then card.sub = copies[i]
        m.grid.content.createChild("ContentNode").addFields(card)
        ' a collection or an artist with no picture of its own borrows one from what it holds
        if card.art = "" and (item.kind = "collection" or item.kind = "artist") then askArt(m.items.count() + i, item)
    end for
    m.items.append(fresh)
    if ctx.first <> true and m.jumpTarget <> invalid
        if m.cursor = invalid and m.jumpTarget >= m.items.count() then m.jumpTarget = m.items.count() - 1
        landJump()
    end if
    if ctx.first = true
        if m.items.count() = 0
            if chosenCount() > 0
                gridSays("empty", "Nothing here", "No titles match these filters.", "Clear all")
            else if m.quick.count() > 0
                gridSays("empty", "Nothing here", "No titles match. Switch a filter off to see more.", "")
            else
                gridSays("empty", "Nothing here", "There's nothing in this view yet.", "")
            end if
        end if
        if m.items.count() > 0 then m.grid.jumpToItem = 0
        if m.items.count() = 0 then askEmpty()
        focusRegion()
    end if
end sub

' Music and audiobooks are square covers (an artist too); films and shows are posters.
function gridShape() as string
    kind = m.detail.views[m.view].kind
    if kind = "artist" or kind = "collection" or isCover(kind) then return "gridSquare"
    return "gridPoster"
end function

' A card for the grid: a poster, a square cover (music, audiobooks, collections), or an artist's
' circle; `found` is what was read from what the title holds ({art, tiles, total}), or "" for nothing.
function cardOf(item as object, found as dynamic) as object
    art = ""
    if type(found) <> "roAssociativeArray" then found = invalid
    if found <> invalid then art = found.art
    if item.kind = "artist"
        if art = "" then art = pick(item.images, ["photo", "portrait", "cover", "poster"])
        if art <> "" then art = imageUrl(art, 150, 150)
        return personCard({personId: item.id, name: item.title, role: "", image: art})
    end if
    card = gridCard(item)
    if art <> "" then card.art = art
    if found <> invalid and found.total > 0 and card.sub = "" then card.sub = countText(found.total, "title", "titles")
    if found <> invalid and found.tiles.count() = 4
        card.tiles = []
        for each tile in found.tiles
            card.tiles.push(imageUrl(tile, 100, 100))
        end for
    end if
    if gridShape() = "gridSquare"
        card.artHeight = 200
        card.art = imageUrl(card.art, 200, 200)
    else
        card.art = imageUrl(card.art, 200, 300)
    end if
    return card
end function

' A collection shows a mosaic of its first four titles' posters (one poster when it holds fewer)
' and how many it holds; an artist a circle cut from their first album's cover (tv-more frame 9).
sub askArt(at as integer, item as object)
    path = "/v1/items?collection=" + item.id + "&sort=order&limit=4&total=true"
    if item.kind = "artist" then path = "/v1/items?parent=" + item.id + "&kind=album&limit=1"
    api("GET", path, invalid, "onArtFound", {generation: m.generation, at: at})
end sub

sub onArtFound(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if ctx.generation <> m.generation or not result.ok or result.data.items = invalid or result.data.items.count() = 0 then return
    if ctx.at >= m.items.count() then return
    items = result.data.items
    found = {art: pick(items[0].images, ["poster", "cover"]), tiles: [], total: 0}
    if result.data.total <> invalid then found.total = result.data.total
    if items.count() = 4
        for each one in items
            poster = pick(one.images, ["poster", "cover"])
            if poster <> "" then found.tiles.push(poster)
        end for
    end if
    if found.art = "" and found.tiles.count() < 4 and found.total = 0 then return
    node = createObject("roSGNode", "ContentNode")
    node.addFields(cardOf(m.items[ctx.at], found))
    m.grid.content.replaceChild(node, ctx.at)
end sub

' The grid's part of the page could not load, or has nothing: one row in its place (rule 9).
sub gridSays(kind as string, title as string, reason as string, action as string)
    m.grid.visible = false
    m.gridState.kind = kind
    m.gridState.title = title
    m.gridState.reason = reason
    m.gridState.action = action
    m.gridState.visible = true
    if action = "" and m.region = "grid" then m.region = "tools"
    focusRegion()
end sub

' The grid's row pressed: Try again after a failed read, or Clear all when the filters leave nothing.
sub onGridState()
    if m.gridState.kind = "empty" then clearFilters() else loadList()
end sub

sub clearFilters()
    m.chosen = {}
    drawTools()
    drawChips()
    loadList()
end sub

sub onCardFocused(event as object)
    at = event.getData()
    ' the grid keeps going: the next page is asked for about three rows before the end
    if m.cursor <> invalid and not m.asking and at >= m.items.count() - 21
        m.asking = true
        api("GET", listPath() + "&limit=60&cursor=" + m.cursor, invalid, "onPage", {generation: m.generation, first: false})
        ' while it comes, one row of plain cards past the last (tv-focus frame 19): Down from the
        ' last row lands on the one in its own column and waits there for the real one
        columns = m.grid.numColumns
        pad = (columns - m.items.count() mod columns) mod columns + columns
        tall = m.grid.content.getChild(m.items.count() - 1).artHeight     ' the box of the cards above it
        for i = 1 to pad
            m.grid.content.createChild("ContentNode").addFields({skeleton: true, kind: "", art: "", title: "", artHeight: tall})
        end for
    end if
end sub

sub onCardSelected(event as object)
    at = event.getData()
    if at >= 0 and at < m.items.count() then m.top.open = {item: m.items[at], play: false}
end sub

' --- Save view: Roku's keyboard for the name, then the view is kept as the profile's saved view

sub askViewName()
    parts = []
    for each spec in m.toolSpecs
        if m.quick[spec.id] = true then parts.push(spec.text)
    end for
    for each spec in m.filterSpecs
        if m.chosen[spec.id] <> invalid
            for each v in m.chosen[spec.id]
                parts.push(v.label)
            end for
        end if
    end for
    sort = ""
    if m.sort <> invalid then sort = sortName(m.sort.id)
    dialog = createObject("roSGNode", "StandardKeyboardDialog")
    dialog.title = "Save view"
    dialog.message = ["Name this view. It is kept in Saved, under Saved views, with its filters and sort."]
    dialog.buttons = ["Save", "Cancel"]
    dialog.text = savedViewName(m.top.library.name, parts, sort)
    dialog.observeField("buttonSelected", "onViewNamed")
    m.dialog = dialog
    m.top.getScene().dialog = dialog
end sub

sub onViewNamed()
    dialog = m.dialog
    if dialog = invalid then return
    name = dialog.text.trim()
    save = (dialog.buttonSelected = 0)
    dialog.close = true
    m.dialog = invalid
    if not save or name = "" then return
    path = listPath()
    query = mid(path, instr(1, path, "?") + 1)
    api("POST", "/v1/lists", {kind: "saved_view", name: name, library: m.top.library.id, query: query}, "onViewSaved", {path: path})
end sub

sub onViewSaved(event as object)
    result = event.getData()
    if not result.ok
        ' the toolbar's own button says it was not kept; pressing it again asks again
        m.saveFailed = true
        drawTools()
        return
    end if
    m.saveFailed = false
    m.savedAs = event.getRoSGNode().ctx.path
    drawTools()
end sub

sub focusRegion()
    if m.region = "tools" and m.empty = true then m.region = "page"     ' an empty library has no toolbar: Browse Home
    if m.region = "page"
        m.page.active = true
    else if m.region = "grid" and onDiscover()
        m.home.active = true
    else if m.region = "grid" and m.grid.visible
        m.grid.setFocus(true)
    else if m.region = "grid" and m.gridState.visible
        m.gridState.active = true
    else
        ' the toolbar and views hold focus as one group beside the grid: focusing the page itself
        ' left the grid holding it (Up lit Sort while Right and OK still went to the cards)
        m.tools.setFocus(true)
    end if
    paint()
end sub

sub onActive()
    if m.top.active then focusRegion()
end sub

sub onToTop()
    if m.grid.visible and m.items.count() > 0 then m.grid.jumpToItem = 0
    if m.region <> "page" then m.region = "grid"
end sub

sub paint()
    c = colours()
    has = m.top.isInFocusChain()
    if m.toolNodes <> invalid
        for i = 0 to m.toolNodes.count() - 1
            m.toolNodes[i].on = (has and m.region = "tools" and i = m.tool)
        end for
    end if
    for i = 0 to m.chips.getChildCount() - 1
        m.chips.getChild(i).on = (has and m.region = "chips" and i = m.chip)
    end for
    m.viewRing.visible = false
    mark = m.top.findNode("viewMark")
    if m.viewX = invalid then return
    for i = 0 to m.views.getChildCount() - 1
        label = m.views.getChild(i)
        if i = m.view then label.color = c.text else label.color = c.textTertiary
    end for
    mark.visible = m.views.visible
    y = frames().browse.titleY
    if m.viewX.count() > m.view
        mark.translation = [m.viewX[m.view][0], y + 52]
        mark.width = m.viewX[m.view][1]
    end if
    if has and m.region = "switch"
        ' the ring round the name and its chevron
        out = metrics().focusSpread + 6
        name = m.top.findNode("name")
        m.viewRing.visible = true
        m.viewRing.translation = [frames().gutter - 18 - out, y - out]
        m.viewRing.width = name.boundingRect().width + 32 + 36 + out * 2
        m.viewRing.height = name.boundingRect().height + out * 2
    end if
    if has and m.region = "views"
        out = metrics().focusSpread + 6
        m.viewRing.visible = true
        m.viewRing.translation = [m.viewX[m.view][0] - 18 - out, y + 2 - out]
        m.viewRing.width = m.viewX[m.view][1] + 36 + out * 2
        m.viewRing.height = 56 + out * 2
    end if
end sub

sub onMenuChosen(event as object)
    if left(event.getData(), 4) = "lib:"
        ' a library from the name's menu: the scene opens it
        for each library in m.siblings
            if library.id = mid(event.getData(), 5) and library.id <> m.top.library.id then m.top.switchTo = library.id
        end for
        return
    end if
    id = event.getData()
    if onDiscover()
        m.home.menuChosen = id        ' a title's menu opened from a Discover row
        return
    end if
    if left(id, 2) = "j:"
        jumpTo(m.jumps[id.mid(2).toInt()])
        return
    end if
    if left(id, 2) = "f:" or id = "clear"
        chooseFilter(id)
        return
    end if
    if id = "show"
        m.panelOpen = false      ' the panel closes on the list it has narrowed
        return
    end if
    m.panelOpen = false
    for each sort in m.detail.sorts
        if sort.id = id then m.sort = sort
    end for
    buildTools()
    loadList()
end sub

' --- Jump to: the letters (or decades) the list holds, each with how many titles; a choice puts
' focus on its first card, reading on until that card has arrived.

sub askJump()
    facet = jumpFacet()
    path = listPath()
    api("GET", "/v1/facets" + mid(path, len("/v1/items") + 1) + "&facet=" + facet, invalid, "onJumps", {generation: m.generation})
end sub

sub onJumps(event as object)
    result = event.getData()
    if event.getRoSGNode().ctx.generation <> m.generation or not result.ok or result.data.values = invalid then return
    values = result.data.values
    ' letters come in A to Z order, decades newest first: turn them round when the list runs the other way
    turn = (jumpFacet() = "letter" and m.sort.order = "desc") or (jumpFacet() = "year" and m.sort.order = "asc")
    if turn then values.reverse()
    rows = []
    m.jumps = []
    at = 0
    for i = 0 to values.count() - 1
        v = values[i]
        label = v.value
        if v.label <> invalid then label = v.label
        rows.push({id: "j:" + i.toStr(), text: label, value: v.count.toStr()})
        m.jumps.push(at)
        at = at + v.count
    end for
    if rows.count() = 0 then return
    m.top.menu = {eyebrow: m.top.library.name, title: "Jump to", status: "", rows: rows}
end sub

sub jumpTo(position as integer)
    m.jumpTarget = position
    landJump()
end sub

' The jump lands once its card has arrived; until then the list reads on from where it has got to.
sub landJump()
    if m.jumpTarget = invalid then return
    if m.jumpTarget < m.items.count()
        m.grid.jumpToItem = m.jumpTarget
        m.jumpTarget = invalid
        m.region = "grid"
        focusRegion()
    else if not m.asking
        m.asking = true
        api("GET", listPath() + "&limit=200&at=" + m.items.count().toStr(), invalid, "onPage", {generation: m.generation, first: false})
    end if
end sub

sub changeView()
    ' another view starts afresh: no filter chosen, and Down from its tab lands on the toolbar's
    ' first control, not the one this view remembered
    m.chosen = {}
    m.allTotal = invalid
    m.tool = 0
    showDiscover(onDiscover())
    if onDiscover() then return
    drawTools()
    drawChips()
    loadList()
end sub

' The Discover tab shows the library's own Home in place of the toolbar and the grid.
sub showDiscover(on as boolean)
    for each part in [m.tools, m.chips, m.top.findNode("count"), m.grid, m.gridState]
        part.visible = not on
    end for
    if on
        m.gridState.visible = false
        if m.home = invalid
            m.home = m.discover.createChild("HomeScreen")
            m.discover.translation = [0, frames().grid.discoverDrop]
            m.home.observeField("open", "onHomeOpen")
            m.home.observeField("art", "onHomeArt")
            m.home.observeField("menu", "onHomeMenu")
            m.home.library = m.top.library.id
        end if
    else
        m.top.art = ""
    end if
    m.discover.visible = on
end sub

sub onHomeOpen(event as object)
    m.top.open = event.getData()
end sub

sub onHomeArt(event as object)
    if onDiscover() then m.top.art = event.getData()
end sub

sub onHomeMenu(event as object)
    m.top.menu = event.getData()
end sub

sub onMenuClosed()
    m.panelOpen = false
end sub

' --- the Filters panel (tv.html frame 4): one heading for each filter, its choices opening in place
' under it, each with how many titles it would leave; a choice switches on or off at once, the
' panel stays open on it, and Back returns to the Filters button.

sub openFilters()
    m.panelOpen = true
    m.panelHeading = ""
    m.panelFocus = ""
    askFacets()
end sub

' Each filter's choices over the view as it is now narrowed (a filter's own choices are not narrowed by it).
sub askFacets()
    m.facetGeneration = m.generation
    m.nextFacets = {}     ' the panel on screen keeps reading m.facets until every list has answered
    m.facetsWaiting = m.filterSpecs.count()
    path = listPath()
    for each spec in m.filterSpecs
        if spec.id = "collection"
            ' the server has no facet for collections: its choices are the library's collections
            api("GET", "/v1/items?library=" + m.top.library.id + "&kind=collection&sort=title&limit=200", invalid, "onFacets", {generation: m.generation, id: spec.id})
        else if spec.id = "playState"
            m.nextFacets.playState = playStates()
            m.facetsWaiting = m.facetsWaiting - 1
        else
            api("GET", "/v1/facets" + mid(path, len("/v1/items") + 1) + "&facet=" + spec.id, invalid, "onFacets", {generation: m.generation, id: spec.id})
        end if
    end for
    if m.facetsWaiting = 0 then m.facets = m.nextFacets
end sub

sub onFacets(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if ctx.generation <> m.facetGeneration then return
    m.nextFacets[ctx.id] = []
    if result.ok and result.data.values <> invalid then m.nextFacets[ctx.id] = result.data.values
    if ctx.id = "year" then m.nextFacets.year.reverse()      ' decades oldest first, as tv.html frame 4 lists them
    if result.ok and result.data.items <> invalid
        ' a library's collections as choices, with how many titles each holds
        for each item in result.data.items
            n = 0
            if item.counts <> invalid and item.counts.children <> invalid then n = item.counts.children
            m.nextFacets[ctx.id].push({value: item.id, label: item.title, count: n})
        end for
    end if
    m.facetsWaiting = m.facetsWaiting - 1
    if m.facetsWaiting = 0 then m.facets = m.nextFacets
    refreshPanel()
end sub

' The panel is drawn again once every list and the count have answered, so its headings never
' move under focus while answers arrive one by one.
sub refreshPanel()
    if m.panelOpen and m.facetsWaiting = 0 and m.total <> invalid then showFilters(m.panelHeading, m.panelFocus)
end sub

sub showFilters(heading as string, focus as string)
    rows = []
    group = ""
    for each spec in m.filterSpecs
        values = m.facets[spec.id]
        chosen = m.chosen[spec.id]
        if (values <> invalid and values.count() > 0) or (chosen <> invalid and chosen.count() > 0)
            if spec.group <> group
                ' each group of filters under its name (About the title, Your activity, About the file)
                group = spec.group
                rows.push({id: "g:" + group, text: group, label: true, gapBefore: rows.count() > 0})
            end if
            rows.push({id: "h:" + spec.id, text: spec.label, heading: true, value: filterSummary(chosen)})
            rows.push({id: "help:" + spec.id, text: spec.help, label: true, under: "h:" + spec.id})
            if values <> invalid
                for i = 0 to values.count() - 1
                    v = values[i]
                    label = v.value
                    if v.label <> invalid then label = v.label
                    count = ""
                    if v.count <> invalid then count = v.count.toStr()
                    rows.push({id: "f:" + spec.id + ":" + i.toStr(), text: label, value: count, under: "h:" + spec.id, current: isChosen(spec.id, v.value)})
                end for
            end if
        end if
    end for
    ' until the new count arrives, the panel says the last one
    if m.total <> invalid then m.matching = kindCount(m.total, m.detail.views[m.view].kind) + " match"
    ' the panel's foot: show what matches (closes the panel), and Clear all (tv.html frame 4)
    if m.total <> invalid then rows.push({id: "show", text: "Show " + kindCount(m.total, m.detail.views[m.view].kind), gapBefore: true})
    if chosenCount() > 0 then rows.push({id: "clear", text: "Clear all", gapBefore: m.total = invalid})
    status = ""
    if m.matching <> invalid then status = m.matching
    for each row in rows
        if row.id = focus then row.focus = true
    end for
    model = {eyebrow: m.top.library.name, title: "Filters", status: status, rows: rows}
    if heading <> "" then model.open = heading
    m.top.menu = model
end sub

function oneChoice(id as string) as boolean
    for each spec in m.filterSpecs
        if spec.id = id then return spec.one = true
    end for
    return false
end function

function isChosen(id as string, value as string) as boolean
    values = m.chosen[id]
    if values = invalid then return false
    for each v in values
        if v.value = value then return true
    end for
    return false
end function

' A choice in the panel switches on or off, the list follows at once, and the panel opens again
' on that choice with every list's counts read afresh.
sub chooseFilter(id as string)
    if id = "clear"
        m.chosen = {}
        m.panelHeading = ""
        m.panelFocus = ""
    else
        parts = id.split(":")
        filter = parts[1]
        v = m.facets[filter][parts[2].toInt()]
        label = v.value
        if v.label <> invalid then label = v.label
        values = m.chosen[filter]
        if values = invalid then values = []
        kept = []
        for each one in values
            if one.value <> v.value then kept.push(one)
        end for
        if kept.count() = values.count() then kept.push({value: v.value, label: label})
        ' a filter of one choice at a time keeps only the newest
        if kept.count() > 1 and oneChoice(filter) then kept = [kept[kept.count() - 1]]
        if kept.count() > 0 then m.chosen[filter] = kept else m.chosen.delete(filter)
        m.panelHeading = "h:" + filter
        m.panelFocus = id
    end if
    m.panelOpen = true
    drawTools()
    drawChips()
    loadList()
    showFilters(m.panelHeading, m.panelFocus)
    askFacets()
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if press and key = "up" and m.region = "page" and m.empty = true and m.detail.views.count() > 1
        m.region = "views"      ' Up from an empty library's Browse Home reaches its views
        focusRegion()
        return true
    end if
    if not press or m.detail = invalid or m.region = "page" then return false
    if m.list <> invalid then return key = "up"     ' one list: the grid is the whole page
    if m.region = "grid"
        if onDiscover()
            if key <> "up" then return false
            m.region = "views"          ' Up from Discover's first row
            focusRegion()
            return true
        end if
        if key = "up"
            m.region = "tools"
            if m.chipSpecs <> invalid and m.chipSpecs.count() > 0 then m.region = "chips"
            focusRegion()
            return true
        end if
        return false          ' Left from the first column: the rail
    end if
    if m.region = "chips"
        if key = "right"
            if m.chip < m.chipSpecs.count() - 1 then m.chip = m.chip + 1
        else if key = "left"
            if m.chip = 0 then return false
            m.chip = m.chip - 1
        else if key = "up"
            m.region = "tools"
        else if key = "down"
            if m.grid.visible or (m.gridState.visible and m.gridState.action <> "") then m.region = "grid"
        else if key = "OK"
            ' a chip takes its value off; Clear all takes every one
            spec = m.chipSpecs[m.chip]
            if spec.id = ""
                m.chosen = {}
            else
                kept = []
                for each one in m.chosen[spec.id]
                    if one.value <> spec.value then kept.push(one)
                end for
                if kept.count() > 0 then m.chosen[spec.id] = kept else m.chosen.delete(spec.id)
            end if
            drawTools()
            drawChips()
            loadList()
        else
            return false
        end if
        focusRegion()
        return true
    end if
    if m.region = "tools"
        if key = "right"
            if m.tool < m.toolSpecs.count() - 1 then m.tool = m.tool + 1
        else if key = "left"
            if m.tool = 0 then return false
            m.tool = m.tool - 1
        else if key = "down"
            if m.chipSpecs <> invalid and m.chipSpecs.count() > 0
                m.region = "chips"
            else if m.grid.visible or (m.gridState.visible and m.gridState.action <> "")
                m.region = "grid"
            end if
        else if key = "up"
            if m.detail.views.count() > 1
                m.region = "views"
            else if hasSwitcher()
                m.region = "switch"
            else
                return false        ' Up from the top goes on to the scene (the now-playing card)
            end if
        else if key = "OK"
            spec = m.toolSpecs[m.tool]
            if spec.id = "save"
                if m.savedAs = "" then askViewName()
                return true
            end if
            if spec.id = "sort"
                rows = []
                for each sort in m.detail.sorts
                    rows.push({id: sort.id, text: sortName(sort.id), current: m.sort <> invalid and m.sort.id = sort.id})
                end for
                m.top.menu = {eyebrow: m.top.library.name, title: "Sort by", status: "", rows: rows}
                return true
            end if
            if spec.id = "filters"
                openFilters()
                return true
            end if
            if spec.id = "jump"
                askJump()
                return true
            end if
            ' a quick filter changes the list in place and keeps focus on its chip
            if m.quick[spec.id] = true then m.quick.delete(spec.id) else m.quick[spec.id] = true
            drawTools()
            loadList()
            return true
        else
            return false
        end if
        focusRegion()
        return true
    end if
    if m.region = "switch"
        ' the library's name: OK lists the libraries of its kind; Left is the rail
        if key = "OK"
            rows = []
            for each library in m.siblings
                rows.push({id: "lib:" + library.id, text: library.name, current: library.id = m.top.library.id})
            end for
            m.top.menu = {eyebrow: "", title: "Libraries", status: "", rows: rows}
            return true
        else if key = "right" and m.detail.views.count() > 1
            m.region = "views"
        else if key = "down"
            m.region = "tools"
        else if key = "left"
            return false
        end if
        focusRegion()
        return true
    end if
    if key = "left" and m.view = 0 and hasSwitcher()
        m.region = "switch"     ' Left from the first view: the library's name
        focusRegion()
        return true
    end if
    if key = "down" and onDiscover()
        m.region = "grid"       ' Down from the views on Discover: its rows
        focusRegion()
        return true
    end if
    ' the views: moving along them changes the list as focus lands
    if key = "right" and m.view < m.detail.views.count() - 1
        m.view = m.view + 1
        changeView()
    else if key = "left"
        if m.view = 0 then return false
        m.view = m.view - 1
        changeView()
    else if key = "down"
        m.region = "tools"
    else if key <> "right"
        return false              ' Up from the views goes on to the scene
    end if
    focusRegion()
    return true
end function
