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
    m.gridState.observeField("pressed", "loadList")
    m.page.observeField("pressed", "onLibrary")
    m.region = "grid"     ' "views", "tools", "grid" or "page"
    m.view = 0
    m.tool = 0
    m.sort = invalid
    m.quick = {}          ' which quick filters are on, by id
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
    if m.detail.views = invalid or m.detail.views.count() = 0 then m.detail.views = [{kind: ""}]
    if m.detail.sorts <> invalid and m.detail.sorts.count() > 0 then m.sort = m.detail.sorts[0]
    ' Sort, then the quick filters this library's titles have the facts for (a music library has no "Unplayed").
    m.toolSpecs = [{id: "sort", text: "Sort"}]
    m.toolSpecs.append(quickFilters(m.detail.filters))
    drawViews()
    drawTools()
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
        if on then b.icon = "check"
        b.switchedOn = on
        if spec.id = "sort" and m.sort <> invalid then b.text = sortName(m.sort.id) else b.text = spec.text
        b.translation = [x, 0]
        m.toolNodes.push(b)
        x = x + b.buttonWidth + 18
        if i = 0 and m.toolSpecs.count() > 1
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

function listPath() as string
    view = m.detail.views[m.view]
    q = "/v1/items?library=" + m.top.library.id
    if view.kind <> "" then q = q + "&kind=" + view.kind
    if m.sort <> invalid then q = q + "&sort=" + m.sort.id + "&order=" + m.sort.order
    for each spec in m.toolSpecs
        if m.quick[spec.id] = true then q = q + "&" + spec.param
    end for
    return q
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
    m.top.findNode("count").text = ""
    api("GET", listPath() + "&limit=60", invalid, "onPage", {generation: m.generation, first: true})
    api("GET", listPath() + "&limit=1&total=true", invalid, "onTotal", {generation: m.generation})
end sub

sub onTotal(event as object)
    result = event.getData()
    if event.getRoSGNode().ctx.generation <> m.generation or not result.ok or result.data.total = invalid then return
    m.top.findNode("count").text = kindCount(result.data.total, m.detail.views[m.view].kind)
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
    for each item in fresh
        card = gridCard(item)
        card.art = imageUrl(card.art, 200, 300)
        m.grid.content.createChild("ContentNode").addFields(card)
    end for
    m.items.append(fresh)
    if ctx.first = true
        if m.items.count() = 0
            reason = "There's nothing in this view yet."
            if m.quick.count() > 0 then reason = "No titles match. Switch a filter off to see more."
            gridSays("empty", "Nothing here", reason, "")
        end if
        if m.items.count() > 0 then m.grid.jumpToItem = 0
        focusRegion()
    end if
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
    for each sort in m.detail.sorts
        if sort.id = id then m.sort = sort
    end for
    drawTools()
    loadList()
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press or m.detail = invalid or m.region = "page" then return false
    if m.region = "grid"
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
        else if key = "down"
            if m.grid.visible or (m.gridState.visible and m.gridState.action <> "") then m.region = "grid"
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
        loadList()
    else if key = "left"
        if m.view = 0 then return false
        m.view = m.view - 1
        loadList()
    else if key = "down"
        m.region = "tools"
    else if key <> "up" and key <> "right"
        return false
    end if
    focusRegion()
    return true
end function
