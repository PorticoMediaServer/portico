' Stops down the page: 0 Episodes (the season list and the episode row, side by side), 1 More like
' this, 2 Details. In Episodes, Left from the first episode reaches the season list and Right
' returns to the row at the episode that season remembers.
sub init()
    c = colours()
    m.sections = m.top.findNode("sections")
    m.part = m.top.findNode("episodesPart")
    m.seasons = m.top.findNode("seasons")
    m.episodes = m.top.findNode("episodes")
    m.rest = m.top.findNode("rest")
    setType(m.top.findNode("title"), "pageTitle", c.text)
    setType(m.top.findNode("bandHead"), "bodyStrong", c.text)
    setType(m.top.findNode("bandFacts"), "body", c.textSecondary)
    setType(m.top.findNode("bandText"), "about", c.textSecondary)
    m.seasons.observeField("index", "onSeasonFocused")
    m.episodes.observeField("focusedIndex", "describe")
    m.episodes.observeField("selectedIndex", "onEpisodeSelected")
    m.sections.observeField("chosen", "onRelatedChosen")
    m.rest.observeField("fire", "showSeason")
    m.stop = 0
    m.inRow = true        ' focus is in the episode row (not the season list)
    m.list = []           ' the seasons as the server lists them
    m.loaded = {}         ' season id -> its episodes
    m.kept = {}           ' season id -> the episode it remembers
    m.shown = ""          ' the season whose episodes are in the row
    m.started = false
end sub

sub onItem()
    item = m.top.item
    m.top.findNode("title").text = item.title
    m.top.art = pageArt(item)
    m.top.quiet = true
    m.sections.item = item
    api("GET", "/v1/items/" + item.id, invalid, "onDetail", {id: item.id})
    api("GET", "/v1/items?parent=" + item.id + "&limit=100", invalid, "onSeasons", {id: item.id})
end sub

sub onDetail(event as object)
    result = event.getData()
    if result.ok and event.getRoSGNode().ctx.id = m.top.item.id then m.sections.item = result.data
end sub

sub onSeasons(event as object)
    result = event.getData()
    if not result.ok or event.getRoSGNode().ctx.id <> m.top.item.id then return
    m.list.append(result.data.items)
    if result.data.nextCursor <> invalid
        api("GET", "/v1/items?parent=" + m.top.item.id + "&limit=100&cursor=" + result.data.nextCursor, invalid, "onSeasons", {id: m.top.item.id})
        return
    end if
    rows = []
    start = -1
    for i = 0 to m.list.count() - 1
        season = m.list[i]
        line = seasonLine(season)
        rows.push({title: season.title, text: line.text, progress: line.progress})
        ' start on the first season with something left to play
        ' (Specials, season 0, only when nothing else is left)
        if start < 0 and season.counts <> invalid and season.counts.unwatchedLeaves > 0 and season.index <> 0 then start = i
    end for
    if start < 0 then start = 0
    m.seasons.seasons = rows
    m.seasons.index = start
    showSeason()
end sub

sub onSeasonFocused()
    describe()
    m.rest.control = "stop"
    m.rest.control = "start"      ' the row follows once the list rests
end sub

sub showSeason()
    if m.list.count() = 0 then return
    season = m.list[m.seasons.index]
    if m.loaded[season.id] = invalid
        api("GET", "/v1/items?parent=" + season.id + "&limit=100", invalid, "onEpisodes", {id: season.id})
        return
    end if
    if m.shown = season.id then return
    m.shown = season.id
    episodes = m.loaded[season.id]
    nextUp = -1
    for i = 0 to episodes.count() - 1
        if nextUp < 0 and episodes[i].state.watched <> true then nextUp = i
    end for
    cards = createObject("roSGNode", "ContentNode")
    for i = 0 to episodes.count() - 1
        card = episodeCard(episodes[i], i = nextUp and season.counts <> invalid and season.counts.unwatchedLeaves < season.counts.leaves)
        card.art = imageUrl(card.art, 384, 216)
        cards.createChild("ContentNode").addFields(card)
    end for
    m.episodes.cards = cards
    at = m.kept[season.id]
    if at = invalid
        at = 0
        if nextUp > 0 then at = nextUp
    end if
    if at > 0 then m.episodes.jumpTo = at
    if not m.started
        m.started = true
        focusStop()
    end if
    describe()
end sub

sub onEpisodes(event as object)
    result = event.getData()
    id = event.getRoSGNode().ctx.id
    if not result.ok then return
    m.loaded[id] = result.data.items
    if m.list.count() > 0 and m.list[m.seasons.index].id = id then showSeason()
end sub

' The band under the row describes whatever has focus: the season, or the episode.
sub describe()
    if m.list.count() = 0 then return
    season = m.list[m.seasons.index]
    head = m.top.findNode("bandHead")
    facts = m.top.findNode("bandFacts")
    text = m.top.findNode("bandText")
    episodes = m.loaded[season.id]
    if m.inRow and m.shown = season.id and episodes <> invalid and episodes.count() > 0
        at = m.episodes.focusedIndex
        if at < 0 or at >= episodes.count() then at = 0
        m.kept[season.id] = at
        episode = episodes[at]
        head.text = episode.title
        line = ""
        if episodeCode(episode) <> "" then line = episodeCode(episode)
        if episode.releaseDate <> invalid then line = line + "      " + dateText(episode.releaseDate)
        if episode.durationMs <> invalid then line = line + "      " + durationText(episode.durationMs)
        facts.text = line.trim()
        text.text = ""
        if episode.summary <> invalid then text.text = episode.summary
    else
        head.text = season.title
        line = ""
        if season.year <> invalid then line = season.year.toStr() + "      "
        if season.counts <> invalid then line = line + countText(season.counts.leaves, "episode", "episodes")
        facts.text = line
        text.text = ""
        if season.summary <> invalid then text.text = season.summary
    end if
    ' the facts follow the name on its line; a name too long for that takes the line, and the facts go
    head.width = 0
    across = head.boundingRect().width
    if across > 760
        head.width = 1220
        facts.text = ""
    end if
    facts.translation = [600 + across + 28, 640]
end sub

sub focusStop()
    m.part.visible = (m.stop = 0)
    if m.stop = 0
        m.sections.section = "lead"
        if m.inRow and m.shown <> "" then m.episodes.active = true else m.seasons.active = true
        describe()
    else
        if m.stop = 1 then m.sections.section = "related" else m.sections.section = "details"
        m.sections.active = true
    end if
end sub

sub onActive()
    if m.top.active and m.started
        ' back from the player: the season's ticks, progress and Up next are read again
        if m.shownOnce = true then reread()
        m.shownOnce = true
    end if
    if m.top.active and m.started then focusStop()
    if m.top.active and not m.started then m.top.setFocus(true)
end sub

sub onEpisodeSelected()
    if m.list.count() = 0 then return
    episodes = m.loaded[m.list[m.seasons.index].id]
    at = m.episodes.focusedIndex
    if episodes <> invalid and at >= 0 and at < episodes.count() then m.top.open = {item: episodes[at], play: true, startOver: false}
end sub

sub onRelatedChosen(event as object)
    m.top.open = {item: event.getData(), play: false}
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press then return false
    hasRelated = m.sections.hasRelated
    if m.stop = 0
        if key = "left" and m.inRow
            m.inRow = false
            focusStop()
            return true
        else if key = "right" and not m.inRow
            m.rest.control = "stop"
            showSeason()
            if m.shown = m.list[m.seasons.index].id
                m.inRow = true
                focusStop()
            end if
            return true
        else if key = "down"
            if hasRelated then m.stop = 1 else m.stop = 2
            focusStop()
            return true
        else if key = "up"
            return true
        end if
        return false     ' Left from the season list: the rail
    end if
    if key = "down"
        if m.stop = 1
            m.stop = 2
            focusStop()
        end if
        return true
    else if key = "up"
        if m.stop = 2 and hasRelated then m.stop = 1 else m.stop = 0
        focusStop()
        return true
    end if
    return false
end function

' Read the seasons and the shown season's episodes again, keeping focus where it is.
sub reread()
    api("GET", "/v1/items?parent=" + m.top.item.id + "&limit=100", invalid, "onSeasonsAgain", {id: m.top.item.id})
    if m.shown <> "" then api("GET", "/v1/items?parent=" + m.shown + "&limit=100", invalid, "onEpisodesAgain", {id: m.shown})
end sub

sub onSeasonsAgain(event as object)
    result = event.getData()
    if not result.ok or result.data.items.count() <> m.list.count() then return
    m.list = result.data.items
    rows = []
    for each season in m.list
        line = seasonLine(season)
        rows.push({title: season.title, text: line.text, progress: line.progress})
    end for
    at = m.seasons.index
    m.seasons.seasons = rows
    m.seasons.index = at
end sub

sub onEpisodesAgain(event as object)
    result = event.getData()
    id = event.getRoSGNode().ctx.id
    if not result.ok or id <> m.shown then return
    m.loaded = {}                  ' other seasons are read again when they are next shown
    m.loaded[id] = result.data.items
    at = m.episodes.focusedIndex
    m.kept[id] = at
    m.shown = ""
    showSeason()
end sub
