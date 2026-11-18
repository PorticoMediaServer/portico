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
    name = m.top.findNode("name")
    setType(name, "title", c.text)
    name.translation = [f.gutter, f.browse.titleY]
    m.views.translation = [0, f.browse.titleY + 12]
    m.top.findNode("viewMark").color = c.accent
    m.tools.translation = [f.gutter, f.grid.toolsY]
    m.chips = m.top.findNode("chips")
    m.chips.translation = [f.gutter, f.grid.chipsY]
    count = m.top.findNode("count")
    setType(count, "meta", c.textSecondary)
    count.translation = [f.gutter, f.grid.countY]
    ' The grid's cells hold the card with headroom for the one in focus to grow (metrics().cardPad).
    card = shelfMetrics("gridPoster").card
    m.grid.translation = [f.gutter, f.grid.cardsY - k.cardPad]
    m.grid.itemSize = [card[0], card[1] + k.cardPad * 2]
    m.grid.itemSpacing = [k.cardGap, 10]
    m.grid.clippingRect = [-60, -40, 1840, 1080 - f.grid.cardsY + 56]
    m.gridState.translation = [f.gutter, f.grid.cardsY]
    m.gridState.width = f.state.width
    m.grid.observeField("itemFocused", "onCardFocused")
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
    api("GET", "/v1/libraries/" + library.id, invalid, "onDetail", {id: library.id})
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
    ' Sort and Filters, then the quick filters this library's titles have the facts for (a music
    ' library has no "Unplayed"). Filters shows only when the library names a filter it can list.
    m.filterSpecs = filterSpecs(m.detail.filters)
    m.toolSpecs = [{id: "sort", text: "Sort", icon: "sort"}]
    if m.filterSpecs.count() > 0 then m.toolSpecs.push({id: "filters", text: "Filters", icon: "sliders"})
    m.leading = m.toolSpecs.count()
    m.toolSpecs.append(quickFilters(m.detail.filters))
    drawViews()
    drawTools()
    drawChips()
    loadList()
end sub

' Nothing on the page could load: the whole-page state, in place of the page (rule 9).
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

' The views beside the name, with the one shown underlined.
sub drawViews()
    c = colours()
    m.views.removeChildrenIndex(m.views.getChildCount(), 0)
    x = frames().gutter + m.top.findNode("name").boundingRect().width + 56
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
        b.translation = [x, 0]
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
    m.gridState.visible = false
    m.grid.visible = true
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
    if not result.ok
        ' the toolbar works, so only the grid's part says so; a later page is asked again on the next move
        if ctx.first = true then gridSays("error", "Couldn't load these titles", whyFailed(result), "Try again")
        return
    end if
    fresh = withoutReading(result.data.items)
    m.cursor = result.data.nextCursor
    if ctx.first = true
        ' the grid keeps its content node and only its cards change: a new node, set while the
        ' toolbar has focus, drew a focus ring on the first card as well
        if m.grid.content = invalid then m.grid.content = createObject("roSGNode", "ContentNode")
        m.grid.content.removeChildrenIndex(m.grid.content.getChildCount(), 0)
    end if
    for i = 0 to fresh.count() - 1
        item = fresh[i]
        card = cardOf(item, "")
        m.grid.content.createChild("ContentNode").addFields(card)
        ' a collection with no picture of its own borrows one from what it holds
        if card.art = "" and item.kind = "collection" then askArt(m.items.count() + i, item)
    end for
    m.items.append(fresh)
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
        focusRegion()
    end if
end sub

' A card for the grid; `art` is a picture borrowed from what the title holds, "" for its own.
function cardOf(item as object, art as string) as object
    card = gridCard(item)
    if art <> "" then card.art = art
    card.art = imageUrl(card.art, 200, 300)
    return card
end function

' A collection shows its first title's poster when it has none of its own.
sub askArt(at as integer, item as object)
    api("GET", "/v1/items?collection=" + item.id + "&sort=order&limit=1", invalid, "onArtFound", {generation: m.generation, at: at})
end sub

sub onArtFound(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if ctx.generation <> m.generation or not result.ok or result.data.items = invalid or result.data.items.count() = 0 then return
    if ctx.at >= m.items.count() then return
    art = pick(result.data.items[0].images, ["poster", "cover"])
    if art = "" then return
    node = createObject("roSGNode", "ContentNode")
    node.addFields(cardOf(m.items[ctx.at], art))
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
    end if
end sub

sub onCardSelected(event as object)
    at = event.getData()
    if at >= 0 and at < m.items.count() then m.top.open = {item: m.items[at], play: false}
end sub

sub focusRegion()
    if m.region = "page"
        m.page.active = true
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
    if has and m.region = "views"
        out = metrics().focusSpread + 6
        m.viewRing.visible = true
        m.viewRing.translation = [m.viewX[m.view][0] - 18 - out, y + 2 - out]
        m.viewRing.width = m.viewX[m.view][1] + 36 + out * 2
        m.viewRing.height = 56 + out * 2
    end if
end sub

sub onMenuChosen(event as object)
    id = event.getData()
    if left(id, 2) = "f:" or id = "clear"
        chooseFilter(id)
        return
    end if
    m.panelOpen = false
    for each sort in m.detail.sorts
        if sort.id = id then m.sort = sort
    end for
    drawTools()
    loadList()
end sub

sub changeView()
    ' another view starts afresh: no filter chosen, and Down from its tab lands on the toolbar's
    ' first control, not the one this view remembered
    m.chosen = {}
    m.allTotal = invalid
    m.tool = 0
    drawTools()
    drawChips()
    loadList()
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
        api("GET", "/v1/facets" + mid(path, len("/v1/items") + 1) + "&facet=" + spec.id, invalid, "onFacets", {generation: m.generation, id: spec.id})
    end for
end sub

sub onFacets(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if ctx.generation <> m.facetGeneration then return
    m.nextFacets[ctx.id] = []
    if result.ok and result.data.values <> invalid then m.nextFacets[ctx.id] = result.data.values
    if ctx.id = "year" then m.nextFacets.year.reverse()      ' decades oldest first, as tv.html frame 4 lists them
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
    for each spec in m.filterSpecs
        values = m.facets[spec.id]
        chosen = m.chosen[spec.id]
        if (values <> invalid and values.count() > 0) or (chosen <> invalid and chosen.count() > 0)
            rows.push({id: "h:" + spec.id, text: spec.label, heading: true, value: filterSummary(chosen)})
            if values <> invalid
                for i = 0 to values.count() - 1
                    v = values[i]
                    label = v.value
                    if v.label <> invalid then label = v.label
                    rows.push({id: "f:" + spec.id + ":" + i.toStr(), text: label, value: v.count.toStr(), under: "h:" + spec.id, current: isChosen(spec.id, v.value)})
                end for
            end if
        end if
    end for
    if chosenCount() > 0 then rows.push({id: "clear", text: "Clear all", gapBefore: true})
    ' until the new count arrives, the panel says the last one
    if m.total <> invalid then m.matching = kindCount(m.total, m.detail.views[m.view].kind) + " match"
    status = ""
    if m.matching <> invalid then status = m.matching
    for each row in rows
        if row.id = focus then row.focus = true
    end for
    model = {eyebrow: m.top.library.name, title: "Filters", status: status, rows: rows}
    if heading <> "" then model.open = heading
    m.top.menu = model
end sub

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
    if not press or m.detail = invalid or m.region = "page" then return false
    if m.region = "grid"
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
            if m.detail.views.count() > 1 then m.region = "views"
        else if key = "OK"
            spec = m.toolSpecs[m.tool]
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
    else if key <> "up" and key <> "right"
        return false
    end if
    focusRegion()
    return true
end function
