sub init()
    m.page = m.top.findNode("page")
    m.hero = m.top.findNode("hero")
    m.slide = m.top.findNode("slide")
    m.slideTo = m.top.findNode("slideTo")
    m.turn = m.top.findNode("turn")
    m.rows = []        ' {row, shelf, y}; shelf is made when focus comes near
    m.spots = []
    m.spot = 0
    m.focus = -2       ' -2 nothing yet, -1 the spotlight, otherwise a row
    m.hero.observeField("pressed", "onHeroPressed")
    m.turn.observeField("fire", "onTurn")
    api("GET", "/v1/home", invalid, "onHome")
end sub

sub onHome(event as object)
    result = event.getData()
    if not result.ok then return   ' the scene shows the reason (states come with the shared notice)
    home = result.data
    m.spots = []
    if home.spotlight <> invalid then m.spots = home.spotlight
    y = 0
    for each row in home.rows
        if row.hidden <> true and row.items <> invalid and row.items.count() > 0
            m.rows.push({row: row, shelf: invalid, y: y})
            y = y + shelfMetrics(row.shape).block
        end if
    end for
    m.hero.visible = true
    if m.spots.count() > 0 then moveTo(-1) else moveTo(0)
    if m.spots.count() > 1 then m.turn.control = "start"
end sub

sub onActive()
    if not m.top.active or m.focus = -2 then return
    if m.focus = -1 then m.hero.active = true else m.rows[m.focus].shelf.active = true
end sub

function makeShelf(i as integer) as object
    entry = m.rows[i]
    if entry.shelf <> invalid then return entry.shelf
    row = entry.row
    shelf = m.page.createChild("Shelf")
    shelf.translation = [0, entry.y]
    shelf.shape = row.shape
    shelf.heading = row.title
    if row.reason <> invalid then shelf.subheading = row.reason
    cards = createObject("roSGNode", "ContentNode")
    g = shelfMetrics(row.shape).card
    for each item in row.items
        card = cardFor(item, row.shape)
        card.art = imageUrl(card.art, g[0], g[1])
        cards.createChild("ContentNode").addFields(card)
    end for
    shelf.cards = cards
    shelf.id = "shelf" + i.toStr()
    shelf.observeField("focusedIndex", "onCardFocused")
    shelf.observeField("selectedIndex", "onCardSelected")
    entry.shelf = shelf
    return shelf
end function

' Move focus to the spotlight (-1) or a row, and bring the page to it.
sub moveTo(i as integer)
    if i < -1 or i >= m.rows.count() then return
    for n = 0 to i + 2
        if n < m.rows.count() then makeShelf(n)
    end for
    m.focus = i
    pageY = 830
    if i = -1
        m.hero.mode = "spotlight"
        showSpot()
        m.hero.visible = true
        m.hero.active = true
    else
        m.hero.mode = "preview"
        m.hero.visible = (i = 0)
        pageY = 507 - m.rows[i].y
        if i = 0 then pageY = 595
        m.rows[i].shelf.active = true
        describe()
    end if
    m.slide.control = "stop"
    m.slideTo.keyValue = [m.page.translation, [0, pageY]]
    m.slide.control = "start"
end sub

function focusedItem() as dynamic
    if m.focus < 0 then return invalid
    entry = m.rows[m.focus]
    at = entry.shelf.focusedIndex
    if at < 0 or at >= entry.row.items.count() then at = 0
    return entry.row.items[at]
end function

' The hero and the backdrop follow the card in focus.
sub describe()
    item = focusedItem()
    if item = invalid then return
    model = heroFor(item)
    m.top.art = model.art
    if m.focus <> 0 then return
    ok = "Details"
    if m.rows[0].row.id = "continue" then ok = "Resume"
    m.hero.okText = ok
    m.hero.model = model
end sub

sub showSpot()
    spot = m.spots[m.spot]
    model = heroFor(spot.entry)
    model.eyebrow = spot.reason.text.fallback
    buttons = []
    if spot.play
        text = "Play"
        target = spot.resumeTarget
        if target <> invalid
            if progressOf(target) >= 0
                text = "Resume"
            else if target.kind = "episode" and episodeCode(target) <> ""
                text = "Play " + episodeCode(target)
            end if
        end if
        buttons.push({id: "play", text: text, icon: "play"})
    end if
    if spot.myList
        if spot.entry.state.inList
            buttons.push({id: "list", text: "My List", icon: "check"})
        else
            buttons.push({id: "list", text: "My List", icon: "plus"})
        end if
    end if
    buttons.push({id: "details", text: "Details", icon: ""})
    m.hero.pages = m.spots.count()
    m.hero.page = m.spot
    m.hero.buttons = buttons
    m.hero.model = model
    m.top.art = model.art
end sub

' The spotlight turns by itself only while it is the opening state; focus stays where it is.
sub onTurn()
    if m.focus <> -1 then return
    m.spot = (m.spot + 1) mod m.spots.count()
    showSpot()
end sub

sub onCardFocused(event as object)
    if m.focus >= 0 and event.getRoSGNode().isSameNode(m.rows[m.focus].shelf) then describe()
end sub

sub onCardSelected(event as object)
    item = focusedItem()
    if item <> invalid then m.top.open = {item: item, play: m.rows[m.focus].row.id = "continue"}
end sub

sub onHeroPressed(event as object)
    spot = m.spots[m.spot]
    which = event.getData()
    if which = "play"
        target = spot.entry
        if spot.resumeTarget <> invalid then target = spot.resumeTarget
        m.top.open = {item: target, play: true}
    else if which = "details"
        m.top.open = {item: spot.entry, play: false}
    end if
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press or m.focus = -2 then return false
    if key = "down"
        moveTo(m.focus + 1)
        return true
    else if key = "up"
        if m.focus > 0 or (m.focus = 0 and m.spots.count() > 0) then moveTo(m.focus - 1)
        return true
    else if key = "back"
        ' Back from anywhere on Home is the opening state; from there it leaves the app.
        if m.focus >= 0 and m.spots.count() > 0
            moveTo(-1)
            return true
        end if
    end if
    return false
end function
