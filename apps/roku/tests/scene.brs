' The scene's own sign-in code (components/MainScene.brs onSignedIn) with stand-ins: the server's id
' is kept beside its address, in the store and in the task's session (security finding F9).

sub main()
    m.failed = 0
    m.passed = 0
    m.store = {}
    m.global = {origin: ""}
    m.task = {session: invalid}
    onSignedIn({data: {origin: "http://home.example", serverName: "Home", serverId: "srv_home",
        session: {accessToken: "a", refreshToken: "r", viewer: {profileId: "p1"}}, profiles: []},
        getData: function() as object : return m.data : end function})
    check("the server's id is stored beside its address", m.store.serverid = "srv_home" and m.store.origin = "http://home.example")
    check("and the task's session carries it", m.task.session.serverId = "srv_home")
    ? "PASSED "; m.passed; " FAILED "; m.failed
end sub

sub check(name as string, ok as boolean)
    if ok
        m.passed = m.passed + 1
    else
        m.failed = m.failed + 1
        ? "FAIL "; name
    end if
end sub

' --- stand-ins for what the scene's other scripts give it ---------------------------------------
sub storeWrite(values as object)
    for each key in values
        m.store[lcase(key)] = values[key]
    end for
end sub

function storeRead(key as string) as string
    value = m.store[lcase(key)]
    if value = invalid then return ""
    return value
end function

function installationId() as string
    return "installation"
end function

function api(method as string, path as string, body as dynamic, callback as string, ctx = invalid as dynamic, isPublic = false as boolean, key = "" as string, origin = "" as string) as object
    return {}
end function
