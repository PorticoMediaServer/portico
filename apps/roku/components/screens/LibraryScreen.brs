sub init()
    c = colours()
    m.grid = m.top.findNode("grid")
    m.views = m.top.findNode("views")
    m.tools = m.top.findNode("tools")
    m.toolRing = m.top.findNode("toolRing")
    m.notice = m.top.findNode("notice")
    setType(m.top.findNode("name"), "title", c.text)
    setType(m.top.findNode("count"), "body", c.textSecondary)
    m.grid.clippingRect = [-60, -30, 1840, 834]
    m.grid.observeField("itemFocused", "onCardFocused")
    m.grid.observeField("itemSelected", "onCardSelected")
    m.notice.observeField("pressed", "load")
    m.region = "grid"     ' "views", "tools" or "grid"
    m.view = 0
    m.tool = 0
    m.sort = invalid
    m.quick = {}          ' which quick filters are on
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
    api("GET", "/v1/libraries/" + library.id, invalid, "onDetail", {id: library.id})
end sub

sub onDetail(event as object)
    result = event.getData()
    if event.getRoSGNode().ctx.id <> m.top.library.id then return
    if not result.ok
        say()
        return
    end if
    m.detail = result.data
    if m.detail.views = invalid or m.detail.views.count() = 0 then m.detail.views = [{kind: ""}]
    if m.detail.sorts <> invalid and m.detail.sorts.count() > 0 then m.sort = m.detail.sorts[0]
    drawChrome()
    load()
end sub

sub say()
    m.grid.visible = false
    m.notice.icon = "warning"
    m.notice.title = "Can't reach the server"
    m.notice.body = "You're still signed in. Try again in a moment."
    m.notice.buttons = [{id: "retry", text: "Try again now", icon: "refresh"}]
    m.notice.visible = true
    m.notice.active = true
end sub

' The views beside the name, and the toolbar: Sort, then the quick filters.
sub drawChrome()
    c = colours()
    m.views.removeChildrenIndex(m.views.getChildCount(), 0)
    x = 200 + m.top.findNode("name").boundingRect().width + 56
    m.viewX = []
    for each view in m.detail.views
        label = m.views.createChild("Label")
        label.text = viewName(view.kind)
        label.translation = [x, 0]
        setType(label, "section", c.textTertiary)
        m.viewX.push([x, label.boundingRect().width])
        x = x + label.boundingRect().width + 40
    end for
    m.views.visible = (m.detail.views.count() > 1)
    m.toolSpecs = [{id: "sort", text: "Sort"}, {id: "unplayed", text: "Unplayed"}, {id: "progress", text: "In progress"}, {id: "favorite", text: "Favorites"}, {id: "list", text: "My List"}]
    drawTools()
end sub

sub drawTools()
    c = colours()
    m.tools.removeChildrenIndex(m.tools.getChildCount(), 0)
    m.toolX = []
    x = 0
    for i = 0 to m.toolSpecs.count() - 1
        spec = m.toolSpecs[i]
        text = spec.text
        if spec.id = "sort" and m.sort <> invalid then text = sortName(m.sort.id)
        on = (m.quick[spec.id] = true)
        focused = (m.region = "tools" and m.tool = i and m.top.isInFocusChain())
        plate = m.tools.createChild("Poster")
        plate.uri = "pkg:/images/round-14.9.png"
        plate.height = 64
        label = m.tools.createChild("Label")
        label.text = text
        label.height = 64
        label.vertAlign = "center"
        setType(label, "chip", c.textSecondary)
        w = label.boundingRect().width + 52
        plate.width = w
        plate.translation = [x, 0]
        label.translation = [x + 26, 0]
        if focused
            plate.blendColor = "0xD8E6EEFF"
            label.color = c.actionInk
        else if on
            plate.blendColor = "0x2F86BBFF"
            label.color = c.text
        else
            plate.blendColor = "0x1D3848E6"
        end if
        m.toolX.push([x, w])
        x = x + w + 14
        if i = 0 then x = x + 22      ' Sort stands apart from the quick filters
    end for
    paint()
end sub

function query() as string
    view = m.detail.views[m.view]
    q = "/v1/items?library=" + m.top.library.id
    if view.kind <> "" then q = q + "&kind=" + view.kind
    if m.sort <> invalid then q = q + "&sort=" + m.sort.id + "&order=" + m.sort.order
    if m.quick.unplayed = true then q = q + "&watched=false"
    if m.quick.progress = true then q = q + "&inProgress=true"
    if m.quick.favorite = true then q = q + "&favorite=true"
    if m.quick.list = true then q = q + "&inList=true"
    return q
end function

' Read the list from its start (a new view, sort or filter). The page never waits for the count.
sub load()
    if m.detail = invalid then return
    m.notice.visible = false
    m.grid.visible = true
    m.generation = m.generation + 1
    m.items = []
    m.cursor = invalid
    m.asking = true
    m.top.findNode("count").text = ""
    api("GET", query() + "&limit=60", invalid, "onPage", {generation: m.generation, first: true})
    api("GET", query() + "&limit=1&total=true", invalid, "onTotal", {generation: m.generation})
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
        if ctx.first = true then say()
        return
    end if
    fresh = withoutReading(result.data.items)
    m.cursor = result.data.nextCursor
    if ctx.first = true
        content = createObject("roSGNode", "ContentNode")
        m.grid.content = content
    end if
    for each item in fresh
        card = gridCard(item)
        card.art = imageUrl(card.art, 200, 300)
        m.grid.content.createChild("ContentNode").addFields(card)
    end for
    m.items.append(fresh)
    if ctx.first = true
        if m.items.count() = 0
            m.grid.visible = false
            m.notice.icon = ""
            m.notice.title = "Nothing here"
            m.notice.body = "No titles match. Change the filters to see more."
            m.notice.buttons = []
            m.notice.visible = true
            if m.region = "grid" then m.region = "tools"
        end if
        focusRegion()
    end if
end sub

sub onCardFocused(event as object)
    at = event.getData()
    ' the grid keeps going: the next page is asked for about three rows before the end
    if m.cursor <> invalid and not m.asking and at >= m.items.count() - 21
        m.asking = true
        api("GET", query() + "&limit=60&cursor=" + m.cursor, invalid, "onPage", {generation: m.generation, first: false})
    end if
end sub

sub onCardSelected(event as object)
    at = event.getData()
    if at >= 0 and at < m.items.count() then m.top.open = {item: m.items[at], play: false}
end sub

sub focusRegion()
    if m.region = "grid" and m.grid.visible then m.grid.setFocus(true) else m.top.setFocus(true)
    drawTools()
end sub

sub onActive()
    if m.top.active then focusRegion()
end sub

sub onToTop()
    if m.grid.visible and m.items.count() > 0 then m.grid.jumpToItem = 0
    m.region = "grid"
end sub

sub paint()
    c = colours()
    has = m.top.isInFocusChain()
    if m.viewX <> invalid
        for i = 0 to m.views.getChildCount() - 1
            label = m.views.getChild(i)
            if i = m.view then label.color = c.text else label.color = c.textTertiary
        end for
        mark = m.top.findNode("viewMark")
        mark.visible = m.views.visible
        if m.viewX.count() > m.view
            mark.translation = [m.viewX[m.view][0], 74 + 44]
            mark.width = m.viewX[m.view][1]
        end if
    end if
    out = metrics().focusSpread + 6
    m.toolRing.visible = false
    if has and m.region = "tools" and m.toolX <> invalid and m.toolX.count() > m.tool
        m.toolRing.visible = true
        m.toolRing.translation = [200 + m.toolX[m.tool][0] - out, 144 - out]
        m.toolRing.width = m.toolX[m.tool][1] + out * 2
        m.toolRing.height = 64 + out * 2
    else if has and m.region = "views" and m.viewX <> invalid
        m.toolRing.visible = true
        m.toolRing.translation = [m.viewX[m.view][0] - 18 - out, 62 - out]
        m.toolRing.width = m.viewX[m.view][1] + 36 + out * 2
        m.toolRing.height = 56 + out * 2
    end if
end sub

sub onMenuChosen(event as object)
    id = event.getData()
    for each sort in m.detail.sorts
        if sort.id = id then m.sort = sort
    end for
    drawTools()
    load()
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press or m.detail = invalid then return false
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
            if m.grid.visible then m.region = "grid"
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
            m.quick[spec.id] = not (m.quick[spec.id] = true)
            drawTools()
            load()
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
        load()
    else if key = "left"
        if m.view = 0 then return false
        m.view = m.view - 1
        load()
    else if key = "down"
        m.region = "tools"
    else if key <> "up" and key <> "right"
        return false
    end if
    focusRegion()
    return true
end function
