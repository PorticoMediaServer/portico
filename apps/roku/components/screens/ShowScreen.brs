' Stops down the page: -1 the lockup and its buttons (the opening state, focus on Play), 0 Episodes
' (the season list and the episode row, side by side), 1 More like this, 2 Details. In Episodes, Left from the first episode reaches the season list and Right
' returns to the row at the episode that season remembers.
sub init()
    c = colours()
    f = frames()
    ' a play started from this page: on the way back the row follows what played (onActive)
    m.top.observeField("open", "onOpenAsked")
    m.tabsY = f.title.tabsY
    m.hero = m.top.findNode("hero")
    m.hero.translation = [f.gutter, f.title.copyTop]
    m.hero.observeField("pressed", "onPressed")
    m.sections = m.top.findNode("sections")
    m.part = m.top.findNode("episodesPart")
    m.seasons = m.top.findNode("seasons")
    m.episodes = m.top.findNode("episodes")
    m.rest = m.top.findNode("rest")
    placeEpisodes(f.title, f.gutter)
    m.seasons.observeField("index", "onSeasonFocused")
    m.episodes.observeField("focusedIndex", "describe")
    m.episodes.observeField("selectedIndex", "onEpisodeSelected")
    m.sections.observeField("chosen", "onRelatedChosen")
    m.rest.observeField("fire", "showSeason")
    m.details = {}        ' episode id -> the episode in full, for the band's synopsis
    m.bandRest = m.top.findNode("bandRest")
    m.bandRest.observeField("fire", "readEpisode")
    m.stop = -1
    m.inRow = true        ' focus is in the episode row (not the season list)
    m.list = []           ' the seasons as the server lists them
    m.loaded = {}         ' season id -> its episodes
    m.kept = {}           ' season id -> the episode it remembers
    m.shown = ""          ' the season whose episodes are in the row
    m.started = false
end sub

sub onItem()
    item = m.top.item
    m.full = item
    drawTop()
    m.sections.item = item
    api("GET", "/v1/items/" + item.id, invalid, "onDetail", {id: item.id})
    api("GET", "/v1/items?parent=" + item.id + "&limit=100", invalid, "onSeasons", {id: item.id})
end sub

sub onDetail(event as object)
    result = event.getData()
    if result.ok and event.getRoSGNode().ctx.id = m.top.item.id
        m.sections.item = result.data
        m.full = result.data
        drawTop()
        if m.follow = true then followNext(result.data.nextEpisode)
    end if
end sub

' The episode the row follows back to, once, when it is in this season; else `at` as it was.
function followedTo(seasonId as string, episodes as object, at as integer) as integer
    if m.followTo = invalid or m.followTo.season <> seasonId then return at
    for i = 0 to episodes.count() - 1
        if episodes[i].id = m.followTo.episode
            m.followTo = invalid
            return i
        end if
    end for
    return at
end function

sub onOpenAsked(event as object)
    if event.getData().play = true then m.fromPlayer = true
end sub

' Back from the player after Up next played on, perhaps into another season: the row goes to the
' show's next episode (the one that was playing when it was left part-way, else the one after it),
' on its own season (walk result pages-01, fault 11).
sub followNext(upNext as dynamic)
    m.follow = false
    if upNext = invalid or upNext.parentId = invalid then return
    for i = 0 to m.list.count() - 1
        if m.list[i].id = upNext.parentId
            m.followTo = {season: upNext.parentId, episode: upNext.id}
            m.kept.delete(upNext.parentId)
            if m.seasons.index <> i
                m.seasons.index = i
                m.shown = ""
                showSeason()
            end if
            return
        end if
    end for
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
    ' opened on a season (an episode asked for to look at): that season, whatever is left to play
    for i = 0 to m.list.count() - 1
        if m.list[i].id = m.top.item.seasonId then start = i
    end for
    m.startSeason = start
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
        api("GET", episodesPath(season.id, invalid), invalid, "onEpisodes", {id: season.id, items: []})
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
    m.newest = newestPlayed(episodes).when
    at = m.kept[season.id]
    if at = invalid
        at = 0
        if nextUp > 0 then at = nextUp
        ' opened at an episode: that one
        for i = 0 to episodes.count() - 1
            if episodes[i].id = m.top.item.episodeId and season.id = m.top.item.seasonId then at = i
        end for
    end if
    at = followedTo(season.id, episodes, at)
    if at > 0 then m.episodes.jumpTo = at
    if not m.started
        m.started = true
        ' opened on a season, the page starts folded on its Episodes, focus on the episode
        if m.top.item.seasonId <> invalid
            m.stop = 0
            m.inRow = true
        end if
        if m.top.isInFocusChain() then focusStop()   ' never from the rail, opened while this was loading
    end if
    drawTop()
    describe()
end sub

' A season's episodes, a page at a time: a long season (a daily show, a long anime) is read to
' its end before the row shows, as the season list is.
function episodesPath(seasonId as string, cursor as dynamic) as string
    path = "/v1/items?parent=" + seasonId + "&limit=200"
    if cursor <> invalid then path = path + "&cursor=" + cursor
    return path
end function

sub onEpisodes(event as object)
    result = event.getData()
    ctx = event.getRoSGNode().ctx
    if not result.ok then return
    ctx.items.append(result.data.items)
    if result.data.nextCursor <> invalid
        api("GET", episodesPath(ctx.id, result.data.nextCursor), invalid, "onEpisodes", ctx)
        return
    end if
    m.loaded[ctx.id] = ctx.items
    if m.list.count() > 0 and m.list[m.seasons.index].id = ctx.id then showSeason()
end sub

' The folded page's parts on the frame's lines (the lower group is at 0 once the page folds).
sub placeEpisodes(t as object, gutter as integer)
    c = colours()
    m.sections.translation = [0, t.foldedTabsY]
    m.seasons.translation = [gutter, t.episodesY]
    m.episodes.slotX = t.episodesX
    m.episodes.translation = [0, t.episodesY - shelfMetrics("episode").headingGap]
    m.bandHead = m.top.findNode("bandHead")
    m.bandCode = m.top.findNode("bandCode")
    m.bandFacts = m.top.findNode("bandFacts")
    m.bandText = m.top.findNode("bandText")
    m.bandResume = m.top.findNode("bandResume")
    m.credits = m.top.findNode("credits")
    m.bandHint = m.top.findNode("bandHint")
    m.bandHint.hints = [{keys: ["*"], text: "Episode options"}]
    m.band = {x: t.episodesX, headBottom: t.bandHeadBottom, width: t.creditsX - t.episodesX - 24}
    setType(m.bandHead, "rowHeading", c.text)
    setType(m.bandCode, "metaStrong", c.text)
    setType(m.bandFacts, "meta", c.textSecondary)
    setType(m.bandText, "about", c.textSecondary)
    setType(m.top.findNode("resumeWord"), "metaStrong", c.text)
    setType(m.top.findNode("resumeLeft"), "meta", c.textSecondary)
    m.bandHead.width = m.band.width
    m.bandText.width = m.band.width
    m.bandCode.translation = [t.episodesX, t.bandMetaMid - 18]
    m.bandText.translation = [t.episodesX, t.bandTextY]
    m.bandResume.translation = [t.episodesX, t.bandResumeMid]
    m.top.findNode("resumeTrack").color = c.track
    m.top.findNode("resumeTrack").translation = [0, -3]
    m.top.findNode("resumeBar").color = c.accent
    m.top.findNode("resumeBar").translation = [0, -3]
    m.top.findNode("resumeWord").translation = [236, -18]
    m.credits.translation = [t.creditsX, t.creditsY]
    m.bandHint.translation = [t.creditsX, t.bandHintMid]
end sub

' The episode of a season played last: {at, when} (when is the ISO time, "" for none).
function newestPlayed(episodes as object) as object
    out = {at: -1, when: ""}
    for i = 0 to episodes.count() - 1
        state = episodes[i].state
        if state <> invalid and state.lastPlayedAt <> invalid and state.lastPlayedAt > out.when then out = {at: i, when: state.lastPlayedAt}
    end for
    return out
end function

' The band under the row describes whatever has focus: the season, or the episode. Its heading,
' meta line, synopsis, Resume line and credits column each keep their place when empty (rule 12).
sub describe()
    if m.list.count() = 0 then return
    season = m.list[m.seasons.index]
    episodes = m.loaded[season.id]
    head = ""
    code = ""
    facts = []
    text = ""
    progress = -1
    left = ""
    credits = invalid
    if m.inRow and m.shown = season.id and episodes <> invalid and episodes.count() > 0
        at = m.episodes.focusedIndex
        if at < 0 or at >= episodes.count() then at = 0
        m.kept[season.id] = at
        episode = episodes[at]
        full = m.details[episode.id]
        if full = invalid
            m.bandRest.control = "stop"
            m.bandRest.control = "start"
            full = episode
        end if
        head = episode.title
        code = episodeCode(episode)
        if episode.releaseDate <> invalid then facts.push(dateText(episode.releaseDate))
        if episode.durationMs <> invalid then facts.push(durationText(episode.durationMs))
        if full.contentRating <> invalid then facts.push(full.contentRating)
        if full.summary <> invalid then text = oneParagraph(full.summary)
        progress = progressOf(episode)
        left = timeLeftText(episode)
        credits = full.credits
    else
        head = season.title
        if season.year <> invalid then facts.push(season.year.toStr())
        if season.counts <> invalid then facts.push(countText(season.counts.leaves, "episode", "episodes"))
        if season.summary <> invalid then text = oneParagraph(season.summary)
    end if
    ' the heading sits on the bottom of its two-line slot; a long one takes the second line
    m.bandHead.text = head
    m.bandHead.translation = [m.band.x, m.band.headBottom - m.bandHead.boundingRect().height]
    m.bandCode.text = code
    m.bandFacts.text = facts.join("      ")
    x = m.band.x
    if code <> "" then x = x + m.bandCode.boundingRect().width + 24
    m.bandFacts.translation = [x, m.bandCode.translation[1]]
    m.bandText.text = text
    m.bandResume.visible = (progress >= 0)
    if progress >= 0
        m.top.findNode("resumeBar").width = 220 * progress
        m.top.findNode("resumeLeft").text = ", " + left
        m.top.findNode("resumeLeft").translation = [236 + m.top.findNode("resumeWord").boundingRect().width, -18]
    end if
    drawCredits(credits)
    m.bandHint.visible = (credits <> invalid or code <> "")    ' an episode has a menu; a season has none yet
end sub

' The credits column: up to three guest stars with their parts, then who directed and wrote it.
sub drawCredits(credits as dynamic)
    c = colours()
    m.credits.removeChildrenIndex(m.credits.getChildCount(), 0)
    if credits = invalid then return
    guests = []
    for each credit in credits
        if credit.role = "guest" then guests.push(credit)
    end for
    rows = []
    for i = 0 to guests.count() - 1
        if i < 3
            part = ""
            if guests[i].character <> invalid then part = guests[i].character
            label = ""
            if i = 0 then label = "Guest stars"
            rows.push({label: label, name: guests[i].name, part: part, gap: 0})
        end if
    end for
    if guests.count() > 3 then rows.push({label: "", name: "", part: "and " + (guests.count() - 3).toStr() + " more", gap: 0})
    director = creditNames(credits, "director", 2)
    writer = creditNames(credits, "writer", 2)
    if director <> "" then rows.push({label: "Directed by", name: director, part: "", gap: 8})
    if writer <> "" then rows.push({label: "Written by", name: writer, part: "", gap: 8})
    y = 0
    for each row in rows
        if y > 0 then y = y + row.gap
        addCreditLabel(row.label, "factLabel", c.textTertiary, 0, y, 136)
        nameWidth = 0
        if row.name <> "" then nameWidth = addCreditLabel(row.name, "factLabel", c.text, 144, y, 512) + 10
        if row.part <> "" then addCreditLabel(row.part, "factLabel", c.textTertiary, 144 + nameWidth, y, 512 - nameWidth)
        y = y + 35
    end for
end sub

' One label of the credits column; answers its drawn width.
function addCreditLabel(text as string, role as string, colour as string, x as integer, y as integer, most as integer) as integer
    if text = "" then return 0
    label = m.credits.createChild("Label")
    setType(label, role, colour)
    label.text = text
    if label.boundingRect().width > most then label.width = most
    label.translation = [x, y]
    return label.boundingRect().width
end function

sub readEpisode()
    episode = focusedEpisode()
    if episode = invalid or m.details[episode.id] <> invalid then return
    api("GET", "/v1/items/" + episode.id, invalid, "onEpisodeDetail", {id: episode.id})
end sub

sub onEpisodeDetail(event as object)
    result = event.getData()
    if not result.ok then return
    m.details[event.getRoSGNode().ctx.id] = result.data
    episode = focusedEpisode()
    if episode <> invalid and episode.id = event.getRoSGNode().ctx.id then describe()
end sub

' The episode Play starts: the server's next episode for this viewer (the one in progress, else the
' next unplayed), from the show's detail. Until the detail arrives, the first unplayed one of the
' season the page opens on.
function playTarget() as dynamic
    if m.full <> invalid and m.full.nextEpisode <> invalid then return m.full.nextEpisode
    if m.list.count() = 0 or m.startSeason = invalid then return invalid
    episodes = m.loaded[m.list[m.startSeason].id]
    if episodes = invalid or episodes.count() = 0 then return invalid
    for each episode in episodes
        if episode.state.watched <> true then return episode
    end for
    return episodes[0]
end function

sub drawTop()
    item = m.full
    model = heroFor(item)
    model.left = ""
    model.progress = -1
    stars = creditNames(item.credits, "actor", 3)
    if stars <> "" then model.credits = "Starring " + stars
    m.hero.model = model
    ' a page opened from an episode knows only the show's name until its detail arrives
    if item.images <> invalid then m.top.art = model.art
    target = playTarget()
    buttons = []
    if target <> invalid
        code = episodeCode(target)
        if progressOf(target) >= 0
            buttons.push({id: "play", text: ("Resume " + code).trim(), icon: "play", progress: progressOf(target), detail: timeLeftText(target)})
        else
            buttons.push({id: "play", text: ("Play " + code).trim(), icon: "play"})
        end if
    else
        buttons.push({id: "play", text: "Play", icon: "play"})
    end if
    if item.state = invalid then item.state = {inList: false, watched: false}
    if item.state.inList then buttons.push({id: "list", text: "My List", icon: "check"}) else buttons.push({id: "list", text: "My List", icon: "plus"})
    ' the round buttons: played (the eye; a ticked circle once played), and the show's menu
    if item.state.watched then buttons.push({id: "watched", text: "", icon: "success"}) else buttons.push({id: "watched", text: "", icon: "watched"})
    buttons.push({id: "more", text: "", icon: "more"})
    m.hero.buttons = buttons
end sub

sub onPressed(event as object)
    which = event.getData()
    item = m.full
    if which = "play"
        target = playTarget()
        if target <> invalid then m.top.open = {item: target, play: true, startOver: false}
        return
    end if
    if which = "more"
        openMenu(item)
        return
    end if
    change = {madeAgoMs: 0}
    if which = "list"
        item.state.inList = not item.state.inList
        change.inList = item.state.inList
    else if which = "watched"
        item.state.watched = not item.state.watched
        change.watched = item.state.watched
    end if
    m.full = item
    drawTop()
    api("PUT", "/v1/items/" + item.id + "/state", change, "onSaved")
end sub

' A whole show marked played changes every season's count: the page reads itself again.
sub onSaved(event as object)
    reread()
end sub

sub focusStop()
    atTop = (m.stop = -1)
    m.hero.folded = not atTop
    m.sections.folded = not atTop
    if atTop then m.top.findNode("lower").translation = [0, m.tabsY - frames().title.foldedTabsY] else m.top.findNode("lower").translation = [0, 0]
    m.top.quiet = not atTop
    m.part.visible = (m.stop <= 0)
    m.top.findNode("band").visible = (m.stop = 0)
    if atTop
        m.sections.section = "lead"
        m.hero.active = true
        return
    end if
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
    if not m.top.active then return
    ' back from the player (or a page opened from here): the season's ticks, progress and Up next
    ' are read again; the first time the page is shown counts even before its episodes arrive
    if m.fromPlayer = true
        m.fromPlayer = false
        m.follow = true          ' onDetail moves to the show's next episode, in whatever season
    end if
    if m.shownOnce = true and m.started then reread()
    m.shownOnce = true
    focusStop()
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

' The * menu (and the "..." button) for the show, or for the episode in focus.
sub openMenu(item as object)
    if item = invalid or item.state = invalid then return
    m.top.menu = cardMenu(item, [], ["details", "show"])     ' Details and Go to show are this page
end sub

function focusedEpisode() as dynamic
    if m.list.count() = 0 or not m.inRow then return invalid
    episodes = m.loaded[m.list[m.seasons.index].id]
    at = m.episodes.focusedIndex
    if episodes = invalid or at < 0 or at >= episodes.count() then return invalid
    return episodes[at]
end function

sub onMenuChosen(event as object)
    cardMenuChosen(event.getData())
end sub

sub cardMenuChanged(item as object)
    reread()
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press then return false
    if key = "options"
        item = m.full
        if m.stop = 0 and focusedEpisode() <> invalid then item = focusedEpisode()
        openMenu(item)
        return true
    end if
    hasRelated = m.sections.hasRelated
    if m.stop = -1
        if key = "down" and m.started
            m.stop = 0
            m.inRow = true
            focusStop()
            return true
        end if
        return key = "down"      ' Up from the top goes on to the scene (the now-playing card)
    end if
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
            m.stop = -1
            focusStop()
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

' Read the show (its next episode), the seasons and the shown season's episodes again, keeping focus where it is.
sub reread()
    api("GET", "/v1/items/" + m.top.item.id, invalid, "onDetail", {id: m.top.item.id})
    api("GET", "/v1/items?parent=" + m.top.item.id + "&limit=100", invalid, "onSeasonsAgain", {id: m.top.item.id})
    if m.shown <> "" then api("GET", episodesPath(m.shown, invalid), invalid, "onEpisodesAgain", {id: m.shown, items: []})
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
    ctx = event.getRoSGNode().ctx
    id = ctx.id
    if not result.ok or id <> m.shown then return
    ctx.items.append(result.data.items)
    if result.data.nextCursor <> invalid
        api("GET", episodesPath(id, result.data.nextCursor), invalid, "onEpisodesAgain", ctx)
        return
    end if
    m.loaded = {}                  ' other seasons are read again when they are next shown
    m.loaded[id] = ctx.items
    at = m.episodes.focusedIndex
    ' back from the player after it played on (Up next): the row is at the episode last played,
    ' the one that was playing, not the one first opened
    newest = newestPlayed(ctx.items)
    if newest.at >= 0 and m.newest <> invalid and newest.when > m.newest then at = newest.at
    m.newest = newest.when
    at = followedTo(id, ctx.items, at)
    m.kept[id] = at
    m.shown = ""
    showSeason()
end sub
