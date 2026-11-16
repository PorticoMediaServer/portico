sub init()
    m.backdrop = m.top.findNode("backdrop")
    m.screens = m.top.findNode("screens")
    m.rail = m.top.findNode("rail")
    m.screen = invalid
    m.task = createObject("roSGNode", "ApiTask")
    m.global.addFields({api: m.task, origin: ""})
    m.task.observeField("signInEnded", "onSignInEnded")
    m.task.control = "run"
    m.rail.observeField("chosen", "onRailChosen")
    m.rail.observeField("left", "onRailLeft")
    if createObject("roAppInfo").isDev()
        m.frames = 0
        m.frameClock = createObject("roTimespan")
        m.top.findNode("frameProbe").observeField("opacity", "onFrame")
        m.top.findNode("frameClock").control = "start"
    end if
end sub

' Development builds print the frames drawn each second, when it falls under 55.
sub onFrame()
    m.frames = m.frames + 1
    ms = m.frameClock.totalMilliseconds()
    if ms >= 1000
        fps = int(m.frames * 1000 / ms)
        if fps < 55 then ? "PORTICO fps "; fps
        m.frames = 0
        m.frameClock.mark()
    end if
end sub

' main.brs hands over the launch parameters once the scene is up; the app starts here.
sub onLaunchArgs()
    if not m.task.ready
        m.task.observeField("ready", "onLaunchArgs")
        return
    end if
    m.task.unobserveField("ready")
    args = m.top.launchArgs
    if createObject("roAppInfo").isDev() and args <> invalid
        ' Development builds only: scripts can hand the app a sign-in, or clear it.
        if args.devSignOut <> invalid then storeWrite({origin: "", refreshToken: "", profileId: "", renewalId: ""})
        if args.devOrigin <> invalid and args.devRefresh <> invalid
            storeWrite({origin: args.devOrigin, refreshToken: args.devRefresh, installationId: args.devInstallation, profileId: args.devProfile, renewalId: ""})
        end if
    end if
    origin = storeRead("origin")
    token = storeRead("refreshToken")
    if origin = "" or token = ""
        return   ' sign-in screens come next
    end if
    m.global.origin = origin
    m.task.session = {origin: origin, refreshToken: token, installationId: installationId()}
    api("GET", "/v1/libraries", invalid, "onLibraries")
    api("GET", "/v1/me", invalid, "onMe")
    api("GET", "/v1/live/channels?limit=1", invalid, "onLive")
    show("HomeScreen")
end sub

sub show(component as string)
    if m.screen <> invalid then m.screens.removeChild(m.screen)
    m.screen = m.screens.createChild(component)
    m.screen.observeField("art", "onArt")
    m.rail.visible = true
    m.rail.current = "home"
    m.screen.active = true
end sub

sub onArt(event as object)
    m.backdrop.art = imageUrl(event.getData(), 1344, 756)
end sub

function libraryIcon(kind as string) as string
    if kind = "movies" or kind = "movie" then return "film"
    if kind = "shows" or kind = "tv" or kind = "anime" then return "tv"
    if kind = "music" then return "music"
    if kind = "audiobooks" or kind = "audiobook" then return "book"
    return "folder"
end function

sub onLibraries(event as object)
    result = event.getData()
    m.libraries = invalid
    entries = [{id: "search", icon: "search", label: "Search"}, {id: "home", icon: "home", label: "Home"}]
    if result.ok and result.data.items <> invalid
        for each library in result.data.items
            ' Reading libraries are not on TV (definition, area 10).
            if library.kind <> "books" and library.kind <> "comics"
                entries.push({id: library.id, icon: libraryIcon(library.kind), label: library.name})
            end if
        end for
    end if
    if m.hasLive = true then entries.push({id: "live", icon: "live", label: "Live TV"})
    entries.push({id: "saved", icon: "bookmark", label: "Saved"})
    m.libraries = result
    m.rail.entries = entries
    m.rail.current = "home"
end sub

' Live TV is in the rail only when this profile has channels to watch.
sub onLive(event as object)
    result = event.getData()
    m.hasLive = result.ok and result.data.items <> invalid and result.data.items.count() > 0
    if m.hasLive and m.libraries <> invalid then onLibrariesAgain()
end sub

sub onLibrariesAgain()
    entries = m.rail.entries
    last = entries.pop()
    entries.push({id: "live", icon: "live", label: "Live TV"})
    entries.push(last)
    m.rail.entries = entries
    m.rail.current = "home"
end sub

sub onMe(event as object)
    result = event.getData()
    if not result.ok then return
    profile = result.data.profile
    if profile <> invalid
        m.rail.profileName = profile.name
        m.rail.profileColour = "0x28587AFF"
    end if
end sub

sub onSignInEnded()
end sub

sub onRailChosen(event as object)
    if m.screen <> invalid then m.screen.active = true
end sub

sub onRailLeft()
    if m.screen <> invalid then m.screen.active = true
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press then return false
    if key = "left" and m.screen <> invalid and m.rail.visible
        ' Left from a page's leftmost element opens the rail.
        m.rail.active = true
        return true
    end if
    return false
end function
